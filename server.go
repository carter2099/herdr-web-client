package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	sessionRequestHeader = "X-Herdr-Web-Client-Request"
	sessionRequestValue  = "session"
	maxOutstandingNonces = 32

	maxOutstandingDetachTokens = 32
)

var (
	errNonceCapacity       = errors.New("too many outstanding session nonces")
	errDetachTokenCapacity = errors.New("too many outstanding detach tokens")
)

//go:embed web/dist
var webDist embed.FS

type nonceStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]time.Time
}

func newNonceStore(ttl time.Duration) *nonceStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &nonceStore{ttl: ttl, items: make(map[string]time.Time)}
}

func (n *nonceStore) issue(now time.Time) (string, time.Time, error) {
	if n == nil {
		return "", time.Time{}, errors.New("cannot issue nonce without a store")
	}
	deadline := now.Add(n.ttl)

	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", time.Time{}, fmt.Errorf("generate nonce: %w", err)
	}
	// Raw URL encoding is safe in JSON and does not introduce URL separators.
	nonce := encodeNonce(bytes)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.pruneLocked(now)
	if len(n.items) >= maxOutstandingNonces {
		return "", time.Time{}, errNonceCapacity
	}
	n.items[nonce] = deadline
	return nonce, deadline, nil
}

func (n *nonceStore) consume(nonce string, now time.Time) bool {
	if n == nil || nonce == "" {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	expiresAt, ok := n.items[nonce]
	if !ok {
		n.pruneLocked(now)
		return false
	}
	if !expiresAt.After(now) {
		delete(n.items, nonce)
		return false
	}
	delete(n.items, nonce)
	return true
}

func (n *nonceStore) pruneLocked(now time.Time) {
	for token, expiresAt := range n.items {
		if !expiresAt.After(now) {
			delete(n.items, token)
		}
	}
}

// encodeNonce is kept separate to make the random token's representation
// explicit and easy to audit.
func encodeNonce(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

type activeAttachment struct {
	cancel context.CancelFunc
	done   chan struct{}

	detached        atomic.Bool
	detachable      bool
	detachRequested bool
	finished        bool
	quarantined     bool
	cleanupErr      error
}

type detachGrant struct {
	attachment *activeAttachment
	expiresAt  time.Time
}

type detachTokenStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]detachGrant
}

func newDetachTokenStore(ttl time.Duration) *detachTokenStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &detachTokenStore{ttl: ttl, items: make(map[string]detachGrant)}
}

func (d *detachTokenStore) issue(attachment *activeAttachment, now time.Time) (string, time.Time, error) {
	if d == nil || attachment == nil {
		return "", time.Time{}, errors.New("cannot issue detach token without an attachment")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pruneLocked(now)
	for token, grant := range d.items {
		if grant.attachment == attachment && grant.expiresAt.After(now) {
			grant.expiresAt = now.Add(d.ttl)
			d.items[token] = grant
			return token, grant.expiresAt, nil
		}
	}
	if len(d.items) >= maxOutstandingDetachTokens {
		return "", time.Time{}, errDetachTokenCapacity
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", time.Time{}, fmt.Errorf("generate detach token: %w", err)
	}
	token := encodeNonce(bytes)
	expiresAt := now.Add(d.ttl)
	d.items[token] = detachGrant{
		attachment: attachment,
		expiresAt:  expiresAt,
	}
	return token, expiresAt, nil
}

func (d *detachTokenStore) take(token string, now time.Time) (detachGrant, bool) {
	if d == nil || token == "" {
		return detachGrant{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	grant, ok := d.items[token]
	if !ok {
		d.pruneLocked(now)
		return detachGrant{}, false
	}
	if !grant.expiresAt.After(now) {
		delete(d.items, token)
		return detachGrant{}, false
	}
	delete(d.items, token)
	return grant, true
}

func (d *detachTokenStore) pruneLocked(now time.Time) {
	for token, grant := range d.items {
		if !grant.expiresAt.After(now) {
			delete(d.items, token)
		}
	}
}

type Server struct {
	cfg          Config
	launcher     Launcher
	completions  AgentCompletionSource
	socketSlots  chan struct{}
	nonces       *nonceStore
	detachTokens *detachTokenStore
	assets       http.Handler

	ctx       context.Context
	cancel    context.CancelFunc
	activeMu  sync.Mutex
	active    *activeAttachment
	closeOnce sync.Once
}

// NewServer constructs the HTTP/WebSocket surface. It does not start
// listening; main owns the net/http lifecycle.
func NewServer(cfg Config, launcher Launcher, completions AgentCompletionSource) (*Server, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validateServer(); err != nil {
		return nil, err
	}
	if launcher == nil {
		return nil, errors.New("PTY launcher is required")
	}
	assets, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded web assets: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:          cfg,
		socketSlots:  make(chan struct{}, 1),
		launcher:     launcher,
		completions:  completions,
		nonces:       newNonceStore(cfg.NonceTTL),
		detachTokens: newDetachTokenStore(cfg.NonceTTL),
		assets:       http.FileServer(http.FS(assets)),
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

func (s *Server) Handler() http.Handler { return s }

func (s *Server) claimSocket() bool {
	select {
	case s.socketSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseSocket() {
	<-s.socketSlots
}

func (s *Server) socketBusy() bool {
	return len(s.socketSlots) != 0
}

// Close cancels the server context and the one active child, if any. It never
// signals a Herdr daemon or any process not owned by the active PTY session.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.cancel()
		s.activeMu.Lock()
		active := s.active
		s.activeMu.Unlock()
		if active != nil && active.cancel != nil {
			active.cancel()
		}
	})
	return nil
}

func (s *Server) setSecurityHeaders(w http.ResponseWriter) {
	webSocketOrigin := "wss://" + s.cfg.publicHost()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(
		"Content-Security-Policy",
		"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self'; connect-src 'self' "+webSocketOrigin+"; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'; worker-src 'none'",
	)
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), clipboard-write=(self), display-capture=(), geolocation=(), microphone=(), payment=(), usb=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.NotFound(w, r)
		return
	}
	s.setSecurityHeaders(w)
	if !s.cfg.strictHost(r.Host) {
		http.NotFound(w, r)
		return
	}

	switch r.URL.Path {
	case "/api/session":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.handleSession(w, r)
	case "/api/detach":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleDetach(w, r)
	case "/api/attach":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.handleAttach(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, http.MethodGet+", "+http.MethodHead)
			return
		}
		s.handleStatic(w, r)
	}
}
func (s *Server) claimActiveAttachment(active *activeAttachment) bool {
	if active == nil {
		return false
	}
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active != nil {
		return false
	}
	s.active = active
	return true
}

func (s *Server) markAttachmentDetachable(active *activeAttachment) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active != active || active.finished || active.quarantined || active.detachRequested {
		return false
	}
	active.detachable = true
	return true
}

func (s *Server) finishActive(active *activeAttachment, cleanupErr error) {
	if active == nil {
		return
	}
	s.activeMu.Lock()
	active.detachable = false
	active.finished = true
	active.cleanupErr = cleanupErr
	if s.active == active {
		s.active = nil
	}
	s.activeMu.Unlock()
}

func (s *Server) activeCleanupError(active *activeAttachment) error {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if active == nil {
		return errors.New("attachment is unavailable")
	}
	return active.cleanupErr
}

func (s *Server) attachmentQuarantined(active *activeAttachment) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return active != nil && active.quarantined
}

func (s *Server) quarantineAttachment(active *activeAttachment) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if active != nil {
		active.quarantined = true
		if s.active == active {
			// Cancel while ownership is locked, never after a replacement claims it.
			s.cancel()
		}
	}
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if values := r.Header.Values(sessionRequestHeader); len(values) != 1 || values[0] != sessionRequestValue {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.socketBusy() {
		type conflictResponse struct {
			Error       string     `json:"error"`
			DetachNonce string     `json:"detach_nonce,omitempty"`
			ExpiresAt   *time.Time `json:"expires_at,omitempty"`
		}
		response := conflictResponse{Error: "another attachment is already active"}
		if nonce, expiresAt, ok := s.issueDetachToken(time.Now()); ok {
			response.DetachNonce = nonce
			response.ExpiresAt = &expiresAt
		}
		writeJSON(w, http.StatusConflict, response)
		return
	}
	now := time.Now()
	nonce, expiresAt, err := s.nonces.issue(now)
	if errors.Is(err, errNonceCapacity) {
		http.Error(w, "too many pending sessions", http.StatusTooManyRequests)
		return
	}
	if err != nil {
		http.Error(w, "unable to create session", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Nonce     string    `json:"nonce"`
		ExpiresAt time.Time `json:"expires_at"`
	}{Nonce: nonce, ExpiresAt: expiresAt})
}

func (s *Server) issueDetachToken(now time.Time) (string, time.Time, bool) {
	s.activeMu.Lock()
	active := s.active
	detachable := active != nil && active.detachable && !active.finished && !active.quarantined && !active.detachRequested
	s.activeMu.Unlock()
	if !detachable {
		return "", time.Time{}, false
	}
	token, expiresAt, err := s.detachTokens.issue(active, now)
	if err != nil {
		return "", time.Time{}, false
	}
	s.activeMu.Lock()
	stillDetachable := s.active == active && active.detachable && !active.finished && !active.quarantined && !active.detachRequested
	s.activeMu.Unlock()
	if !stillDetachable {
		return "", time.Time{}, false
	}
	return token, expiresAt, true
}

func (s *Server) handleDetach(w http.ResponseWriter, r *http.Request) {
	if !hasExactOrigin(r, s.cfg.PublicOrigin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if values := r.Header.Values(sessionRequestHeader); len(values) != 1 || values[0] != sessionRequestValue {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 || !strings.EqualFold(strings.TrimSpace(contentTypes[0]), "application/json") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(s.cfg.HelloTimeout))
	maxBytes := s.cfg.MaxInboundBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil || int64(len(payload)) > maxBytes {
		http.Error(w, "invalid detach request", http.StatusBadRequest)
		return
	}
	token, err := decodeDetach(payload)
	if err != nil {
		http.Error(w, "invalid detach request", http.StatusBadRequest)
		return
	}
	grant, ok := s.detachTokens.take(token, time.Now())
	if !ok {
		http.Error(w, "invalid or expired detach token", http.StatusForbidden)
		return
	}

	s.activeMu.Lock()
	active := s.active
	if active == nil || active != grant.attachment || active.finished {
		s.activeMu.Unlock()
		http.Error(w, "attachment is no longer active", http.StatusConflict)
		return
	}
	if !active.detachable || active.detachRequested || active.quarantined {
		s.activeMu.Unlock()
		http.Error(w, "attachment is no longer detachable", http.StatusConflict)
		return
	}
	active.detachable = false
	active.detachRequested = true
	active.detached.Store(true)
	cancel := active.cancel
	s.activeMu.Unlock()

	if cancel != nil {
		cancel()
	}
	timer := time.NewTimer(s.cfg.WriteTimeout)
	defer timer.Stop()
	select {
	case <-active.done:
		if cleanupErr := s.activeCleanupError(active); cleanupErr != nil {
			http.Error(w, "attachment teardown failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case <-timer.C:
		s.quarantineAttachment(active)
		http.Error(w, "attachment teardown could not be confirmed", http.StatusServiceUnavailable)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.assets == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	// FileServer handles path cleaning and rejects traversal outside the
	// embedded fs. Never let it serve an API path as a static fallback.
	s.assets.ServeHTTP(w, r)
}

func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	if !hasExactOrigin(r, s.cfg.PublicOrigin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	protocolValues := r.Header.Values("Sec-WebSocket-Protocol")
	if len(protocolValues) != 1 || !hasExactSubprotocol(protocolValues[0], webSocketSubprotocol) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if !s.claimSocket() {
		http.Error(w, "another attachment is already active", http.StatusConflict)
		return
	}
	releaseSocket := true
	var active *activeAttachment
	var cleanupErr error
	defer func() {
		s.finishActive(active, cleanupErr)
		if releaseSocket {
			s.releaseSocket()
		}
		if active != nil {
			close(active.done)
		}
	}()
	quarantine := func(err error) {
		if err != nil {
			releaseSocket = false
			s.quarantineAttachment(active)
			log.Printf("attachment teardown could not be confirmed; server quarantined: %v", err)
			return
		}
		if s.attachmentQuarantined(active) {
			releaseSocket = false
		}
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:    32 * 1024,
		WriteBufferSize:   32 * 1024,
		Subprotocols:      []string{webSocketSubprotocol},
		EnableCompression: false,
		CheckOrigin: func(request *http.Request) bool {
			return hasExactOrigin(request, s.cfg.PublicOrigin)
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(s.cfg.MaxInboundBytes)
	_ = conn.SetReadDeadline(time.Now().Add(s.cfg.HelloTimeout))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		writeAttachError(conn, errExpectedHello.Error(), websocket.ClosePolicyViolation)
		return
	}
	if messageType != websocket.TextMessage {
		writeAttachError(conn, errBinaryBeforeHello.Error(), websocket.ClosePolicyViolation)
		return
	}
	nonce, dimensions, err := decodeHello(payload)
	if err != nil {
		writeAttachError(conn, "invalid hello message", websocket.ClosePolicyViolation)
		return
	}
	if !s.nonces.consume(nonce, time.Now()) {
		writeAttachError(conn, "invalid or expired session", websocket.ClosePolicyViolation)
		return
	}

	attachCtx, cancel := context.WithCancel(s.ctx)
	active = &activeAttachment{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if !s.claimActiveAttachment(active) {
		cancel()
		writeAttachError(conn, "another attachment is already active", websocket.CloseTryAgainLater)
		return
	}
	defer cancel()
	attachCtx = context.WithValue(attachCtx, attachmentContextKey{}, active)

	if err := attachCtx.Err(); err != nil {
		writeAttachError(conn, "server is shutting down", websocket.CloseGoingAway)
		return
	}
	session, err := s.launcher.Start(attachCtx, dimensions.Cols, dimensions.Rows)
	if err != nil {
		log.Printf("terminal startup failed: %v", err)
		if session != nil {
			closeErr := session.Close()
			_, waitErr := session.Wait()
			cleanupErr = errors.Join(closeErr, waitErr)
			quarantine(cleanupErr)
		}
		writeAttachError(conn, "unable to start terminal", websocket.CloseInternalServerErr)
		return
	}
	if session == nil {
		writeAttachError(conn, "unable to start terminal", websocket.CloseInternalServerErr)
		return
	}
	if err := attachCtx.Err(); err != nil {
		closeErr := session.Close()
		_, waitErr := session.Wait()
		cleanupErr = errors.Join(closeErr, waitErr)
		quarantine(cleanupErr)
		writeAttachError(conn, "server is shutting down", websocket.CloseGoingAway)
		return
	}
	if !s.markAttachmentDetachable(active) {
		closeErr := session.Close()
		_, waitErr := session.Wait()
		cleanupErr = errors.Join(closeErr, waitErr)
		quarantine(cleanupErr)
		writeAttachError(conn, "server is shutting down", websocket.CloseGoingAway)
		return
	}
	cleanupErr = runBridge(attachCtx, conn, session, s.completions, s.cfg)
	quarantine(cleanupErr)
}

func hasExactOrigin(r *http.Request, expected string) bool {
	values := r.Header.Values("Origin")
	return len(values) == 1 && values[0] == expected
}

func hasExactSubprotocol(value, expected string) bool {
	parts := strings.Split(value, ",")
	return len(parts) == 1 && parts[0] == expected
}

func writeAttachError(conn *websocket.Conn, message string, closeCode int) {
	writer := &socketWriter{conn: conn, deadline: 5 * time.Second}
	_ = writer.message(websocket.TextMessage, encodeError(message))
	_ = writer.control(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, message))
	_ = conn.Close()
}
