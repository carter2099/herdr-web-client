package main

import (
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type coreTestLauncher struct {
	starts atomic.Int32
}

func (l *coreTestLauncher) Start(context.Context, int, int) (PTYSession, error) {
	l.starts.Add(1)
	return nil, errors.New("unexpected PTY start")
}

func TestSessionRequiresExactHostAndRequestMarker(t *testing.T) {
	cfg := validTestConfig()
	server, err := NewServer(cfg, &coreTestLauncher{}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	wrongHost := httptest.NewRequest(http.MethodGet, cfg.PublicOrigin+"/api/session", nil)
	wrongHost.Host = "evil.example"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, wrongHost)
	if response.Code != http.StatusNotFound {
		t.Fatalf("wrong Host status = %d, want 404", response.Code)
	}

	missingRequestMarker := httptest.NewRequest(http.MethodGet, cfg.PublicOrigin+"/api/session", nil)
	missingRequestMarker.Host = cfg.publicHost()
	response = httptest.NewRecorder()
	server.ServeHTTP(response, missingRequestMarker)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing request marker status = %d, want 403", response.Code)
	}
}

func TestResponsesSetBrowserSecurityHeaders(t *testing.T) {
	cfg := validTestConfig()
	server, err := NewServer(cfg, &coreTestLauncher{}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, cfg.PublicOrigin+"/", nil)
	request.Host = cfg.publicHost()

	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("static response status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), `href="/favicon.png"`) {
		t.Fatal("static response does not reference the PNG favicon")
	}
	for header, expected := range map[string]string{
		"Cache-Control":                "no-store",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           "camera=(), clipboard-write=(self), display-capture=(), geolocation=(), microphone=(), payment=(), usb=()",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	} {
		if got := response.Header().Get(header); got != expected {
			t.Errorf("%s = %q, want %q", header, got, expected)
		}
	}
	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") ||
		!strings.Contains(csp, "script-src 'self'; style-src 'self' 'unsafe-inline'") ||
		!strings.Contains(csp, "img-src 'self'") ||
		!strings.Contains(csp, "connect-src 'self' wss://"+cfg.publicHost()) ||
		!strings.Contains(csp, "frame-ancestors 'none'") ||
		strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("unexpected Content-Security-Policy: %q", csp)
	}
}

func TestCircularFaviconIsServed(t *testing.T) {
	cfg := validTestConfig()
	server, err := NewServer(cfg, &coreTestLauncher{}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, cfg.PublicOrigin+"/favicon.png", nil)
	request.Host = cfg.publicHost()

	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("favicon response status = %d, want 200", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("favicon Content-Type = %q, want image/png", contentType)
	}
	favicon, err := png.Decode(response.Body)
	if err != nil {
		t.Fatalf("decode favicon: %v", err)
	}
	bounds := favicon.Bounds()
	if bounds.Dx() != 512 || bounds.Dy() != 512 {
		t.Fatalf("favicon bounds = %v, want 512x512", bounds)
	}
	_, _, _, cornerAlpha := favicon.At(bounds.Min.X, bounds.Min.Y).RGBA()
	_, _, _, centerAlpha := favicon.At(bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2).RGBA()
	if cornerAlpha != 0 || centerAlpha != 0xffff {
		t.Fatalf("favicon alpha corner=%d center=%d, want transparent corner and opaque center", cornerAlpha, centerAlpha)
	}
}

func TestSessionNonceIsSingleUse(t *testing.T) {
	cfg := validTestConfig()
	server, err := NewServer(cfg, &coreTestLauncher{}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, cfg.PublicOrigin+"/api/session", nil)
	request.Host = cfg.publicHost()
	request.Header.Set(sessionRequestHeader, sessionRequestValue)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("session status = %d", response.Code)
	}
	var payload struct {
		Nonce  string    `json:"nonce"`
		Expiry time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if payload.Nonce == "" || !payload.Expiry.After(time.Now()) {
		t.Fatalf("unexpected session payload: %+v", payload)
	}
	if !server.nonces.consume(payload.Nonce, time.Now()) {
		t.Fatal("nonce was not accepted")
	}
	if server.nonces.consume(payload.Nonce, time.Now()) {
		t.Fatal("nonce was accepted more than once")
	}
}

func TestSessionNonceCapacityIsBounded(t *testing.T) {
	cfg := validTestConfig()
	server, err := NewServer(cfg, &coreTestLauncher{}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	for attempt := 0; attempt <= maxOutstandingNonces; attempt++ {
		request := httptest.NewRequest(http.MethodGet, cfg.PublicOrigin+"/api/session", nil)
		request.Host = cfg.publicHost()
		request.Header.Set(sessionRequestHeader, sessionRequestValue)

		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)

		want := http.StatusOK
		if attempt == maxOutstandingNonces {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.Code, want)
		}
	}
	if got := len(server.nonces.items); got != maxOutstandingNonces {
		t.Fatalf("stored nonce count = %d, want %d", got, maxOutstandingNonces)
	}
}

func TestAttachRejectsBinaryBeforeHelloWithoutSpawning(t *testing.T) {
	testServer := httptest.NewUnstartedServer(nil)
	cfg := validTestConfig()
	cfg.PublicOrigin = "https://" + testServer.Listener.Addr().String()
	launcher := &coreTestLauncher{}
	server, err := NewServer(cfg, launcher, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	testServer.Config.Handler = server
	testServer.Start()
	defer testServer.Close()

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("session request: %v", err)
	}
	request.Host = cfg.publicHost()

	request.Header.Set(sessionRequestHeader, sessionRequestValue)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("session request: %v", err)
	}
	defer response.Body.Close()
	var session struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	dialer := websocket.Dialer{Subprotocols: []string{webSocketSubprotocol}}
	header := http.Header{}
	header.Set("Origin", cfg.PublicOrigin)

	wsURL := strings.Replace(testServer.URL, "http://", "ws://", 1) + "/api/attach"
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("WebSocket dial: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("input before hello")); err != nil {
		t.Fatalf("write pre-hello input: %v", err)
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read protocol error: %v", err)
	}
	if messageType != websocket.TextMessage || !strings.Contains(string(payload), `"type":"error"`) {
		t.Fatalf("protocol error message = type %d %q", messageType, payload)
	}
	if launcher.starts.Load() != 0 {
		t.Fatalf("launcher started %d times before valid hello", launcher.starts.Load())
	}
}

func TestAttachTimesOutBeforeHelloWithoutSpawning(t *testing.T) {
	testServer := httptest.NewUnstartedServer(nil)
	cfg := validTestConfig()
	cfg.PublicOrigin = "https://" + testServer.Listener.Addr().String()
	cfg.HelloTimeout = 25 * time.Millisecond
	launcher := &coreTestLauncher{}
	server, err := NewServer(cfg, launcher, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	testServer.Config.Handler = server
	testServer.Start()
	defer testServer.Close()

	dialer := websocket.Dialer{Subprotocols: []string{webSocketSubprotocol}}
	header := http.Header{}
	header.Set("Origin", cfg.PublicOrigin)

	wsURL := strings.Replace(testServer.URL, "http://", "ws://", 1) + "/api/attach"
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("WebSocket dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read timeout response: %v", err)
	}
	if messageType != websocket.TextMessage || !strings.Contains(string(payload), errExpectedHello.Error()) {
		t.Fatalf("timeout response = type %d %q", messageType, payload)
	}
	if launcher.starts.Load() != 0 {
		t.Fatalf("launcher started %d times without a hello", launcher.starts.Load())
	}
}

func TestPendingAttachBoundsConcurrentSockets(t *testing.T) {
	testServer := httptest.NewUnstartedServer(nil)
	cfg := validTestConfig()
	cfg.PublicOrigin = "https://" + testServer.Listener.Addr().String()
	launcher := &coreTestLauncher{}
	server, err := NewServer(cfg, launcher, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	testServer.Config.Handler = server
	testServer.Start()
	defer testServer.Close()

	dialer := websocket.Dialer{Subprotocols: []string{webSocketSubprotocol}}
	header := http.Header{}
	header.Set("Origin", cfg.PublicOrigin)

	wsURL := strings.Replace(testServer.URL, "http://", "ws://", 1) + "/api/attach"
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("pending WebSocket dial: %v", err)
	}
	defer conn.Close()

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("second session request: %v", err)
	}
	request.Host = cfg.publicHost()

	request.Header.Set(sessionRequestHeader, sessionRequestValue)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("second session request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("second session status = %d, want 409", response.StatusCode)
	}
	if launcher.starts.Load() != 0 {
		t.Fatalf("launcher started %d times before a hello", launcher.starts.Load())
	}
}
