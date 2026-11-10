package localcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/secretguard"
)

// OpAgentEvent accepts one normalized coding-agent event. It commits locally
// and returns at once: unlike capture it never waits for delivery, because the
// caller is an agent hook that must not slow the agent down.
const OpAgentEvent Operation = "agent_event"

const (
	// AgentEventMaxBytes bounds one event. Normalization already truncates
	// text and tool input, so a larger event is malformed.
	AgentEventMaxBytes = 256 << 10
	// AgentEventMaxRows and AgentEventMaxAge bound the local backlog while the
	// server is unreachable. Events are evidence with their own retention, not
	// captures: the oldest are dropped first.
	AgentEventMaxRows = 20000
	AgentEventMaxAge  = 7 * 24 * time.Hour

	agentEventAAD = "agent-event"

	// One delivery carries at most this many events and bytes, inside the
	// server's limits of 200 events and 3 MiB per request.
	agentEventBatchSize     = 100
	agentEventBatchBytes    = 2 << 20
	agentEventIdleDelay     = 2 * time.Second
	agentEventDeliveryLimit = 30 * time.Second
)

// AgentEventAdapter delivers a batch to the central server. It is optional,
// like OnlineAdapter: without it events stay queued within the local bounds.
type AgentEventAdapter interface {
	DeliverAgentEvents(context.Context, Binding, []json.RawMessage) error
}

// agentEventDelivery is touched only by the worker goroutine.
type agentEventDelivery struct {
	next     time.Time
	failures int
}

type AgentEventRequest struct {
	Event json.RawMessage `json:"event"`
}

type AgentEventResponse struct {
	Accepted bool `json:"accepted"`
}

// AgentEventService is asserted by the IPC server, as OnlineService is, so
// the Service interface and its implementations stay unchanged.
type AgentEventService interface {
	AgentEvent(context.Context, AgentEventRequest) (AgentEventResponse, error)
}

func (c *IPCClient) AgentEvent(ctx context.Context, req AgentEventRequest) (AgentEventResponse, error) {
	var out AgentEventResponse
	err := c.call(ctx, OpAgentEvent, req, &out)
	return out, err
}

// AgentEvent redacts and durably queues one event. Redaction happens before
// the write, as for captures, so the queue never holds an unredacted event.
func (r *Runtime) AgentEvent(_ context.Context, req AgentEventRequest) (AgentEventResponse, error) {
	event := bytes.TrimSpace(req.Event)
	if len(event) == 0 || len(event) > AgentEventMaxBytes || event[0] != '{' || !json.Valid(event) {
		return AgentEventResponse{}, ErrConflictSentinel
	}
	r.intakeMu.Lock()
	defer r.intakeMu.Unlock()
	r.mu.Lock()
	stopped, quiescing, available := r.stopped, r.quiescing, r.storageAvailable
	r.mu.Unlock()
	if stopped {
		return AgentEventResponse{}, ErrServiceStoppedSentinel
	}
	if quiescing {
		return AgentEventResponse{}, ErrServiceQuiescingSentinel
	}
	if !available {
		return AgentEventResponse{}, ErrStorageFailedSentinel
	}
	var scan secretguard.Scan
	redacted, err := scan.JSON(event)
	if err != nil {
		return AgentEventResponse{}, ErrInternalSentinel
	}
	if err = r.store.acceptAgentEvent(redacted, time.Now(), AgentEventMaxRows); err != nil {
		r.markStorageFailure(err)
		return AgentEventResponse{}, err
	}
	return AgentEventResponse{Accepted: true}, nil
}

type queuedAgentEvent struct {
	id    int64
	event json.RawMessage
}

func (s *Store) acceptAgentEvent(event []byte, now time.Time, maxRows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, err := encrypt(s.aead, agentEventAAD, event)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec("INSERT INTO agent_events(created_ns,envelope) VALUES(?,?)", now.UnixNano(), blob); err != nil {
		return ErrStorageFailedSentinel
	}
	// The row cap is enforced on every insert through the primary key, so a
	// long outage cannot grow the queue without bound.
	if _, err = s.db.Exec("DELETE FROM agent_events WHERE id <= (SELECT MAX(id) FROM agent_events) - ?", maxRows); err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

func (s *Store) pruneAgentEvents(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("DELETE FROM agent_events WHERE created_ns < ?", now.Add(-AgentEventMaxAge).UnixNano()); err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

// agentEventBatch returns the oldest queued events in insertion order.
func (s *Store) agentEventBatch(limit int) ([]queuedAgentEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT id,envelope FROM agent_events ORDER BY id LIMIT ?", limit)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	defer rows.Close()
	var batch []queuedAgentEvent
	for rows.Next() {
		var id int64
		var blob []byte
		if rows.Scan(&id, &blob) != nil {
			return nil, ErrStorageFailedSentinel
		}
		plain, decryptErr := decrypt(s.aead, agentEventAAD, blob)
		if decryptErr != nil {
			return nil, ErrKeyUnavailableSentinel
		}
		batch = append(batch, queuedAgentEvent{id: id, event: plain})
	}
	if rows.Err() != nil {
		return nil, ErrStorageFailedSentinel
	}
	return batch, nil
}

func (s *Store) deleteAgentEventsThrough(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("DELETE FROM agent_events WHERE id <= ?", id); err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

func (s *Store) agentEventCount() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_events").Scan(&count); err != nil {
		return 0, ErrStorageFailedSentinel
	}
	return count, nil
}

// deliverAgentEvents sends one batch when one is due. Events are evidence, not
// captures: delivery reuses the cached server session instead of a fresh SSH
// proof per item, a failure only backs off, and nothing here changes the
// receipt counts, the connection state or the last-delivery summary.
func (r *Runtime) deliverAgentEvents(ctx context.Context) {
	now := time.Now()
	if now.Before(r.agentEvents.next) {
		return
	}
	adapter, ok := r.adapter.(AgentEventAdapter)
	if !ok {
		r.agentEvents.next = now.Add(time.Hour)
		return
	}
	if !r.workMu.TryLock() {
		return
	}
	defer r.workMu.Unlock()
	r.mu.Lock()
	blocked := r.stopped || r.quiescing || r.paused || !r.storageAvailable
	r.mu.Unlock()
	if blocked {
		return
	}
	batch, err := r.store.agentEventBatch(agentEventBatchSize)
	if err != nil {
		r.markStorageFailure(err)
		return
	}
	if len(batch) == 0 {
		r.agentEvents.next = now.Add(agentEventIdleDelay)
		return
	}
	events := make([]json.RawMessage, 0, len(batch))
	size := 0
	last := batch[0].id
	for _, queued := range batch {
		if len(events) > 0 && size+len(queued.event) > agentEventBatchBytes {
			break
		}
		events = append(events, queued.event)
		size += len(queued.event)
		last = queued.id
	}
	r.configMu.RLock()
	config, err := r.store.configuration()
	r.configMu.RUnlock()
	if err != nil || validateConfiguration(config) != nil {
		r.agentEvents.next = now.Add(agentEventIdleDelay)
		return
	}
	deliver, cancel := context.WithTimeout(ctx, agentEventDeliveryLimit)
	defer cancel()
	err = adapter.DeliverAgentEvents(deliver, Binding{ServerURL: config.ServerURL, CredentialFingerprint: config.CredentialFingerprint, PublicKeyPath: config.PublicKeyPath, SigningKeyPath: config.SigningKeyPath, SSHAgentSocket: config.SSHAgentSocket}, events)
	if err != nil {
		r.agentEvents.failures++
		delay := r.retryMin << min(r.agentEvents.failures, 10)
		if delay > r.timeRetryMax || delay <= 0 {
			delay = r.timeRetryMax
		}
		r.agentEvents.next = time.Now().Add(delay)
		return
	}
	r.agentEvents.failures = 0
	r.agentEvents.next = time.Time{}
	if err = r.store.deleteAgentEventsThrough(last); err != nil {
		r.markStorageFailure(err)
	}
}
