package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRefreshingDetachGrantProvidesFullLifetime(t *testing.T) {
	store := newDetachTokenStore(time.Second)
	active := &activeAttachment{}
	now := time.Unix(1_000, 0)
	if _, _, err := store.issue(active, now); err != nil {
		t.Fatal(err)
	}
	refreshed, _, err := store.issue(active, now.Add(750*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// The click's renewed grant remains valid past the original page's expiry.
	if _, ok := store.take(refreshed, now.Add(1500*time.Millisecond)); !ok {
		t.Fatal("refreshed grant expired on the original token's deadline")
	}
}

type detachTestSession struct {
	readClosed chan struct{}
	waitClosed chan struct{}
	closeOnce  sync.Once
	closeErr   error
	waitErr    error
}

func newDetachTestSession(closeErr, waitErr error) *detachTestSession {
	return &detachTestSession{
		readClosed: make(chan struct{}),
		waitClosed: make(chan struct{}),
		closeErr:   closeErr,
		waitErr:    waitErr,
	}
}

func (s *detachTestSession) Read([]byte) (int, error) {
	<-s.readClosed
	return 0, io.EOF
}

func (s *detachTestSession) Write(payload []byte) (int, error) { return len(payload), nil }

func (s *detachTestSession) Resize(int, int) error { return nil }

func (s *detachTestSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.readClosed)
		close(s.waitClosed)
	})
	return s.closeErr
}

func (s *detachTestSession) Wait() (int, error) {
	<-s.waitClosed
	return 0, s.waitErr
}

type detachTestLauncher struct {
	starts   atomic.Int32
	sessions []*detachTestSession
}

func (l *detachTestLauncher) Start(context.Context, int, int) (PTYSession, error) {
	index := int(l.starts.Add(1)) - 1
	if index >= len(l.sessions) {
		return nil, errors.New("unexpected PTY start")
	}
	return l.sessions[index], nil
}

func newDetachTestServer(t *testing.T, cfg Config, launcher Launcher) (*httptest.Server, Config, *Server) {
	t.Helper()
	testServer := httptest.NewUnstartedServer(nil)
	cfg.PublicOrigin = "https://" + testServer.Listener.Addr().String()
	server, err := NewServer(cfg, launcher, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	testServer.Config.Handler = server
	testServer.Start()
	t.Cleanup(func() {
		_ = server.Close()
		testServer.Close()
	})
	return testServer, cfg, server
}

func requestDetachSession(t *testing.T, testServer *httptest.Server, cfg Config) (int, string, time.Time) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("create session request: %v", err)
	}
	request.Host = cfg.publicHost()
	request.Header.Set(sessionRequestHeader, sessionRequestValue)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("session request: %v", err)
	}
	defer response.Body.Close()
	var payload struct {
		Nonce       string    `json:"nonce"`
		DetachNonce string    `json:"detach_nonce"`
		ExpiresAt   time.Time `json:"expires_at"`
		Error       string    `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	nonce := payload.Nonce
	if nonce == "" {
		nonce = payload.DetachNonce
	}
	return response.StatusCode, nonce, payload.ExpiresAt
}

func openDetachTestSocket(t *testing.T, testServer *httptest.Server, cfg Config, nonce string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{webSocketSubprotocol}}
	header := http.Header{}
	header.Set("Origin", cfg.PublicOrigin)
	wsURL := strings.Replace(testServer.URL, "http://", "ws://", 1) + "/api/attach"
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial attachment: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set WebSocket deadline: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "hello", "nonce": nonce, "cols": 80, "rows": 24}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}
	if messageType != websocket.TextMessage || string(payload) != `{"type":"ready"}` {
		t.Fatalf("ready message = type %d %q", messageType, payload)
	}
	return conn
}

func postDetach(t *testing.T, testServer *httptest.Server, cfg Config, nonce, origin string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, testServer.URL+"/api/detach", bytes.NewReader([]byte(`{"nonce":"`+nonce+`"}`)))
	if err != nil {
		t.Fatalf("create detach request: %v", err)
	}
	request.Host = cfg.publicHost()
	request.Header.Set("Origin", origin)
	request.Header.Set(sessionRequestHeader, sessionRequestValue)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("detach request: %v", err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func waitForDetachSession(t *testing.T, testServer *httptest.Server, cfg Config, want int) (string, time.Time) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, nonce, expiresAt := requestDetachSession(t, testServer, cfg)
		if status == want {
			return nonce, expiresAt
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session did not reach status %d", want)
	return "", time.Time{}
}

func TestDetachTeardownThenReattach(t *testing.T) {
	cfg := validTestConfig()
	cfg.WriteTimeout = 250 * time.Millisecond
	launcher := &detachTestLauncher{sessions: []*detachTestSession{
		newDetachTestSession(nil, nil),
		newDetachTestSession(nil, nil),
	}}
	testServer, cfg, _ := newDetachTestServer(t, cfg, launcher)
	status, nonce, _ := requestDetachSession(t, testServer, cfg)
	if status != http.StatusOK || nonce == "" {
		t.Fatalf("initial session status = %d, nonce %q", status, nonce)
	}
	conn := openDetachTestSocket(t, testServer, cfg, nonce)

	status, detachNonce, expiresAt := requestDetachSession(t, testServer, cfg)
	if status != http.StatusConflict || detachNonce == "" || !expiresAt.After(time.Now()) {
		t.Fatalf("busy session = status %d, nonce %q, expiry %v", status, detachNonce, expiresAt)
	}
	result := make(chan int, 1)
	go func() { result <- postDetach(t, testServer, cfg, detachNonce, cfg.PublicOrigin) }()
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read detached control: %v", err)
	}
	if messageType != websocket.TextMessage || string(payload) != `{"type":"detached"}` {
		t.Fatalf("detached control = type %d %q", messageType, payload)
	}
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseNormalClosure || closeErr.Text != "attachment detached" {
		t.Fatalf("detached close = %v, want normal attachment detached", err)
	}
	if status := <-result; status != http.StatusNoContent {
		t.Fatalf("detach status = %d, want 204", status)
	}

	nonce, _ = waitForDetachSession(t, testServer, cfg, http.StatusOK)
	_ = openDetachTestSocket(t, testServer, cfg, nonce)
	if launcher.starts.Load() != 2 {
		t.Fatalf("launcher starts = %d, want 2", launcher.starts.Load())
	}
}

func TestDetachRejectsCrossOriginStaleAndReplayTokens(t *testing.T) {
	cfg := validTestConfig()
	cfg.WriteTimeout = 250 * time.Millisecond
	launcher := &detachTestLauncher{sessions: []*detachTestSession{
		newDetachTestSession(nil, nil),
		newDetachTestSession(nil, nil),
	}}
	testServer, cfg, _ := newDetachTestServer(t, cfg, launcher)
	status, initialNonce, _ := requestDetachSession(t, testServer, cfg)
	if status != http.StatusOK || initialNonce == "" {
		t.Fatalf("initial session status = %d, nonce %q", status, initialNonce)
	}
	conn := openDetachTestSocket(t, testServer, cfg, initialNonce)
	status, nonce, _ := requestDetachSession(t, testServer, cfg)
	if status != http.StatusConflict || nonce == "" {
		t.Fatal("failed to obtain a detach token")
	}
	if status := postDetach(t, testServer, cfg, nonce, "https://evil.example"); status != http.StatusForbidden {
		t.Fatalf("cross-origin detach status = %d, want 403", status)
	}
	_ = conn.Close()
	secondNonce, _ := waitForDetachSession(t, testServer, cfg, http.StatusOK)
	_ = openDetachTestSocket(t, testServer, cfg, secondNonce)
	if status := postDetach(t, testServer, cfg, nonce, cfg.PublicOrigin); status != http.StatusConflict {
		t.Fatalf("stale detach status = %d, want 409", status)
	}
	if status := postDetach(t, testServer, cfg, nonce, cfg.PublicOrigin); status != http.StatusForbidden {
		t.Fatalf("replayed detach status = %d, want 403", status)
	}
}

func TestDetachFailurePreservesQuarantine(t *testing.T) {
	cfg := validTestConfig()
	cfg.WriteTimeout = 250 * time.Millisecond
	launcher := &detachTestLauncher{sessions: []*detachTestSession{
		newDetachTestSession(errors.New("close failed"), nil),
	}}
	testServer, cfg, _ := newDetachTestServer(t, cfg, launcher)
	request, _ := http.NewRequest(http.MethodGet, testServer.URL+"/api/session", nil)
	request.Host = cfg.publicHost()
	request.Header.Set(sessionRequestHeader, sessionRequestValue)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("session request: %v", err)
	}
	var session struct {
		Nonce string `json:"nonce"`
	}
	_ = json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	conn := openDetachTestSocket(t, testServer, cfg, session.Nonce)
	status, detachNonce, _ := requestDetachSession(t, testServer, cfg)
	if status != http.StatusConflict || detachNonce == "" {
		t.Fatal("failed to obtain quarantine detach token")
	}
	if status := postDetach(t, testServer, cfg, detachNonce, cfg.PublicOrigin); status != http.StatusServiceUnavailable {
		t.Fatalf("failed detach status = %d, want 503", status)
	}
	status, detachNonce, _ = requestDetachSession(t, testServer, cfg)
	if status != http.StatusConflict || detachNonce != "" {
		t.Fatalf("quarantined session = status %d, token %q", status, detachNonce)
	}
	_ = conn.Close()
}
