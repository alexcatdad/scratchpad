package localcapture

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ServiceOptions struct {
	Store            *Store
	Adapter          DeliveryAdapter
	ExplicitStart    bool
	ExitAfterQuiesce bool
	DeferWorker      bool
	RetryMin         time.Duration
	RetryMax         time.Duration
	HealthInterval   time.Duration
	PruneInterval    time.Duration
}

const (
	fastResultCacheTTL     = 5 * time.Second
	fastResultCacheEntries = 32
	fastResultCacheBytes   = 2 << 20
	fastWaiterCount        = 64
	fastWaiterBytes        = 8 << 20
)

type cachedDeliveryResult struct {
	result    DeliveryResult
	storedAt  time.Time
	byteCount int
}

type deliveryWaiter struct {
	requestID       string
	done            chan struct{}
	refs            int
	ready           bool
	resultAvailable bool
	result          DeliveryResult
	byteCount       int
}

// Runtime owns intake, durable settings and one serialized background worker.
type Runtime struct {
	store                   *Store
	adapter                 DeliveryAdapter
	mu                      sync.Mutex
	intakeMu                sync.Mutex
	workMu                  sync.Mutex
	configMu                sync.RWMutex
	configGeneration        uint64
	healthInterval          time.Duration
	pruneInterval           time.Duration
	lifecycle               Lifecycle
	paused                  bool
	stopped                 bool
	quiescing               bool
	connection              ConnectionStatus
	lastDelivery            *DeliverySummary
	wake                    chan struct{}
	probeWake               chan struct{}
	stop                    chan struct{}
	workerDone              chan struct{}
	stopOnce                sync.Once
	retryMin, timeRetryMax  time.Duration
	resultsMu               sync.Mutex
	waitersMu               sync.Mutex
	results                 map[string]cachedDeliveryResult
	resultsBytes            int
	waiters                 map[string]*deliveryWaiter
	waiterBytes             int
	resultPublishHook       func() // deterministic seam for receipt/cache publication tests
	resultWaiterPublishHook func() // deterministic seam for cache-pressure/waiter tests
	resultReadHook          func() // deterministic seam for receipt/cache publication tests
	exitAfterQuiesce        bool
	storageAvailable        bool
	workerStarted           bool
	closed                  bool
	workerAdmissionHook     func() // deterministic scheduling seam for quiesce tests
	intakeAdmissionHook     func() // deterministic seam around durable intake commit
}

func NewRuntime(ctx context.Context, options ServiceOptions) (*Runtime, error) {
	if options.Store == nil {
		return nil, ErrStorageFailedSentinel
	}
	if options.RetryMin <= 0 {
		options.RetryMin = 2 * time.Second
	}
	if options.RetryMax <= 0 {
		options.RetryMax = time.Hour
	}
	if options.HealthInterval <= 0 {
		options.HealthInterval = 30 * time.Second
	}
	if options.PruneInterval <= 0 {
		options.PruneInterval = time.Hour
	}
	r := &Runtime{store: options.Store, adapter: options.Adapter, wake: make(chan struct{}, 1), probeWake: make(chan struct{}, 1), stop: make(chan struct{}), workerDone: make(chan struct{}), retryMin: options.RetryMin, timeRetryMax: options.RetryMax, healthInterval: options.HealthInterval, pruneInterval: options.PruneInterval, results: map[string]cachedDeliveryResult{}, waiters: map[string]*deliveryWaiter{}, exitAfterQuiesce: options.ExitAfterQuiesce, storageAvailable: true}
	settings, err := r.store.settings()
	if err != nil {
		return nil, err
	}
	r.paused = settings.DeliveryPaused
	r.stopped = settings.ServiceStopped
	r.connection = settings.Connection
	r.lastDelivery = settings.LastDelivery
	if options.ExplicitStart && r.stopped {
		r.stopped = false
		if err = r.persistSettings(); err != nil {
			return nil, err
		}
	}
	if r.stopped {
		r.lifecycle = LifecycleStopped
	} else {
		r.lifecycle = LifecycleRunning
	}
	if err = r.store.prune(time.Now()); err != nil {
		return nil, err
	}
	if options.DeferWorker {
		close(r.workerDone)
	} else {
		r.workerStarted = true
		go r.worker(ctx)
	}
	return r, nil
}

func (r *Runtime) Close() {
	r.mu.Lock()
	r.closed = true
	started := r.workerStarted
	r.mu.Unlock()
	r.stopOnce.Do(func() { close(r.stop) })
	if started {
		<-r.workerDone
	}
	_ = r.store.Close()
}

// StartWorker is used by the production command only after it has acquired
// the exclusive Unix socket. Other service instances never start delivery.
func (r *Runtime) StartWorker(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.stopped || r.quiescing {
		return ErrServiceStoppedSentinel
	}
	if r.workerStarted {
		return nil
	}
	r.workerDone = make(chan struct{})
	r.workerStarted = true
	go r.worker(ctx)
	return nil
}

func (r *Runtime) worker(ctx context.Context) {
	defer close(r.workerDone)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	healthTimer := time.NewTimer(r.healthInterval)
	defer healthTimer.Stop()
	pruneTimer := time.NewTimer(r.pruneInterval)
	defer pruneTimer.Stop()
	r.probeConnection(ctx)
	resetTimer(healthTimer, r.healthInterval)
	for {
		probe := false
		prune := false
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-r.probeWake:
			probe = true
		case <-healthTimer.C:
			probe = true
		case <-pruneTimer.C:
			prune = true
		case <-r.wake:
		case <-ticker.C:
		}
		if probe {
			r.probeConnection(ctx)
			resetTimer(healthTimer, r.healthInterval)
		}
		if prune {
			r.pruneReceipts()
			resetTimer(pruneTimer, r.pruneInterval)
		}
		for i := 0; i < 8; i++ {
			progressed, err := r.processOne(ctx)
			if err != nil {
				r.markStorageFailure(err)
				break
			}
			if !progressed {
				break
			}
		}
	}
}

func (r *Runtime) pruneReceipts() {
	if !r.workMu.TryLock() {
		return
	}
	defer r.workMu.Unlock()
	r.mu.Lock()
	blocked := r.stopped || r.quiescing || !r.storageAvailable
	r.mu.Unlock()
	if blocked {
		return
	}
	if err := r.store.prune(time.Now()); err != nil {
		r.markStorageFailure(err)
	}
}

func resetTimer(timer *time.Timer, interval time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(interval)
}

// probeConnection refreshes authenticated remote reachability without reading
// projects or creating a capture. It shares the work barrier with delivery and
// rechecks lifecycle/configuration after admission so quiesce and reconfigure
// cannot publish a stale observation.
func (r *Runtime) probeConnection(ctx context.Context) {
	if r.adapter == nil {
		return
	}
	r.mu.Lock()
	blocked := r.stopped || r.quiescing || !r.storageAvailable
	r.mu.Unlock()
	if blocked {
		return
	}
	r.workMu.Lock()
	defer r.workMu.Unlock()
	r.mu.Lock()
	blocked = r.stopped || r.quiescing || !r.storageAvailable
	r.mu.Unlock()
	if blocked {
		return
	}
	r.configMu.RLock()
	c, err := r.store.configuration()
	generation := r.configGeneration
	r.configMu.RUnlock()
	if err != nil {
		r.markStorageFailure(err)
		return
	}
	if validateConfiguration(c) != nil {
		return
	}
	b := Binding{ServerURL: c.ServerURL, CredentialFingerprint: c.CredentialFingerprint, PublicKeyPath: c.PublicKeyPath, SigningKeyPath: c.SigningKeyPath}
	identity, err := r.adapter.Authenticate(ctx, b)
	if err != nil {
		code, _ := classifyFailure(err, DeliveryConnectivity, true)
		r.noteConnectionFor(generation, code)
		return
	}
	if canonicalURL(identity.ServerURL) != canonicalURL(c.ServerURL) || identity.CredentialID == "" || !hasCapability(identity.CaptureCapabilities, CapabilityClientSavedAtV1) {
		r.noteConnectionFor(generation, DeliveryServerIncompatible)
		return
	}
	r.noteConnectionFor(generation, "")
}

func (r *Runtime) processOne(ctx context.Context) (bool, error) {
	r.mu.Lock()
	blocked := r.stopped || r.quiescing || r.paused
	blocked = blocked || !r.storageAvailable
	r.mu.Unlock()
	if blocked || r.adapter == nil {
		return false, nil
	}
	if r.workerAdmissionHook != nil {
		r.workerAdmissionHook()
	}
	r.workMu.Lock()
	defer r.workMu.Unlock()
	// Quiesce may set its barrier after the initial check but before this lock.
	r.mu.Lock()
	blocked = r.stopped || r.quiescing || r.paused || !r.storageAvailable
	r.mu.Unlock()
	if blocked {
		return false, nil
	}
	due, err := r.store.due(time.Now(), 1)
	if err != nil {
		return false, err
	}
	if len(due) == 0 {
		return false, nil
	}
	item := due[0]
	env := item.env
	req := *env.Request
	config, err := r.store.configuration()
	if err != nil {
		return false, err
	}
	if !sameBinding(req.Binding, config) {
		return true, r.fail(env, item.id, item.attempts, DeliveryCredentialChanged, false, ReceiptBlocked)
	}
	adapterBinding := req.Binding
	adapterBinding.PublicKeyPath = config.PublicKeyPath
	adapterBinding.SigningKeyPath = config.SigningKeyPath
	identity, authErr := r.adapter.Authenticate(ctx, adapterBinding)
	if authErr != nil {
		code, retryable := classifyFailure(authErr, DeliveryConnectivity, true)
		r.noteConnection(code)
		return true, r.fail(env, item.id, item.attempts, code, retryable, ReceiptBlocked)
	}
	if canonicalURL(identity.ServerURL) != canonicalURL(req.Binding.ServerURL) || identity.CredentialID == "" {
		r.noteConnection(DeliveryServerIncompatible)
		return true, r.fail(env, item.id, item.attempts, DeliveryServerIncompatible, false, ReceiptBlocked)
	}
	if !hasCapability(identity.CaptureCapabilities, CapabilityClientSavedAtV1) {
		r.noteConnection(DeliveryServerIncompatible)
		return true, r.fail(env, item.id, item.attempts, DeliveryServerIncompatible, false, ReceiptBlocked)
	}
	if env.CredentialID != "" && env.CredentialID != identity.CredentialID {
		r.noteConnection(DeliveryCredentialChanged)
		return true, r.fail(env, item.id, item.attempts, DeliveryCredentialChanged, false, ReceiptBlocked)
	}
	if env.CredentialID == "" {
		env.CredentialID = identity.CredentialID
		env.Request = &req
		if err = r.store.updateEnvelope(item.id, env, time.Now(), item.attempts); err != nil {
			return false, err
		}
	}
	r.noteConnection("")
	if req.Scope.ProjectID == "" {
		if len(req.GitContext) == 0 {
			return true, r.fail(env, item.id, item.attempts, DeliveryProjectAmbiguous, false, ReceiptNeedsSelection)
		}
		projectID, resolveErr := r.adapter.ResolveProject(ctx, adapterBinding, identity.CredentialID, req.GitContext)
		if resolveErr != nil {
			code, retryable := classifyFailure(resolveErr, DeliveryProjectAmbiguous, false)
			state := ReceiptNeedsSelection
			if code != DeliveryProjectAmbiguous {
				state = ReceiptBlocked
			}
			return true, r.fail(env, item.id, item.attempts, code, retryable, state)
		}
		if strings.TrimSpace(projectID) == "" {
			return true, r.fail(env, item.id, item.attempts, DeliveryProjectAmbiguous, false, ReceiptNeedsSelection)
		}
		req.Scope.ProjectID = projectID
		env.ProjectResolved = true
		env.Request = &req
		env.Receipt.Content = &ReceiptContent{RequestID: req.RequestID, Record: req.Record, Scope: req.Scope, GitContext: req.GitContext, ClientSavedAt: req.ClientSavedAt}
	}
	// Persist the delivery identity and uncertainty marker before the first
	// request that could commit centrally. A restart can therefore only retry
	// with the same credential, request identity and fully resolved project.
	env.Request = &req
	env.DeliveryAttempted = true
	if err = r.store.updateEnvelope(item.id, env, time.Now(), item.attempts); err != nil {
		return false, err
	}
	result, deliverErr := r.adapter.Deliver(ctx, DeliveryItem{ReceiptID: env.Receipt.ReceiptID, RequestID: req.RequestID, AcceptedAt: env.Receipt.AcceptedAt, ClientSavedAt: req.ClientSavedAt, Record: req.Record, Scope: req.Scope, GitContext: req.GitContext, Binding: adapterBinding, CredentialID: identity.CredentialID, Mirror: req.Mirror})
	if deliverErr != nil {
		code, retryable := classifyFailure(deliverErr, DeliveryConnectivity, true)
		r.noteConnection(code)
		state := ReceiptBlocked
		if code == DeliveryServerRejected {
			state = ReceiptRejected
		}
		if code == DeliveryProjectAmbiguous {
			state = ReceiptNeedsSelection
		}
		return true, r.fail(env, item.id, item.attempts, code, retryable, state)
	}
	if result.RecordID == "" || result.ProjectID == "" || result.ServerRecordedAt == "" {
		r.noteConnection(DeliveryServerIncompatible)
		return true, r.fail(env, item.id, item.attempts, DeliveryServerIncompatible, false, ReceiptBlocked)
	}
	env.Receipt.State = ReceiptDelivered
	env.Receipt.CentralRecordID = result.RecordID
	env.Receipt.CentralProjectID = result.ProjectID
	env.Receipt.ServerRecordedAt = result.ServerRecordedAt
	env.Receipt.Content = nil
	env.Receipt.DeliveryCode = ""
	env.Receipt.NextAttemptAt = ""
	env.Request = nil
	env.TombstoneUntil = time.Now().Add(ReceiptRetention).UTC().Format(time.RFC3339Nano)
	r.resultsMu.Lock()
	if err = r.store.updateEnvelope(item.id, env, time.Time{}, item.attempts+1); err != nil {
		r.resultsMu.Unlock()
		return false, err
	}
	if r.resultPublishHook != nil {
		r.resultPublishHook()
	}
	r.cacheDeliveryResultLocked(env.Receipt.ReceiptID, result, time.Now())
	if r.resultWaiterPublishHook != nil {
		r.resultWaiterPublishHook()
	}
	r.publishDeliveryWaiterLocked(req.RequestID, env.Receipt, result)
	r.resultsMu.Unlock()
	r.noteDelivery(DeliveryCode(""), false)
	return true, nil
}

func deliveryResultBytes(result DeliveryResult) int {
	return len(result.CentralRecord) + len(result.CentralProject) + len(result.Mirror)
}

func cloneDeliveryResult(result DeliveryResult) DeliveryResult {
	result.CentralRecord = append(json.RawMessage(nil), result.CentralRecord...)
	result.CentralProject = append(json.RawMessage(nil), result.CentralProject...)
	result.Mirror = append(json.RawMessage(nil), result.Mirror...)
	return result
}

func (r *Runtime) removeCachedDeliveryResultLocked(id string) {
	if cached, ok := r.results[id]; ok {
		delete(r.results, id)
		r.resultsBytes -= cached.byteCount
	}
}

func (r *Runtime) cacheDeliveryResultLocked(id string, result DeliveryResult, now time.Time) {
	for existingID, cached := range r.results {
		if now.Sub(cached.storedAt) >= fastResultCacheTTL {
			r.removeCachedDeliveryResultLocked(existingID)
		}
	}
	byteCount := deliveryResultBytes(result)
	if byteCount > fastResultCacheBytes {
		return
	}
	r.removeCachedDeliveryResultLocked(id)
	for len(r.results) >= fastResultCacheEntries || r.resultsBytes+byteCount > fastResultCacheBytes {
		oldestID := ""
		var oldest time.Time
		for candidateID, cached := range r.results {
			if oldestID == "" || cached.storedAt.Before(oldest) {
				oldestID = candidateID
				oldest = cached.storedAt
			}
		}
		if oldestID == "" {
			break
		}
		r.removeCachedDeliveryResultLocked(oldestID)
	}
	r.results[id] = cachedDeliveryResult{result: cloneDeliveryResult(result), storedAt: now, byteCount: byteCount}
	r.resultsBytes += byteCount
}

// A waiter enriches only a currently active capture call. Durable receipts
// remain authoritative, while bounded waiters preserve large live responses
// through cache pressure without turning delivered payloads into an archive.
func (r *Runtime) registerDeliveryWaiter(requestID string) *deliveryWaiter {
	r.waitersMu.Lock()
	defer r.waitersMu.Unlock()
	if waiter := r.waiters[requestID]; waiter != nil {
		waiter.refs++
		return waiter
	}
	if len(r.waiters) >= fastWaiterCount {
		return nil
	}
	waiter := &deliveryWaiter{requestID: requestID, done: make(chan struct{}), refs: 1}
	r.waiters[requestID] = waiter
	return waiter
}

func (r *Runtime) releaseDeliveryWaiter(waiter *deliveryWaiter) {
	if waiter == nil {
		return
	}
	r.waitersMu.Lock()
	defer r.waitersMu.Unlock()
	waiter.refs--
	if waiter.refs > 0 || r.waiters[waiter.requestID] != waiter {
		return
	}
	delete(r.waiters, waiter.requestID)
	if waiter.resultAvailable {
		r.waiterBytes -= waiter.byteCount
		waiter.result = DeliveryResult{}
		waiter.resultAvailable = false
	}
}

func captureResponseFits(receipt Receipt, result DeliveryResult) bool {
	outcome := CaptureOutcome{Outcome: OutcomeDelivered, Receipt: receipt, CentralRecord: result.CentralRecord, CentralProject: result.CentralProject, Mirror: result.Mirror}
	encoded, err := json.Marshal(outcome)
	if err != nil {
		return false
	}
	response, err := json.Marshal(Response{Version: ProtocolVersion, ID: strings.Repeat("0", 256), Result: encoded})
	return err == nil && len(response) <= MaxFrameBytes
}

func (r *Runtime) publishDeliveryWaiterLocked(requestID string, receipt Receipt, result DeliveryResult) {
	r.waitersMu.Lock()
	defer r.waitersMu.Unlock()
	waiter := r.waiters[requestID]
	if waiter == nil || waiter.ready {
		return
	}
	waiter.ready = true
	byteCount := deliveryResultBytes(result)
	if captureResponseFits(receipt, result) && byteCount <= fastWaiterBytes && r.waiterBytes+byteCount <= fastWaiterBytes {
		waiter.result = cloneDeliveryResult(result)
		waiter.byteCount = byteCount
		waiter.resultAvailable = true
		r.waiterBytes += byteCount
	}
	close(waiter.done)
}

func (r *Runtime) deliveryResult(id string) (DeliveryResult, bool) {
	if r.resultReadHook != nil {
		r.resultReadHook()
	}
	r.resultsMu.Lock()
	defer r.resultsMu.Unlock()
	cached, ok := r.results[id]
	if !ok {
		return DeliveryResult{}, false
	}
	if time.Since(cached.storedAt) >= fastResultCacheTTL {
		r.removeCachedDeliveryResultLocked(id)
		return DeliveryResult{}, false
	}
	return cloneDeliveryResult(cached.result), true
}

func (r *Runtime) fail(env storedEnvelope, id string, attempts int, code DeliveryCode, retry bool, state ReceiptState) error {
	now := time.Now().UTC()
	env.Receipt.State = state
	env.Receipt.DeliveryCode = code
	env.Receipt.Content = nil
	if env.Request != nil {
		env.Receipt.Content = &ReceiptContent{RequestID: env.Request.RequestID, Record: env.Request.Record, Scope: env.Request.Scope, GitContext: env.Request.GitContext, ClientSavedAt: env.Request.ClientSavedAt}
	}
	var next time.Time
	retry = retry || recoverableDeliveryCode(code)
	if retry {
		delay := r.retryMin * time.Duration(math.Pow(2, float64(min(attempts, 10))))
		if delay > r.timeRetryMax {
			delay = r.timeRetryMax
		}
		next = now.Add(delay)
		env.Receipt.NextAttemptAt = next.Format(time.RFC3339Nano)
	} else {
		next = time.Now().Add(365 * 24 * time.Hour)
		env.Receipt.NextAttemptAt = ""
	}
	if err := r.store.updateEnvelope(id, env, next, attempts+1); err != nil {
		return err
	}
	r.noteDelivery(code, retry)
	return nil
}

func recoverableDeliveryCode(code DeliveryCode) bool {
	switch code {
	case DeliveryConnectivity, DeliveryAuthentication, DeliveryCredentialChanged, DeliveryServerIncompatible:
		return true
	default:
		return false
	}
}

func (r *Runtime) markStorageFailure(err error) {
	if !errors.Is(err, ErrStorageFailedSentinel) && !errors.Is(err, ErrKeyUnavailableSentinel) {
		return
	}
	r.mu.Lock()
	r.storageAvailable = false
	r.mu.Unlock()
}

func classifyFailure(err error, fallback DeliveryCode, retryable bool) (DeliveryCode, bool) {
	var df *DeliveryFailure
	if errors.As(err, &df) {
		if df.Code != "" {
			return df.Code, df.Retryable
		}
	}
	return fallback, retryable
}

func hasCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}
func sameBinding(b Binding, c ServiceConfiguration) bool {
	return canonicalURL(b.ServerURL) == canonicalURL(c.ServerURL) && b.CredentialFingerprint == c.CredentialFingerprint
}

func (r *Runtime) noteConnection(code DeliveryCode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := ConnectionConnected
	switch code {
	case DeliveryConnectivity:
		state = ConnectionConnectivity
	case DeliveryAuthentication:
		state = ConnectionAuthentication
	case DeliveryCredentialChanged:
		state = ConnectionCredentialChanged
	case DeliveryServerIncompatible:
		state = ConnectionServerIncompatible
	case DeliveryServerRejected, DeliveryConflict:
		state = ConnectionUnknown
	}
	r.connection = ConnectionStatus{State: state, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := r.persistSettingsLocked(); err != nil {
		r.storageAvailable = false
	}
}

// noteConnectionFor publishes an observation only while the configuration
// generation it observed is still current. A slow verification for a former
// server or signer must not overwrite the status of the newly selected one.
func (r *Runtime) noteConnectionFor(generation uint64, code DeliveryCode) bool {
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	if r.configGeneration != generation {
		return false
	}
	r.noteConnection(code)
	return true
}
func (r *Runtime) noteDelivery(code DeliveryCode, retry bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if code == "" {
		r.lastDelivery = nil
	} else {
		r.lastDelivery = &DeliverySummary{Code: code, At: time.Now().UTC().Format(time.RFC3339Nano), Retryable: retry}
	}
	if err := r.persistSettingsLocked(); err != nil {
		r.storageAvailable = false
	}
}
func (r *Runtime) persistSettings() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistSettingsLocked()
}
func (r *Runtime) persistSettingsLocked() error {
	return r.store.saveSettings(storedEnvelope{DeliveryPaused: r.paused, ServiceStopped: r.stopped, Connection: r.connection, LastDelivery: r.lastDelivery})
}

func (r *Runtime) Capture(ctx context.Context, req CaptureRequest) (CaptureOutcome, error) {
	r.intakeMu.Lock()
	intakeLocked := true
	defer func() {
		if intakeLocked {
			r.intakeMu.Unlock()
		}
	}()
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return CaptureOutcome{}, ErrServiceStoppedSentinel
	}
	if r.quiescing {
		r.mu.Unlock()
		return CaptureOutcome{}, ErrServiceQuiescingSentinel
	}
	if !r.storageAvailable {
		r.mu.Unlock()
		return CaptureOutcome{}, ErrStorageFailedSentinel
	}
	r.mu.Unlock()
	if r.intakeAdmissionHook != nil {
		r.intakeAdmissionHook()
	}
	config, err := r.store.configuration()
	if err != nil {
		r.markStorageFailure(err)
		return CaptureOutcome{}, err
	}
	if err = validateConfiguration(config); err != nil {
		return CaptureOutcome{}, ErrKeyUnavailableSentinel
	}
	if !sameBinding(req.Binding, config) {
		return CaptureOutcome{}, ErrConflictSentinel
	}
	if req.RequestID == "" || len(req.RequestID) > 256 || req.Record.Type == "" || req.Record.Title == "" || len(req.Record.Payload) == 0 || !json.Valid(req.Record.Payload) {
		return CaptureOutcome{}, ErrConflictSentinel
	}
	// This is internal retry semantics, never caller-controlled protocol data.
	req.ClientSavedAtGenerated = false
	req.Binding = Binding{ServerURL: config.ServerURL, CredentialFingerprint: config.CredentialFingerprint}
	if req.ClientSavedAt == "" {
		req.ClientSavedAt = time.Now().UTC().Format(time.RFC3339Nano)
		req.ClientSavedAtGenerated = true
	} else if _, err := time.Parse(time.RFC3339Nano, req.ClientSavedAt); err != nil {
		return CaptureOutcome{}, ErrConflictSentinel
	}
	if _, err := url.ParseRequestURI(req.Binding.ServerURL); err != nil {
		return CaptureOutcome{}, ErrConflictSentinel
	}
	waiter := r.registerDeliveryWaiter(req.RequestID)
	defer r.releaseDeliveryWaiter(waiter)
	now := time.Now().UTC()
	receipt := Receipt{State: ReceiptPending, AcceptedAt: now.Format(time.RFC3339Nano), ClientSavedAt: req.ClientSavedAt, Content: &ReceiptContent{RequestID: req.RequestID, Record: req.Record, Scope: req.Scope, GitContext: req.GitContext, ClientSavedAt: req.ClientSavedAt}}
	if req.Scope.ProjectID == "" && len(req.GitContext) == 0 {
		receipt.State = ReceiptNeedsSelection
	}
	receipt, err = r.store.accept(req, receipt)
	if err != nil {
		r.markStorageFailure(err)
		return CaptureOutcome{}, err
	}
	// Drain ends at the durable local commit; the caller's bounded fast-path
	// wait and all remote work happen after releasing the intake barrier.
	r.intakeMu.Unlock()
	intakeLocked = false
	select {
	case r.wake <- struct{}{}:
	default:
	}
	deadline := time.NewTimer(FastConfirmWait)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		latest, e := r.LookupReceipt(ctx, receipt.ReceiptID)
		if e == nil && latest.State == ReceiptDelivered {
			out := CaptureOutcome{Outcome: OutcomeDelivered, Receipt: latest}
			var res DeliveryResult
			ok := false
			if receipt.State != ReceiptDelivered && waiter != nil {
				select {
				case <-waiter.done:
					if waiter.resultAvailable {
						res, ok = cloneDeliveryResult(waiter.result), true
					}
				case <-ctx.Done():
				case <-deadline.C:
				}
			} else {
				res, ok = r.deliveryResult(receipt.ReceiptID)
			}
			if ok {
				out.CentralRecord = res.CentralRecord
				out.CentralProject = res.CentralProject
				out.Mirror = res.Mirror
			}
			return out, nil
		}
		select {
		case <-ctx.Done():
			return CaptureOutcome{Outcome: OutcomeSavedPending, Receipt: receipt}, nil
		case <-deadline.C:
			return CaptureOutcome{Outcome: OutcomeSavedPending, Receipt: latestIfAvailable(latest, receipt)}, nil
		case <-poll.C:
		}
	}
}
func latestIfAvailable(latest, fallback Receipt) Receipt {
	if latest.ReceiptID != "" {
		return latest
	}
	return fallback
}

func (r *Runtime) LookupReceipt(ctx context.Context, id string) (Receipt, error) {
	_, env, err := r.store.loadReceipt(id)
	r.markStorageFailure(err)
	if err != nil {
		return Receipt{}, err
	}
	if (env.Receipt.State == ReceiptDelivered || env.Receipt.State == ReceiptDiscarded) && env.TombstoneUntil != "" {
		if until, parseErr := time.Parse(time.RFC3339Nano, env.TombstoneUntil); parseErr == nil && !time.Now().Before(until) {
			return Receipt{}, ErrReceiptNotFoundSentinel
		}
	}
	return env.Receipt, nil
}
func (r *Runtime) ListReceipts(ctx context.Context, q ListReceiptsRequest) (ListReceiptsResponse, error) {
	if err := r.store.prune(time.Now()); err != nil {
		r.markStorageFailure(err)
		return ListReceiptsResponse{}, err
	}
	out, err := r.store.receipts(q)
	r.markStorageFailure(err)
	return out, err
}
func (r *Runtime) DiscardReceipt(ctx context.Context, id string) (Receipt, error) {
	out, err := r.store.discard(id)
	r.markStorageFailure(err)
	return out, err
}
func (r *Runtime) SelectProject(ctx context.Context, q SelectProjectRequest) (Receipt, error) {
	if !r.workMu.TryLock() {
		return Receipt{}, ErrConflictSentinel
	}
	defer r.workMu.Unlock()
	id, env, err := r.store.loadReceipt(q.ReceiptID)
	if err != nil {
		r.markStorageFailure(err)
		return Receipt{}, err
	}
	if env.Request == nil || env.DeliveryAttempted {
		return Receipt{}, ErrConflictSentinel
	}
	if strings.TrimSpace(q.ProjectID) == "" {
		return Receipt{}, ErrProjectSelectionSentinel
	}
	env.Request.Scope.ProjectID = q.ProjectID
	env.ProjectResolved = true
	env.Receipt.State = ReceiptPending
	env.Receipt.DeliveryCode = ""
	env.Receipt.NextAttemptAt = ""
	env.Receipt.Content = &ReceiptContent{RequestID: env.Request.RequestID, Record: env.Request.Record, Scope: env.Request.Scope, GitContext: env.Request.GitContext, ClientSavedAt: env.Request.ClientSavedAt}
	if err = r.store.updateEnvelope(id, env, time.Now(), 0); err != nil {
		r.markStorageFailure(err)
		return Receipt{}, err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return env.Receipt, nil
}

func (r *Runtime) Status(ctx context.Context) (ServiceStatus, error) {
	states, err := r.store.counts()
	if err != nil {
		r.markStorageFailure(err)
		states = map[ReceiptState]int{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	lifecycle := r.lifecycle
	if r.stopped {
		lifecycle = LifecycleStopped
	} else if r.quiescing {
		lifecycle = LifecycleQuiescing
	} else if r.paused {
		lifecycle = LifecyclePaused
	}
	connection := r.connection
	if connection.CheckedAt != "" {
		checkedAt, parseErr := time.Parse(time.RFC3339Nano, connection.CheckedAt)
		if parseErr != nil || time.Since(checkedAt) > 2*r.healthInterval {
			connection.State = ConnectionUnknown
		}
	}
	return ServiceStatus{Lifecycle: lifecycle, Pending: states[ReceiptPending], Blocked: states[ReceiptBlocked], Rejected: states[ReceiptRejected], NeedsSelection: states[ReceiptNeedsSelection], KeyAvailable: true, StorageAvailable: r.storageAvailable, LastDelivery: r.lastDelivery, Connection: connection, QueueSchemaVersion: QueueSchemaVersion}, nil
}
func (r *Runtime) PauseDelivery(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = true
	if err := r.persistSettingsLocked(); err != nil {
		r.storageAvailable = false
		return err
	}
	return nil
}
func (r *Runtime) ResumeDelivery(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return ErrServiceStoppedSentinel
	}
	r.paused = false
	if err := r.persistSettingsLocked(); err != nil {
		r.storageAvailable = false
		return err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	select {
	case r.probeWake <- struct{}{}:
	default:
	}
	return nil
}
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	r.stopped = true
	r.lifecycle = LifecycleStopped
	if err := r.persistSettingsLocked(); err != nil {
		r.storageAvailable = false
		r.stopped = false
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()
	r.intakeMu.Lock()
	r.intakeMu.Unlock()
	// Drain an admitted delivery or health probe before acknowledging Stop.
	// Late admissions observe stopped after they acquire the work lock.
	r.workMu.Lock()
	r.workMu.Unlock()
	r.stopOnce.Do(func() { close(r.stop) })
	return nil
}
func (r *Runtime) Quiesce(ctx context.Context) error {
	r.mu.Lock()
	r.quiescing = true
	r.lifecycle = LifecycleQuiescing
	r.mu.Unlock()
	r.intakeMu.Lock()
	r.intakeMu.Unlock()
	r.workMu.Lock()
	r.workMu.Unlock()
	return nil
}

// finishQuiesce is called only after the IPC server has written the successful
// updater handshake response. This prevents closing the socket before the
// supervisor receives its acknowledgement.
func (r *Runtime) finishQuiesce() {
	if r.exitAfterQuiesce {
		r.stopOnce.Do(func() { close(r.stop) })
	}
}
func (r *Runtime) ResumeAfterUpdate(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return ErrServiceStoppedSentinel
	}
	if r.exitAfterQuiesce {
		select {
		case <-r.stop:
			// The successful quiesce handshake has committed this process to
			// exit. The supervisor must wait for drain and relaunch it; claiming
			// running here would report a state the worker cannot honor.
			return ErrServiceQuiescingSentinel
		default:
		}
	}
	r.quiescing = false
	r.lifecycle = LifecycleRunning
	select {
	case r.wake <- struct{}{}:
	default:
	}
	select {
	case r.probeWake <- struct{}{}:
	default:
	}
	return nil
}

func (r *Runtime) GetConfiguration(ctx context.Context) (ServiceConfiguration, error) {
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return r.store.configuration()
}
func (r *Runtime) SetConfiguration(ctx context.Context, c ServiceConfiguration) error {
	if err := validateConfiguration(c); err != nil {
		return ErrConflictSentinel
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ErrConflictSentinel
	}
	r.workMu.Lock()
	defer r.workMu.Unlock()
	r.configMu.Lock()
	defer r.configMu.Unlock()
	previous, err := r.store.configuration()
	if err != nil {
		r.markStorageFailure(err)
		return err
	}
	if err = r.store.saveConfiguration(c); err != nil {
		r.markStorageFailure(err)
		return err
	}
	if previous != c {
		r.configGeneration++
		r.mu.Lock()
		r.connection = ConnectionStatus{State: ConnectionUnknown}
		if err = r.persistSettingsLocked(); err != nil {
			r.storageAvailable = false
			r.mu.Unlock()
			return err
		}
		r.mu.Unlock()
	}
	if err = r.store.wakeRecoverable(c); err != nil {
		r.markStorageFailure(err)
		return err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	select {
	case r.probeWake <- struct{}{}:
	default:
	}
	return nil
}
func (r *Runtime) VerifyConnection(ctx context.Context) (VerifyConnectionResponse, error) {
	r.configMu.RLock()
	c, err := r.store.configuration()
	generation := r.configGeneration
	r.configMu.RUnlock()
	if err != nil {
		return VerifyConnectionResponse{}, err
	}
	if r.adapter == nil {
		return VerifyConnectionResponse{}, ErrStorageFailedSentinel
	}
	b := Binding{ServerURL: c.ServerURL, CredentialFingerprint: c.CredentialFingerprint, PublicKeyPath: c.PublicKeyPath, SigningKeyPath: c.SigningKeyPath}
	identity, err := r.adapter.VerifyConnection(ctx, b)
	if err != nil {
		code, _ := classifyFailure(err, DeliveryConnectivity, true)
		if !r.noteConnectionFor(generation, code) {
			return VerifyConnectionResponse{}, ErrConflictSentinel
		}
		return VerifyConnectionResponse{}, err
	}
	if canonicalURL(identity.ServerURL) != canonicalURL(c.ServerURL) || identity.CredentialID == "" || !hasCapability(identity.CaptureCapabilities, CapabilityClientSavedAtV1) {
		if !r.noteConnectionFor(generation, DeliveryServerIncompatible) {
			return VerifyConnectionResponse{}, ErrConflictSentinel
		}
		return VerifyConnectionResponse{}, &DeliveryFailure{Code: DeliveryServerIncompatible}
	}
	if !r.noteConnectionFor(generation, "") {
		return VerifyConnectionResponse{}, ErrConflictSentinel
	}
	if err = r.store.wakeRecoverable(c); err != nil {
		r.markStorageFailure(err)
		return VerifyConnectionResponse{}, err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return VerifyConnectionResponse{ServerURL: c.ServerURL, CredentialID: identity.CredentialID, CaptureCapabilities: identity.CaptureCapabilities, VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}
func (r *Runtime) ListProjects(ctx context.Context) (ListProjectsResponse, error) {
	r.configMu.RLock()
	c, err := r.store.configuration()
	generation := r.configGeneration
	r.configMu.RUnlock()
	if err != nil {
		return ListProjectsResponse{}, err
	}
	if r.adapter == nil {
		return ListProjectsResponse{}, ErrStorageFailedSentinel
	}
	projects, err := r.adapter.ListProjects(ctx, Binding{ServerURL: c.ServerURL, CredentialFingerprint: c.CredentialFingerprint, PublicKeyPath: c.PublicKeyPath, SigningKeyPath: c.SigningKeyPath})
	if err != nil {
		code, _ := classifyFailure(err, DeliveryConnectivity, true)
		if !r.noteConnectionFor(generation, code) {
			return ListProjectsResponse{}, ErrConflictSentinel
		}
		return ListProjectsResponse{}, err
	}
	if !r.noteConnectionFor(generation, "") {
		return ListProjectsResponse{}, ErrConflictSentinel
	}
	if err = r.store.wakeRecoverable(c); err != nil {
		r.markStorageFailure(err)
		return ListProjectsResponse{}, err
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return ListProjectsResponse{Projects: projects}, nil
}

var _ Service = (*Runtime)(nil)
