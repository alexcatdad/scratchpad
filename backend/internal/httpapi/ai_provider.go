package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type providerSnapshot struct {
	config     AIConfig
	key        string
	generation float64
}

func aiUnavailable() error {
	return &protocolError{"AI_UNAVAILABLE", "AI provider is unavailable or returned invalid output.", 503}
}
func aiChanged() error {
	return &protocolError{"AI_CONFIGURATION_CHANGED", "AI configuration changed; retry using current settings.", 503}
}
func (a *AI) provider(ctx context.Context) (providerSnapshot, error) {
	row, err := entityMap(ctx, a.auth.Store.DB, "ai_settings", "global")
	if err == sql.ErrNoRows {
		row = nil
		err = nil
	}
	if err != nil {
		return providerSnapshot{}, err
	}
	cfg, err := aiConfig(row)
	if err != nil {
		return providerSnapshot{}, err
	}
	if !cfg.Enabled {
		return providerSnapshot{}, &protocolError{"AI_DISABLED", "Enable optional AI first.", 409}
	}
	key, err := a.box.Open(row["apiKey"], "ai_settings", "global", "apiKey")
	if err != nil {
		return providerSnapshot{}, secretUnavailable()
	}
	generation, _ := row["dispatchGeneration"].(float64)
	return providerSnapshot{cfg, key, generation}, nil
}
func (a *AI) providerCurrent(ctx context.Context, conn *sql.Conn, r *http.Request, snapshot providerSnapshot) error {
	if r != nil {
		if _, err := apiActor(ctx, conn, r, a.auth.Origin); err != nil {
			return err
		}
	}
	row, err := entityMap(ctx, conn, "ai_settings", "global")
	if err != nil {
		return err
	}
	generation, _ := row["dispatchGeneration"].(float64)
	if generation != snapshot.generation || row["enabled"] != true {
		return aiChanged()
	}
	return nil
}
func (a *AI) postProvider(ctx context.Context, r *http.Request, snapshot providerSnapshot, path string, body any) ([]byte, error) {
	return a.postProviderGuarded(ctx, r, snapshot, path, body, nil)
}
func (a *AI) postProviderGuarded(ctx context.Context, r *http.Request, snapshot providerSnapshot, path string, body any, guard func(*sql.Conn) error) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, aiUnavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(snapshot.config.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(snapshot.config.BaseURL, "/")+"/"+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, aiUnavailable()
	}
	req.Header.Set("Content-Type", "application/json")
	if snapshot.key != "" {
		req.Header.Set("Authorization", "Bearer "+snapshot.key)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	type response struct {
		value *http.Response
		err   error
	}
	done := make(chan response, 1)
	dispatched := false
	err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		if r == nil && guard == nil {
			return aiUnavailable()
		}
		if err := a.providerCurrent(ctx, conn, r, snapshot); err != nil {
			return err
		}
		if guard != nil {
			if err := guard(conn); err != nil {
				return err
			}
		}
		// Admit the request under the configuration lock, then release it
		// without waiting for provider headers or body.
		started := make(chan struct{})
		go func() { close(started); value, err := client.Do(req); done <- response{value, err} }()
		<-started
		dispatched = true
		return nil
	})
	if err != nil {
		cancel()
		if dispatched {
			result := <-done
			if result.value != nil {
				result.value.Body.Close()
			}
		}
		return nil, err
	}
	result := <-done
	if result.err != nil {
		return nil, aiUnavailable()
	}
	defer result.value.Body.Close()
	if result.value.StatusCode < 200 || result.value.StatusCode >= 300 {
		return nil, aiUnavailable()
	}
	data, err := io.ReadAll(io.LimitReader(result.value.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 || !json.Valid(data) {
		return nil, aiUnavailable()
	}
	return data, nil
}
func (a *AI) TestProvider() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := apiActor(r.Context(), a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		snapshot, err := a.provider(r.Context())
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(snapshot.config.RequestTimeoutSeconds*2+5)*time.Second)
		defer cancel()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Duration(snapshot.config.RequestTimeoutSeconds*2+5) * time.Second))
		data, err := a.postProvider(ctx, r, snapshot, "embeddings", map[string]any{"model": snapshot.config.EmbeddingModel, "input": []string{"Synthetic Scratchpad provider connectivity test"}})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		var embeddings struct {
			Data []struct {
				Index     *int       `json:"index"`
				Embedding []*float64 `json:"embedding"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &embeddings) != nil || len(embeddings.Data) != 1 || embeddings.Data[0].Index == nil || *embeddings.Data[0].Index != 0 || len(embeddings.Data[0].Embedding) != snapshot.config.EmbeddingDimensions {
			a.auth.fail(w, aiUnavailable())
			return
		}
		nonzero := false
		for _, value := range embeddings.Data[0].Embedding {
			if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
				a.auth.fail(w, aiUnavailable())
				return
			}
			if *value != 0 {
				nonzero = true
			}
		}
		if !nonzero {
			a.auth.fail(w, aiUnavailable())
			return
		}
		body := map[string]any{"model": snapshot.config.Model, "temperature": 0, "max_tokens": snapshot.config.MaxOutputTokens, "messages": []any{map[string]any{"role": "system", "content": "Return JSON {\"ok\":true}. This is a synthetic connectivity test."}, map[string]any{"role": "user", "content": "{\"test\":true}"}}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "scratchpad_result", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean", "const": true}}, "required": []string{"ok"}, "additionalProperties": false}}}}
		if snapshot.config.ReasoningEffort != "default" {
			body["reasoning_effort"] = snapshot.config.ReasoningEffort
		}
		data, err = a.postProvider(ctx, r, snapshot, "chat/completions", body)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		var completed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		var answer map[string]any
		if json.Unmarshal(data, &completed) != nil || len(completed.Choices) == 0 || json.Unmarshal([]byte(completed.Choices[0].Message.Content), &answer) != nil || answer["ok"] != true {
			a.auth.fail(w, aiUnavailable())
			return
		}
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error { return a.providerCurrent(ctx, conn, r, snapshot) })
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"ok": true, "model": snapshot.config.Model, "embeddingModel": snapshot.config.EmbeddingModel, "dimensions": len(embeddings.Data[0].Embedding)})
	}
}
