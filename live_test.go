//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestLiveHerdrWebSocketAttach(t *testing.T) {
	if os.Getenv("HERDR_WEB_CLIENT_LIVE") != "1" {
		t.Skip("set HERDR_WEB_CLIENT_LIVE=1 to attach to the live local Herdr server")
	}

	testServer := httptest.NewUnstartedServer(nil)
	cfg := validTestConfig()
	cfg.PublicOrigin = "https://" + testServer.Listener.Addr().String()
	application, err := NewServer(cfg, NewPTYLauncher(cfg.HerdrPath, cfg.HerdrWorkdir), nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer application.Close()
	testServer.Config.Handler = application
	testServer.Start()
	defer testServer.Close()

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("create session request: %v", err)
	}
	request.Host = cfg.publicHost()

	request.Header.Set(sessionRequestHeader, sessionRequestValue)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request session: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, want 200", response.StatusCode)
	}
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
	connection, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial attachment: %v", err)
	}
	defer connection.Close()
	if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if err := connection.WriteJSON(map[string]any{
		"type":  "hello",
		"nonce": session.Nonce,
		"cols":  80,
		"rows":  24,
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	messageType, payload, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}
	if messageType != websocket.TextMessage || string(payload) != `{"type":"ready"}` {
		t.Fatalf("ready frame = type %d %q", messageType, payload)
	}

	for {
		messageType, payload, err = connection.ReadMessage()
		if err != nil {
			t.Fatalf("read terminal output: %v", err)
		}
		if messageType == websocket.BinaryMessage && len(payload) > 0 {
			break
		}
	}
	if err := connection.WriteJSON(map[string]any{"type": "resize", "cols": 100, "rows": 30}); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, []byte{0x00, 'q'}); err != nil {
		t.Fatalf("write detach shortcut: %v", err)
	}

	for {
		messageType, payload, err = connection.ReadMessage()
		if err != nil {
			t.Fatalf("read detach result: %v", err)
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var control struct {
			Type string `json:"type"`
			Code int    `json:"code"`
		}
		if err := json.Unmarshal(payload, &control); err != nil {
			t.Fatalf("decode detach control: %v", err)
		}
		if control.Type == "exit" {
			if control.Code != 0 {
				t.Fatalf("Herdr client exit code = %d, want 0", control.Code)
			}
			break
		}
	}
}
