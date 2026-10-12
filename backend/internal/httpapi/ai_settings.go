package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/alexcatdad/scratchpad/backend/internal/secrets"
)

type AI struct {
	auth *SSH
	box  *secrets.Box
	rag  bool
}
type AIConfig struct {
	Enabled               bool     `json:"enabled"`
	BaseURL               string   `json:"baseUrl"`
	Model                 string   `json:"model"`
	EmbeddingModel        string   `json:"embeddingModel"`
	EmbeddingDimensions   int      `json:"embeddingDimensions"`
	SimilarityThreshold   float64  `json:"similarityThreshold"`
	AnalysisModes         []string `json:"analysisModes"`
	MaxOutputTokens       int      `json:"maxOutputTokens"`
	RequestTimeoutSeconds int      `json:"requestTimeoutSeconds"`
	ReasoningEffort       string   `json:"reasoningEffort"`
	ScheduleMinutes       int      `json:"scheduleMinutes"`
}

func NewAI(auth *SSH, box *secrets.Box, rag bool) *AI { return &AI{auth: auth, box: box, rag: rag} }

var aiFields = []string{"enabled", "baseUrl", "model", "embeddingModel", "embeddingDimensions", "similarityThreshold", "analysisModes", "maxOutputTokens", "requestTimeoutSeconds", "reasoningEffort", "scheduleMinutes"}

func aiInvalid() error { return &protocolError{"VALIDATION_FAILED", "Invalid AI configuration.", 400} }
func secretUnavailable() error {
	return &protocolError{"SECRET_UNAVAILABLE", "The provider secret is unavailable. Supply the retained installation key or replace/remove the provider token.", 503}
}
func aiDefaults() AIConfig {
	return AIConfig{false, "http://host.docker.internal:1234/v1", "qwen/qwen3.8-27b", "text-embedding-qwen3-embedding-4b", 2560, 0.5, append([]string{}, artifactKinds[:len(artifactKinds)-1]...), 4096, 600, "none", 60}
}
func aiConfig(row map[string]any) (AIConfig, error) {
	cfg := aiDefaults()
	selected := map[string]any{}
	for _, field := range aiFields {
		if value, exists := row[field]; exists {
			if value == nil {
				return cfg, aiInvalid()
			}
			selected[field] = value
		}
	}
	encoded, err := json.Marshal(selected)
	if err != nil || json.Unmarshal(encoded, &cfg) != nil {
		return cfg, aiInvalid()
	}
	for _, value := range []string{cfg.Model, cfg.EmbeddingModel} {
		if value == "" || len(utf16.Encode([]rune(value))) > 200 {
			return cfg, aiInvalid()
		}
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return cfg, aiInvalid()
	}
	if cfg.EmbeddingDimensions < 1 || cfg.EmbeddingDimensions > 16384 || cfg.SimilarityThreshold < -1 || cfg.SimilarityThreshold > 1 || cfg.MaxOutputTokens < 256 || cfg.MaxOutputTokens > 32768 || cfg.RequestTimeoutSeconds < 5 || cfg.RequestTimeoutSeconds > 3600 || cfg.ScheduleMinutes < 0 || cfg.ScheduleMinutes > 43200 {
		return cfg, aiInvalid()
	}
	if !contains([]string{"default", "none", "low", "medium", "high", "xhigh"}, cfg.ReasoningEffort) {
		return cfg, aiInvalid()
	}
	for _, mode := range cfg.AnalysisModes {
		if !contains(artifactKinds, mode) {
			return cfg, aiInvalid()
		}
	}
	return cfg, nil
}
func aiConfigMap(cfg AIConfig) map[string]any {
	raw, _ := json.Marshal(cfg)
	var value map[string]any
	json.Unmarshal(raw, &value)
	return value
}
func (a *AI) public(row map[string]any) (map[string]any, error) {
	cfg, err := aiConfig(row)
	if err != nil {
		return nil, err
	}
	result := aiConfigMap(cfg)
	version := any(float64(0))
	if row != nil {
		version = row["version"]
	}
	result["version"], result["apiKeyConfigured"], result["secretProtection"] = version, row["apiKey"] != nil && row["apiKey"] != "", a.box.Protection(row["apiKey"])
	return result, nil
}
func aiExpected(body map[string]any, r *http.Request) (float64, error) {
	value := body["expectedVersion"]
	if value == nil {
		if _, exists := r.Header["If-Match"]; !exists {
			return 0, aiInvalid()
		}
		value = strings.ReplaceAll(r.Header.Get("If-Match"), "\"", "")
	}
	version, ok := value.(float64)
	if !ok {
		if text, textOK := value.(string); textOK {
			text = strings.TrimSpace(text)
			if text == "" {
				text = "0"
			}
			parsed, err := strconv.ParseFloat(text, 64)
			if err == nil {
				version, ok = parsed, true
			}
		}
	}
	if !ok || version < 0 || math.Trunc(version) != version || math.IsInf(version, 0) || math.IsNaN(version) {
		return 0, aiInvalid()
	}
	return version, nil
}
func (a *AI) Settings(patch bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		if !patch {
			raw, err := optional(ctx, a.auth.Store.DB, "ai_settings", "global")
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			var row map[string]any
			if raw != nil && json.Unmarshal(raw, &row) != nil {
				a.auth.fail(w, aiInvalid())
				return
			}
			result, err := a.public(row)
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			JSON(w, 200, result)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024*1024))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			a.auth.fail(w, aiInvalid())
			return
		}
		for field := range body {
			if !contains(aiFields, field) && field != "apiKey" && field != "expectedVersion" {
				a.auth.fail(w, aiInvalid())
				return
			}
		}
		expected, err := aiExpected(body, r)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		var result map[string]any
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, a.auth.Origin)
			if err != nil {
				return err
			}
			result, err = a.configure(ctx, conn, body, actor, expected)
			return err
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}

func (a *AI) configure(ctx context.Context, conn *sql.Conn, body, actor map[string]any, expected float64) (map[string]any, error) {
	raw, err := optional(ctx, conn, "ai_settings", "global")
	if err != nil {
		return nil, err
	}
	var previous map[string]any
	if raw != nil && json.Unmarshal(raw, &previous) != nil {
		return nil, aiInvalid()
	}
	version := float64(0)
	if previous != nil {
		version, _ = previous["version"].(float64)
	}
	if version != expected {
		return nil, &protocolError{"CONFLICT", "AI configuration changed. Read it again.", 409}
	}
	cfg, err := aiConfig(previous)
	if err != nil {
		return nil, err
	}
	next := aiConfigMap(cfg)
	for _, field := range aiFields {
		if value, exists := body[field]; exists {
			next[field] = value
		}
	}
	cfg, err = aiConfig(next)
	if err != nil {
		return nil, err
	}
	next = aiConfigMap(cfg)
	next["apiKey"] = previous["apiKey"]
	if value, exists := body["apiKey"]; exists {
		key, ok := value.(string)
		if !ok || len(utf16.Encode([]rune(key))) > 10000 {
			return nil, aiInvalid()
		}
		next["apiKey"], err = a.box.Seal(key, "ai_settings", "global", "apiKey")
		if err != nil {
			return nil, secretUnavailable()
		}
	}
	generation, _ := previous["dispatchGeneration"].(float64)
	next["dispatchGeneration"] = generation + 1
	var before map[string]any
	if previous != nil {
		oldCfg, err := aiConfig(previous)
		if err != nil {
			return nil, err
		}
		before = aiConfigMap(oldCfg)
		before["apiKeyConfigured"] = previous["apiKey"] != nil && previous["apiKey"] != ""
		next["id"], next["version"], next["createdAt"] = previous["id"], previous["version"], previous["createdAt"]
		err = update(ctx, conn, "ai_settings", next)
	} else {
		err = insert(ctx, conn, "ai_settings", "global", next)
	}
	if err != nil {
		return nil, err
	}
	after := aiConfigMap(cfg)
	after["apiKeyConfigured"] = next["apiKey"] != nil && next["apiKey"] != ""
	if err = apiAudit(ctx, conn, "ai.configured", "ai_settings", "global", actor, before, after); err != nil {
		return nil, err
	}
	return a.public(next)
}
