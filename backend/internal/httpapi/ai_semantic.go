package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

func aiCosine(left, right []float64) float64 {
	// Scale first to avoid overflow from valid, very large provider values.
	lscale, rscale := 0.0, 0.0
	for _, v := range left {
		lscale = math.Max(lscale, math.Abs(v))
	}
	for _, v := range right {
		rscale = math.Max(rscale, math.Abs(v))
	}
	if len(left) != len(right) || lscale == 0 || rscale == 0 {
		return -1
	}
	dot, ll, rr := 0.0, 0.0, 0.0
	for i, v := range left {
		l, r := v/lscale, right[i]/rscale
		dot += l * r
		ll += l * l
		rr += r * r
	}
	return dot / math.Sqrt(ll*rr)
}
func (a *AI) SemanticSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			a.auth.fail(w, aiInvalid())
			return
		}
		scope, err := parseJobScope(body)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		query, ok := body["query"].(string)
		query = strings.TrimSpace(query)
		limit := 20
		if value, exists := body["limit"]; exists {
			n, ok := value.(float64)
			if !ok || n < 1 || n > 100 || n != math.Trunc(n) {
				a.auth.fail(w, aiInvalid())
				return
			}
			limit = int(n)
		}
		if !ok || query == "" || len(utf16.Encode([]rune(query))) > 4000 {
			a.auth.fail(w, aiInvalid())
			return
		}
		if _, err = apiActor(r.Context(), a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		snapshot, err := a.provider(r.Context())
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(snapshot.config.RequestTimeoutSeconds+10)*time.Second)
		defer cancel()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Duration(snapshot.config.RequestTimeoutSeconds+10) * time.Second))
		signature, err := embeddingFingerprint(snapshot.config)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		var eligible []map[string]any
		sources := map[string]map[string]any{}
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			if err := a.providerCurrent(ctx, conn, r, snapshot); err != nil {
				return err
			}
			_, err := allowedJobs(ctx, conn, scope)
			return err
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		// Corpus loading and hashing are read-only work. Keep a consistent
		// snapshot without holding the serialized writer admission boundary.
		err = func() error {
			conn, err := a.auth.Store.DB.Conn(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()
			begin := "BEGIN"
			if a.auth.Store.Engine == "postgres" {
				begin = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"
			}
			if _, err := conn.ExecContext(ctx, begin); err != nil {
				return err
			}
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			records, err := a.aiRecords(ctx, conn, scope)
			if err != nil {
				return err
			}
			for _, record := range records {
				sources[stringValue(record, "id")] = record
			}
			embeddings, err := a.aiScopedEntities(ctx, conn, scope, "ai_embedding")
			if err != nil {
				return err
			}
			for _, item := range embeddings {
				record := sources[stringValue(item, "recordId")]
				if record == nil {
					continue
				}
				hash, err := aiHash(record)
				if err != nil {
					return err
				}
				if item["fingerprint"] == signature && item["model"] == snapshot.config.EmbeddingModel && item["dimensions"] == float64(snapshot.config.EmbeddingDimensions) && item["contentHash"] == hash {
					eligible = append(eligible, item)
				}
			}
			return nil
		}()
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		// The read snapshot has ended before rechecking current admission or
		// making provider requests, including the empty-index response path.
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			if err := a.providerCurrent(ctx, conn, r, snapshot); err != nil {
				return err
			}
			_, err := allowedJobs(ctx, conn, scope)
			return err
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		if len(eligible) == 0 {
			JSON(w, 200, map[string]any{"results": []any{}, "model": snapshot.config.EmbeddingModel, "indexRequired": true})
			return
		}
		vectors, err := a.embedInputs(ctx, r, snapshot, []string{query}, func(conn *sql.Conn) error { _, err := allowedJobs(ctx, conn, scope); return err })
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		results := []map[string]any{}
		for _, item := range eligible {
			raw, _ := json.Marshal(item["vector"])
			var vector []float64
			if json.Unmarshal(raw, &vector) != nil || len(vector) != snapshot.config.EmbeddingDimensions {
				continue
			}
			score := aiCosine(vectors[0], vector)
			if !math.IsNaN(score) && !math.IsInf(score, 0) && score >= snapshot.config.SimilarityThreshold {
				results = append(results, map[string]any{"record": sources[stringValue(item, "recordId")], "score": score})
			}
		}
		sort.SliceStable(results, func(i, j int) bool { return results[i]["score"].(float64) > results[j]["score"].(float64) })
		if len(results) > limit {
			results = results[:limit]
		}
		// Revalidate after ranking, in the same admission boundary as other AI calls.
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			if err := a.providerCurrent(ctx, conn, r, snapshot); err != nil {
				return err
			}
			allowed, err := allowedJobs(ctx, conn, scope)
			if err != nil {
				return err
			}
			for _, result := range results {
				record := result["record"].(map[string]any)
				if !contains(allowed, stringValue(record, "projectId")) {
					return &protocolError{"AI_NOT_ALLOWED", "Current consent does not permit these semantic results.", 403}
				}
				current, err := entityMap(ctx, conn, "record", stringValue(record, "id"))
				if err != nil {
					return err
				}
				before, err := aiHash(record)
				if err != nil {
					return err
				}
				after, err := aiHash(current)
				if err != nil {
					return err
				}
				if before != after {
					return aiChanged()
				}
			}
			return nil
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"results": results, "model": snapshot.config.EmbeddingModel})
	}
}
