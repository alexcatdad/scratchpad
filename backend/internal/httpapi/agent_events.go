package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Agent events are hook events from coding agents, delivered in batches by the
// local capture service. They are evidence with their own retention, not
// captures: there is no audit row per event, no idempotency record beyond the
// event identity, and they are never exported.
const (
	agentEventBatchLimit = 200
	agentEventPageLimit  = 500
	agentEventPageSize   = 100
)

var (
	agentEventAgents = []string{"claude-code", "codex"}
	agentEventKinds  = []string{"session_start", "session_end", "prompt", "tool_failure", "permission_denied", "turn_end", "interrupt", "model_failure"}
)

func validationAgentEvents() error {
	return &protocolError{"VALIDATION_FAILED", "Invalid agent event request.", 400}
}

type agentEvent struct {
	id, sessionID, agent, kind, occurredAt string
	projectID, remote                      string
	data                                   string
}

// parseAgentEvent validates the fields the server indexes. Everything else in
// the event is stored as received.
func parseAgentEvent(raw json.RawMessage) (agentEvent, bool) {
	var fields map[string]any
	if DecodeJSONNumbers(raw, &fields) != nil || fields == nil {
		return agentEvent{}, false
	}
	text := func(name string, limit int) (string, bool) {
		value, ok := fields[name].(string)
		return value, ok && value != "" && len(value) <= limit
	}
	event := agentEvent{}
	var ok bool
	if event.id, ok = text("eventId", 128); !ok {
		return agentEvent{}, false
	}
	if event.sessionID, ok = text("sessionId", 512); !ok {
		return agentEvent{}, false
	}
	if event.agent, ok = text("agent", 64); !ok || !contains(agentEventAgents, event.agent) {
		return agentEvent{}, false
	}
	if event.kind, ok = text("kind", 64); !ok || !contains(agentEventKinds, event.kind) {
		return agentEvent{}, false
	}
	occurred, ok := text("occurredAt", 64)
	if !ok {
		return agentEvent{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		return agentEvent{}, false
	}
	event.occurredAt = timestamp(at)
	if rawProject, exists := fields["projectId"]; exists {
		if event.projectID, ok = rawProject.(string); !ok || event.projectID == "" {
			return agentEvent{}, false
		}
	}
	if git, isObject := fields["gitContext"].(map[string]any); isObject {
		event.remote, _ = git["remote"].(string)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return agentEvent{}, false
	}
	event.data = compact.String()
	return event, true
}

// AgentEvents stores a batch. It always answers 200 with counts once the batch
// itself is well formed, so the sender can drop what it sent: an event that is
// invalid, belongs to no known project or to a project that has not opted in
// will not become acceptable by being sent again.
func (s *SSH) AgentEvents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		matches := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
		if len(matches) != 2 {
			s.fail(w, &protocolError{"AUTH_INVALID", "Agent events require a machine credential.", 403})
			return
		}
		var body struct {
			Events []json.RawMessage `json:"events"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3*1024*1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || len(body.Events) == 0 || len(body.Events) > agentEventBatchLimit {
			s.fail(w, validationAgentEvents())
			return
		}
		events := make([]agentEvent, 0, len(body.Events))
		invalid := 0
		for _, raw := range body.Events {
			event, ok := parseAgentEvent(raw)
			if !ok {
				invalid++
				continue
			}
			events = append(events, event)
		}
		accepted, duplicate, unresolved, notEnabled := 0, 0, 0, 0
		err := s.Store.Write(ctx, func(conn *sql.Conn) error {
			accepted, duplicate, unresolved, notEnabled = 0, 0, 0, 0
			if _, err := apiActor(ctx, conn, r, s.Origin); err != nil {
				return err
			}
			session, err := entityMap(ctx, conn, "session", tokenID(matches[1]))
			if err != nil {
				return err
			}
			credentialID := stringValue(session, "credentialId")
			projects := agentEventProjects{conn: conn, byRemote: map[string]string{}, enabled: map[string]bool{}}
			received := timestamp(time.Now())
			for _, event := range events {
				projectID, err := projects.resolve(ctx, event)
				if err != nil {
					return err
				}
				if projectID == "" {
					unresolved++
					continue
				}
				enabled, err := projects.optedIn(ctx, projectID)
				if err != nil {
					return err
				}
				if !enabled {
					notEnabled++
					continue
				}
				// The identity is scoped to the credential, so one machine cannot
				// shadow another machine's event by reusing its identifier.
				sum := sha256.Sum256([]byte(credentialID + "\x00" + event.id))
				result, err := conn.ExecContext(ctx, `INSERT INTO agent_events(id,project_id,credential_id,session_id,agent,kind,occurred_at,received_at,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO NOTHING`,
					"evt_"+hex.EncodeToString(sum[:16]), projectID, credentialID, event.sessionID, event.agent, event.kind, event.occurredAt, received, event.data)
				if err != nil {
					return err
				}
				if rows, _ := result.RowsAffected(); rows == 1 {
					accepted++
				} else {
					duplicate++
				}
			}
			return nil
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"accepted": accepted, "duplicate": duplicate, "discarded": map[string]any{"invalid": invalid, "unresolved": unresolved, "notEnabled": notEnabled}})
	}
}

// agentEventProjects resolves and caches projects for one batch. It never
// creates a project: an event for a repository Scratchpad has not seen is
// dropped, because nobody has opted that project in.
type agentEventProjects struct {
	conn     *sql.Conn
	byRemote map[string]string
	enabled  map[string]bool
}

func (p *agentEventProjects) resolve(ctx context.Context, event agentEvent) (string, error) {
	if event.projectID != "" {
		return event.projectID, nil
	}
	if event.remote == "" {
		return "", nil
	}
	if id, known := p.byRemote[event.remote]; known {
		return id, nil
	}
	identity, err := normalizeRemote(event.remote)
	if err != nil {
		p.byRemote[event.remote] = ""
		return "", nil
	}
	var id string
	err = p.conn.QueryRowContext(ctx, `SELECT project_id FROM identities WHERE identity=$1`, identity).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	p.byRemote[event.remote] = id
	return id, nil
}

func (p *agentEventProjects) optedIn(ctx context.Context, projectID string) (bool, error) {
	if enabled, known := p.enabled[projectID]; known {
		return enabled, nil
	}
	project, err := entityMap(ctx, p.conn, "project", projectID)
	if errors.Is(err, sql.ErrNoRows) {
		p.enabled[projectID] = false
		return false, nil
	}
	if err != nil {
		return false, err
	}
	enabled := objectValue(project, "settings")["agentEvents"] == true
	p.enabled[projectID] = enabled
	return enabled, nil
}

func agentEventCursor(seq int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte("seq:" + strconv.FormatInt(seq, 10)))
}

func parseAgentEventCursor(cursor string) (int64, bool) {
	if cursor == "" {
		return 0, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(decoded), "seq:") {
		return 0, false
	}
	seq, err := strconv.ParseInt(strings.TrimPrefix(string(decoded), "seq:"), 10, 64)
	return seq, err == nil && seq >= 0
}

// AgentEventList pages events in the order they were stored. The cursor is a
// database-allocated sequence, so a reader that keeps the last cursor never
// misses an event that is committed later.
func (s *SSH) AgentEventList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		query := r.URL.Query()
		after, ok := parseAgentEventCursor(query.Get("after"))
		if !ok {
			s.fail(w, validationAgentEvents())
			return
		}
		limit := agentEventPageSize
		if raw := query.Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > agentEventPageLimit {
				s.fail(w, validationAgentEvents())
				return
			}
			limit = parsed
		}
		// One fixed statement: an empty filter value matches every row.
		rows, err := s.Store.DB.QueryContext(ctx, `SELECT seq,id,project_id,session_id,agent,kind,occurred_at,received_at,data FROM agent_events WHERE seq>$1 AND ($2='' OR project_id=$2) AND ($3='' OR session_id=$3) ORDER BY seq LIMIT $4`,
			after, query.Get("projectId"), query.Get("sessionId"), limit+1)
		if err != nil {
			s.fail(w, err)
			return
		}
		defer rows.Close()
		events := []map[string]any{}
		cursor, hasMore := query.Get("after"), false
		for rows.Next() {
			var seq int64
			var id, projectID, sessionID, agent, kind, occurredAt, receivedAt string
			var data []byte
			if err = rows.Scan(&seq, &id, &projectID, &sessionID, &agent, &kind, &occurredAt, &receivedAt, &data); err != nil {
				s.fail(w, err)
				return
			}
			if len(events) == limit {
				hasMore = true
				break
			}
			events = append(events, map[string]any{"id": id, "projectId": projectID, "sessionId": sessionID, "agent": agent, "kind": kind, "occurredAt": occurredAt, "receivedAt": receivedAt, "event": json.RawMessage(data)})
			cursor = agentEventCursor(seq)
		}
		if err = rows.Err(); err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"events": events, "cursor": cursor, "hasMore": hasMore})
	}
}

// RunAgentEventRetention deletes events older than the retention period, once
// at startup and then hourly. It has its own loop because the AI worker's tick
// returns early when no provider is configured.
func (s *SSH) RunAgentEventRetention(ctx context.Context, retention time.Duration) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		pruneContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = s.Store.Write(pruneContext, func(conn *sql.Conn) error {
			_, err := conn.ExecContext(pruneContext, `DELETE FROM agent_events WHERE received_at<$1`, timestamp(time.Now().Add(-retention)))
			return err
		})
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
