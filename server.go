package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	sessionRequestHeader = "X-Herdr-Web-Client-Request"
	sessionRequestValue  = "session"
	maxOutstandingNonces = 32
)

var errNonceCapacity = errors.New("too many outstanding session nonces")

//go:embed web/dist
var webDist embed.FS

type nonceEntry struct {
	subject   string
	expiresAt time.Time
}

type nonceStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]nonceEntry
}

func newNonceStore(ttl time.Duration) *nonceStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &nonceStore{ttl: ttl, items: make(map[string]nonceEntry)}
}

func (n *nonceStore) issue(subject string, identityExpiry, now time.Time) (string, time.Time, error) {
	if n == nil || subject == "" {
		return "", time.Time{}, errors.New("cannot issue nonce without a subject")
	}
	deadline := now.Add(n.ttl)
	if !identityExpiry.IsZero() && identityExpiry.Before(deadline) {
		deadline = identityExpiry
	}
	if !deadline.After(now) {
		return "", time.Time{}, errors.New("identity is expired")
	}

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
	n.items[nonce] = nonceEntry{subject: subject, expiresAt: deadline}
	return nonce, deadline, nil
}

func (n *nonceStore) consume(nonce, subject string, now time.Time) bool {
	if n == nil || nonce == "" || subject == "" {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	entry, ok := n.items[nonce]
	if !ok {
		n.pruneLocked(now)
		return false
	}
	if !entry.expiresAt.After(now) {
		delete(n.items, nonce)
		return false
	}
	if entry.subject != subject {
		return false
	}
	delete(n.items, nonce)
	return true
}

func (n *nonceStore) pruneLocked(now time.Time) {
	for token, entry := range n.items {
		if !entry.expiresAt.After(now) {
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
}

type Server struct {
	cfg         Config
	auth        Authenticator
	launcher    Launcher
	completions AgentCompletionSource
	socketSlots chan struct{}
	nonces      *nonceStore
	assets      http.Handler

	ctx       context.Context
	cancel    context.CancelFunc
	activeMu  sync.Mutex
	active    *activeAttachment
	closeOnce sync.Once
}

// NewServer constructs the protected HTTP/WebSocket surface. It does not
// start listening; main owns the net/http lifecycle.
func NewServer(cfg Config, auth Authenticator, launcher Launcher, completions AgentCompletionSource) (*Server, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validateServer(); err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, errors.New("authenticator is required")
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
		cfg:         cfg,
		socketSlots: make(chan struct{}, 1),
		auth:        auth,
		launcher:    launcher,
		completions: completions,
		nonces:      newNonceStore(cfg.NonceTTL),
		assets:      http.FileServer(http.FS(assets)),
		ctx:         ctx,
		cancel:      cancel,
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
func (s *Server) claimActive(cancel context.CancelFunc) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active != nil {
		return false
	}
	s.active = &activeAttachment{cancel: cancel}
	return true
}

func (s *Server) releaseActive(_ context.CancelFunc) {
	s.activeMu.Lock()
	s.active = nil
	s.activeMu.Unlock()
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
	identity, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if s.socketBusy() {
		http.Error(w, "another attachment is already active", http.StatusConflict)
		return
	}
	now := time.Now()
	nonce, expiresAt, err := s.nonces.issue(identity.Subject, identity.ExpiresAt, now)
	if errors.Is(err, errNonceCapacity) {
		http.Error(w, "too many pending sessions", http.StatusTooManyRequests)
		return
	}
	if err != nil {
		http.Error(w, "unable to create session", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Email     string    `json:"email"`
		Nonce     string    `json:"nonce"`
		ExpiresAt time.Time `json:"expires_at"`
	}{Email: identity.Email, Nonce: nonce, ExpiresAt: expiresAt})
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	values := r.Header.Values(s.cfg.AssertionHeader)
	if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] || strings.Contains(values[0], ",") {
		unauthorized(w)
		return Identity{}, false
	}
	identity, err := s.auth.Authenticate(r.Context(), values[0])
	if err != nil || identity.Subject == "" || identity.ExpiresAt.IsZero() || !identity.ExpiresAt.After(time.Now()) {
		unauthorized(w)
		return Identity{}, false
	}
	return identity, true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
	if _, ok := s.authenticate(w, r); !ok {
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

	identity, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if !s.claimSocket() {
		http.Error(w, "another attachment is already active", http.StatusConflict)
		return
	}
	releaseSocket := true
	defer func() {
		if releaseSocket {
			s.releaseSocket()
		}
	}()
	quarantine := func(err error) {
		if err == nil {
			return
		}
		releaseSocket = false
		s.cancel()
		log.Printf("attachment teardown could not be confirmed; server quarantined: %v", err)
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
	if !s.nonces.consume(nonce, identity.Subject, time.Now()) {
		writeAttachError(conn, "invalid or expired session", websocket.ClosePolicyViolation)
		return
	}

	attachCtx, cancel := context.WithCancel(s.ctx)
	if !identity.ExpiresAt.IsZero() {
		var deadlineCancel context.CancelFunc
		attachCtx, deadlineCancel = context.WithDeadline(attachCtx, identity.ExpiresAt)
		oldCancel := cancel
		cancel = func() {
			deadlineCancel()
			oldCancel()
		}
	}
	if !s.claimActive(cancel) {
		cancel()
		writeAttachError(conn, "another attachment is already active", websocket.CloseTryAgainLater)
		return
	}
	defer s.releaseActive(cancel)
	defer cancel()

	if err := attachCtx.Err(); err != nil {
		writeAttachError(conn, "session expired", websocket.ClosePolicyViolation)
		return
	}
	session, err := s.launcher.Start(attachCtx, dimensions.Cols, dimensions.Rows)
	if err != nil {
		log.Printf("terminal startup failed: %v", err)
		if session != nil {
			closeErr := session.Close()
			_, waitErr := session.Wait()
			quarantine(errors.Join(closeErr, waitErr))
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
		quarantine(errors.Join(closeErr, waitErr))
		writeAttachError(conn, "session expired", websocket.ClosePolicyViolation)
		return
	}
	quarantine(runBridge(attachCtx, conn, session, s.completions, s.cfg))
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
