package localcapture

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const ipcDeadline = 5 * time.Second

type IPCClient struct {
	SocketPath string
	Timeout    time.Duration
}

func NewClient(socketPath string) *IPCClient {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return &IPCClient{SocketPath: socketPath, Timeout: ipcDeadline}
}

func (c *IPCClient) call(ctx context.Context, op Operation, body any, out any) error {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return ErrStorageFailedSentinel
	}
	id := fmt.Sprintf("%x", idBytes[:])
	data, err := json.Marshal(body)
	if err != nil || len(data) > MaxFrameBytes {
		return ErrConflictSentinel
	}
	request := Request{Version: ProtocolVersion, ID: id, Op: op, Body: data}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) > MaxFrameBytes {
		return ErrConflictSentinel
	}
	d := net.Dialer{Timeout: c.Timeout}
	conn, err := d.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
			return ErrServiceStoppedSentinel
		}
		return ErrStorageFailedSentinel
	}
	defer conn.Close()
	deadline := time.Now().Add(c.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err = writeFrame(conn, payload); err != nil {
		return ErrStorageFailedSentinel
	}
	reply, err := readFrame(conn)
	if err != nil {
		return ErrStorageFailedSentinel
	}
	var response Response
	if decodeStrict(reply, &response) != nil || response.Version != ProtocolVersion || response.ID != id {
		return ErrConflictSentinel
	}
	if response.Error != nil {
		return errorForWire(response.Error.Code)
	}
	if out == nil {
		return nil
	}
	if len(response.Result) == 0 || decodeStrict(response.Result, out) != nil {
		return ErrConflictSentinel
	}
	return nil
}

func (c *IPCClient) Capture(ctx context.Context, r CaptureRequest) (CaptureOutcome, error) {
	var out CaptureResponse
	if err := c.call(ctx, OpCapture, r, &out); err != nil {
		return CaptureOutcome{}, err
	}
	return CaptureOutcome{Outcome: out.Outcome, Receipt: out.Receipt, CentralRecord: out.CentralRecord, CentralProject: out.CentralProject, Mirror: out.Mirror}, nil
}
func (c *IPCClient) LookupReceipt(ctx context.Context, id string) (Receipt, error) {
	var out Receipt
	err := c.call(ctx, OpLookupReceipt, ReceiptRequest{ReceiptID: id}, &out)
	return out, err
}
func (c *IPCClient) ListReceipts(ctx context.Context, q ListReceiptsRequest) (ListReceiptsResponse, error) {
	var out ListReceiptsResponse
	err := c.call(ctx, OpListReceipts, q, &out)
	return out, err
}
func (c *IPCClient) DiscardReceipt(ctx context.Context, id string) (Receipt, error) {
	var out Receipt
	err := c.call(ctx, OpDiscardReceipt, ReceiptRequest{ReceiptID: id}, &out)
	return out, err
}
func (c *IPCClient) SelectProject(ctx context.Context, q SelectProjectRequest) (Receipt, error) {
	var out Receipt
	err := c.call(ctx, OpSelectProject, q, &out)
	return out, err
}
func (c *IPCClient) Status(ctx context.Context) (ServiceStatus, error) {
	var out ServiceStatus
	err := c.call(ctx, OpStatus, struct{}{}, &out)
	return out, err
}
func (c *IPCClient) PauseDelivery(ctx context.Context) error {
	return c.call(ctx, OpPauseDelivery, struct{}{}, nil)
}
func (c *IPCClient) ResumeDelivery(ctx context.Context) error {
	return c.call(ctx, OpResumeDelivery, struct{}{}, nil)
}
func (c *IPCClient) Stop(ctx context.Context) error { return c.call(ctx, OpStop, struct{}{}, nil) }
func (c *IPCClient) Quiesce(ctx context.Context) error {
	return c.call(ctx, OpQuiesce, struct{}{}, nil)
}
func (c *IPCClient) ResumeAfterUpdate(ctx context.Context) error {
	return c.call(ctx, OpResumeAfterUpdate, struct{}{}, nil)
}
func (c *IPCClient) GetConfiguration(ctx context.Context) (ServiceConfiguration, error) {
	var out ConfigurationResponse
	err := c.call(ctx, OpGetConfiguration, struct{}{}, &out)
	return out.Configuration, err
}
func (c *IPCClient) SetConfiguration(ctx context.Context, cfg ServiceConfiguration) error {
	return c.call(ctx, OpSetConfiguration, SetConfigurationRequest{Configuration: cfg}, nil)
}
func (c *IPCClient) VerifyConnection(ctx context.Context) (VerifyConnectionResponse, error) {
	var out VerifyConnectionResponse
	err := c.call(ctx, OpVerifyConnection, struct{}{}, &out)
	return out, err
}
func (c *IPCClient) ListProjects(ctx context.Context) (ListProjectsResponse, error) {
	var out ListProjectsResponse
	err := c.call(ctx, OpListProjects, struct{}{}, &out)
	return out, err
}

type IPCServer struct {
	Service        Service
	SocketPath     string
	listener       *net.UnixListener
	socketInfo     os.FileInfo
	mu             sync.Mutex
	RequestTimeout time.Duration
	ControlTimeout time.Duration
	operationMu    sync.RWMutex
	quiescing      bool // guarded by operationMu
}

const defaultControlTimeout = 110 * time.Second

func DefaultSocketPath() string {
	uid := strconv.Itoa(os.Getuid())
	if runtimeOS() == "linux" {
		if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
			if fi, err := os.Lstat(runtimeDir); err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 && fi.Mode().Perm()&0077 == 0 && ownedByCurrentUser(fi) {
				return filepath.Join(runtimeDir, "scratchpad", "service.sock")
			}
		}
	}
	// Native clients, login agents and manually launched MCP processes may
	// inherit different TMPDIR values. Keep the fallback stable across those
	// environments; only a validated Linux XDG runtime directory overrides it.
	return filepath.Join("/tmp", "scratchpad-"+uid, "service.sock")
}

func ListenIPC(path string, svc Service) (*IPCServer, error) {
	guard, err := AcquireStartupGuard(path)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	if err = guard.Prepare(); err != nil {
		return nil, err
	}
	return guard.ListenIPC(svc)
}

func (g *StartupGuard) ListenIPC(svc Service) (*IPCServer, error) {
	if g == nil || g.closed || g.lockFile == nil {
		return nil, ErrStorageFailedSentinel
	}
	if !g.prepared {
		if err := g.Prepare(); err != nil {
			return nil, err
		}
	}
	addr := &net.UnixAddr{Name: g.socketPath, Net: "unix"}
	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	if err = os.Chmod(g.socketPath, 0600); err != nil {
		listener.Close()
		return nil, ErrStorageFailedSentinel
	}
	socketInfo, err := os.Lstat(g.socketPath)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 || !ownedByCurrentUser(socketInfo) {
		listener.Close()
		return nil, ErrStorageFailedSentinel
	}
	listener.SetUnlinkOnClose(false)
	return &IPCServer{Service: svc, SocketPath: g.socketPath, listener: listener, socketInfo: socketInfo, RequestTimeout: ipcDeadline, ControlTimeout: defaultControlTimeout}, nil
}

func (s *IPCServer) Serve(ctx context.Context) error {
	if s.listener == nil || s.Service == nil {
		return ErrStorageFailedSentinel
	}
	if runtime, ok := s.Service.(*Runtime); ok {
		go func() { <-runtime.done(); _ = s.listener.Close() }()
	}
	go func() { <-ctx.Done(); _ = s.listener.Close() }()
	var wg sync.WaitGroup
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			break
		}
		wg.Add(1)
		go func(c *net.UnixConn) { defer wg.Done(); s.serveOne(c) }(conn)
	}
	wg.Wait()
	_ = s.listener.Close()
	s.unlinkOwnedSocket()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (s *IPCServer) Close() error {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.unlinkOwnedSocket()
	return nil
}

func (s *IPCServer) unlinkOwnedSocket() {
	if s.SocketPath == "" || s.socketInfo == nil {
		return
	}
	guard, err := AcquireStartupGuard(s.SocketPath)
	if err != nil {
		return
	}
	defer guard.Close()
	current, err := os.Lstat(s.SocketPath)
	if err == nil && os.SameFile(s.socketInfo, current) {
		_ = os.Remove(s.SocketPath)
	}
}

func (s *IPCServer) serveOne(conn *net.UnixConn) {
	defer conn.Close()
	requestTimeout := s.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = ipcDeadline
	}
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))
	if !authorizedPeer(conn) {
		s.reply(conn, Response{Version: ProtocolVersion, Error: &WireError{Code: ErrUnauthorizedPeer, Message: "Local service peer is not authorized."}})
		return
	}
	frame, err := readFrame(conn)
	if err != nil {
		s.reply(conn, Response{Version: ProtocolVersion, Error: &WireError{Code: ErrInvalidRequest, Message: "Request is invalid."}})
		return
	}
	var req Request
	if decodeStrict(frame, &req) != nil || req.ID == "" || len(req.ID) > 256 || req.Op == "" {
		s.reply(conn, Response{Version: ProtocolVersion, Error: &WireError{Code: ErrInvalidRequest, Message: "Request is invalid."}})
		return
	}
	if req.Version != ProtocolVersion {
		s.reply(conn, Response{Version: ProtocolVersion, ID: req.ID, Error: &WireError{Code: ErrProtocolVersion, Message: "Protocol version is unsupported."}})
		return
	}
	if req.Op == OpQuiesce || req.Op == OpStop || req.Op == OpResumeAfterUpdate {
		controlTimeout := s.ControlTimeout
		if controlTimeout <= 0 {
			controlTimeout = defaultControlTimeout
		}
		_ = conn.SetDeadline(time.Now().Add(controlTimeout))
	}
	if req.Op == OpQuiesce || req.Op == OpResumeAfterUpdate {
		s.operationMu.Lock()
		defer s.operationMu.Unlock()
		if req.Op == OpQuiesce {
			s.quiescing = true
		}
	} else {
		s.operationMu.RLock()
		defer s.operationMu.RUnlock()
		if s.quiescing {
			s.reply(conn, Response{Version: ProtocolVersion, ID: req.ID, Error: &WireError{Code: ErrServiceQuiescing, Message: safeMessage(ErrServiceQuiescing)}})
			return
		}
	}
	result, err := s.dispatch(req)
	if err != nil {
		code := wireCode(err)
		s.reply(conn, Response{Version: ProtocolVersion, ID: req.ID, Error: &WireError{Code: code, Message: safeMessage(code)}})
		return
	}
	if req.Op == OpResumeAfterUpdate {
		// The exclusive operation lock makes reopening the IPC gate atomic with
		// the runtime resume. Failed resumes leave the gate closed.
		s.quiescing = false
	}
	data := json.RawMessage(`{}`)
	if result != nil {
		data, err = json.Marshal(result)
		if err != nil {
			s.reply(conn, Response{Version: ProtocolVersion, ID: req.ID, Error: &WireError{Code: ErrInternal, Message: safeMessage(ErrInternal)}})
			return
		}
	}
	s.reply(conn, Response{Version: ProtocolVersion, ID: req.ID, Result: data})
	if req.Op == OpQuiesce && s.Service != nil {
		if runtime, ok := s.Service.(*Runtime); ok {
			runtime.finishQuiesce()
		}
	}
}

func (s *IPCServer) reply(c *net.UnixConn, r Response) {
	data, err := json.Marshal(r)
	if err == nil && len(data) <= MaxFrameBytes {
		_ = writeFrame(c, data)
	}
}
func bodyAs(data json.RawMessage, out any) error {
	if len(data) == 0 || !json.Valid(data) {
		return ErrConflictSentinel
	}
	if decodeStrict(data, out) != nil {
		return ErrConflictSentinel
	}
	return nil
}

func decodeStrict(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func emptyBody(data json.RawMessage) error {
	var body struct{}
	return bodyAs(data, &body)
}

func (s *IPCServer) dispatch(req Request) (any, error) {
	ctx := context.Background()
	switch req.Op {
	case OpCapture:
		var b CaptureRequest
		if bodyAs(req.Body, &b) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.Capture(ctx, b)
	case OpLookupReceipt:
		var b ReceiptRequest
		if bodyAs(req.Body, &b) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.LookupReceipt(ctx, b.ReceiptID)
	case OpListReceipts:
		var b ListReceiptsRequest
		if bodyAs(req.Body, &b) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.ListReceipts(ctx, b)
	case OpDiscardReceipt:
		var b ReceiptRequest
		if bodyAs(req.Body, &b) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.DiscardReceipt(ctx, b.ReceiptID)
	case OpSelectProject:
		var b SelectProjectRequest
		if bodyAs(req.Body, &b) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.SelectProject(ctx, b)
	case OpStatus:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.Status(ctx)
	case OpPauseDelivery:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return nil, s.Service.PauseDelivery(ctx)
	case OpResumeDelivery:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return nil, s.Service.ResumeDelivery(ctx)
	case OpStop:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return nil, s.Service.Stop(ctx)
	case OpQuiesce:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return nil, s.Service.Quiesce(ctx)
	case OpResumeAfterUpdate:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return nil, s.Service.ResumeAfterUpdate(ctx)
	case OpGetConfiguration:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		c, e := s.Service.GetConfiguration(ctx)
		return ConfigurationResponse{Configuration: c}, e
	case OpSetConfiguration:
		var b SetConfigurationRequest
		if bodyAs(req.Body, &b) != nil {
			return nil, ErrConflictSentinel
		}
		return nil, s.Service.SetConfiguration(ctx, b.Configuration)
	case OpVerifyConnection:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.VerifyConnection(ctx)
	case OpListProjects:
		if emptyBody(req.Body) != nil {
			return nil, ErrConflictSentinel
		}
		return s.Service.ListProjects(ctx)
	default:
		return nil, ErrUnknownOperationSentinel
	}
}

func wireCode(err error) ErrorCode {
	switch {
	case errors.Is(err, ErrServiceStoppedSentinel):
		return ErrServiceStopped
	case errors.Is(err, ErrServiceQuiescingSentinel):
		return ErrServiceQuiescing
	case errors.Is(err, ErrKeyUnlockRequiredSentinel):
		return ErrKeyUnlockRequired
	case errors.Is(err, ErrKeyUnavailableSentinel):
		return ErrKeyUnavailable
	case errors.Is(err, ErrStorageFailedSentinel):
		return ErrStorageFailed
	case errors.Is(err, ErrReceiptNotFoundSentinel):
		return ErrReceiptNotFound
	case errors.Is(err, ErrDiscardNotAllowedSentinel):
		return ErrDiscardNotAllowed
	case errors.Is(err, ErrProjectSelectionSentinel):
		return ErrProjectSelection
	case errors.Is(err, ErrUnknownOperationSentinel):
		return ErrUnknownOperation
	case errors.Is(err, ErrConflictSentinel):
		return ErrConflict
	default:
		var f *DeliveryFailure
		if errors.As(err, &f) {
			if knownDeliveryCode(f.Code) {
				return ErrorCode(f.Code)
			}
		}
		return ErrInternal
	}
}
func safeMessage(code ErrorCode) string {
	switch code {
	case ErrInvalidRequest:
		return "Request is invalid."
	case ErrProtocolVersion:
		return "Protocol version is unsupported."
	case ErrUnknownOperation:
		return "Operation is unsupported."
	case ErrUnauthorizedPeer:
		return "Local service peer is not authorized."
	case ErrServiceStopped:
		return "Scratchpad service is stopped."
	case ErrServiceQuiescing:
		return "Scratchpad service is preparing to update."
	case ErrKeyUnavailable:
		return "Scratchpad's local encryption key is unavailable."
	case ErrKeyUnlockRequired:
		return "Unlock the login keychain, then start Scratchpad again."
	case ErrStorageFailed:
		return "Scratchpad could not save local data."
	case ErrReceiptNotFound:
		return "Capture receipt was not found."
	case ErrDiscardNotAllowed:
		return "Capture receipt cannot be discarded."
	case ErrProjectSelection:
		return "Choose a project before delivery."
	case ErrConflict:
		return "Request conflicts with an existing capture."
	case ErrorCode(DeliveryConnectivity):
		return "Scratchpad server is unreachable."
	case ErrorCode(DeliveryAuthentication):
		return "Scratchpad authentication failed."
	case ErrorCode(DeliveryCredentialChanged):
		return "The enrolled SSH credential changed."
	case ErrorCode(DeliveryServerIncompatible):
		return "Scratchpad server cannot accept this capture version."
	case ErrorCode(DeliveryProjectAmbiguous):
		return "Choose a project before delivery."
	case ErrorCode(DeliveryServerRejected):
		return "Scratchpad rejected this capture."
	case ErrorCode(DeliveryConflict):
		return "Capture conflicts with existing Scratchpad data."
	default:
		return "Scratchpad service could not complete the request."
	}
}
func errorForWire(code ErrorCode) error {
	switch code {
	case ErrUnauthorizedPeer:
		return ErrUnauthorizedPeerSentinel
	case ErrServiceStopped:
		return ErrServiceStoppedSentinel
	case ErrServiceQuiescing:
		return ErrServiceQuiescingSentinel
	case ErrKeyUnavailable:
		return ErrKeyUnavailableSentinel
	case ErrKeyUnlockRequired:
		return ErrKeyUnlockRequiredSentinel
	case ErrStorageFailed:
		return ErrStorageFailedSentinel
	case ErrReceiptNotFound:
		return ErrReceiptNotFoundSentinel
	case ErrDiscardNotAllowed:
		return ErrDiscardNotAllowedSentinel
	case ErrProjectSelection:
		return ErrProjectSelectionSentinel
	case ErrConflict, ErrInvalidRequest, ErrProtocolVersion:
		return ErrConflictSentinel
	case ErrInternal:
		return ErrInternalSentinel
	default:
		if knownDeliveryCode(DeliveryCode(code)) {
			return &DeliveryFailure{Code: DeliveryCode(code)}
		}
		return ErrInternalSentinel
	}
}

func knownDeliveryCode(code DeliveryCode) bool {
	switch code {
	case DeliveryConnectivity, DeliveryAuthentication, DeliveryCredentialChanged, DeliveryServerIncompatible, DeliveryProjectAmbiguous, DeliveryServerRejected, DeliveryConflict:
		return true
	default:
		return false
	}
}

func writeFrame(w io.Writer, data []byte) error {
	if len(data) == 0 || len(data) > MaxFrameBytes {
		return ErrConflictSentinel
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := io.Copy(w, bytes.NewReader(header[:])); err != nil {
		return err
	}
	_, err := io.Copy(w, bytes.NewReader(data))
	return err
}
func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxFrameBytes {
		return nil, ErrConflictSentinel
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (r *Runtime) done() <-chan struct{}  { return r.stop }
func (r *Runtime) InitiallyStopped() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.stopped }
func runtimeOS() string                   { return runtime.GOOS }
