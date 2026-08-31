package main

import (
	"errors"
	"testing"
)

func TestDimensionsNormalized(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    Dimensions
		expected Dimensions
	}{
		{name: "low", input: Dimensions{Cols: 1, Rows: 1}, expected: Dimensions{Cols: minColumns, Rows: minRows}},
		{name: "high", input: Dimensions{Cols: 999, Rows: 999}, expected: Dimensions{Cols: maxColumns, Rows: maxRows}},
		{name: "inside", input: Dimensions{Cols: 80, Rows: 24}, expected: Dimensions{Cols: 80, Rows: 24}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.input.normalized(); got != test.expected {
				t.Fatalf("normalized dimensions = %+v, want %+v", got, test.expected)
			}
		})
	}
}

func TestCanonicalWireNames(t *testing.T) {
	if webSocketSubprotocol != "herdr-web-client.v1" {
		t.Fatalf("WebSocket subprotocol = %q", webSocketSubprotocol)
	}
	if sessionRequestHeader != "X-Herdr-Web-Client-Request" || sessionRequestValue != "session" {
		t.Fatalf("session marker = %q: %q", sessionRequestHeader, sessionRequestValue)
	}
	if herdrSnapshotRequestID != "herdr-web-client-snapshot" || herdrCompletionsRequestID != "herdr-web-client-completions" {
		t.Fatalf("Herdr request IDs = %q, %q", herdrSnapshotRequestID, herdrCompletionsRequestID)
	}

}

func TestDecodeHelloRejectsUnknownAndMissingFields(t *testing.T) {
	if _, _, err := decodeHello([]byte(`{"type":"hello","nonce":"n","cols":80,"rows":24,"extra":true}`)); err == nil {
		t.Fatal("decodeHello accepted an unknown field")
	}
	if _, _, err := decodeHello([]byte(`{"type":"resize","nonce":"n","cols":80,"rows":24}`)); !errors.Is(err, errInvalidHello) {
		t.Fatalf("decodeHello error = %v, want invalid hello", err)
	}
}

func TestDecodeMessagesRejectDuplicateJSONKeys(t *testing.T) {
	if _, _, err := decodeHello([]byte(`{"type":"hello","nonce":"n","cols":80,"rows":24,"cols":81}`)); err == nil {
		t.Fatal("decodeHello accepted duplicate fields")
	}
	if _, err := decodeResize([]byte(`{"type":"resize","cols":80,"rows":24,"meta":{"x":1,"x":2}}`)); err == nil {
		t.Fatal("decodeResize accepted nested duplicate fields")
	}
}

func TestDecodeResizeClampsDimensions(t *testing.T) {
	got, err := decodeResize([]byte(`{"type":"resize","cols":1,"rows":999}`))
	if err != nil {
		t.Fatalf("decodeResize: %v", err)
	}
	if got != (Dimensions{Cols: minColumns, Rows: maxRows}) {
		t.Fatalf("resize dimensions = %+v", got)
	}
	if _, err := decodeResize([]byte(`{"type":"hello","cols":80,"rows":24}`)); err == nil {
		t.Fatal("decodeResize accepted hello")
	}
}

func TestEncodeAgentDone(t *testing.T) {
	got := string(encodeAgentDone(AgentCompletion{Agent: "omp", Title: "Review ready"}))
	want := `{"type":"agent-done","agent":"omp","title":"Review ready"}`
	if got != want {
		t.Fatalf("agent done message = %q, want %q", got, want)
	}
}
