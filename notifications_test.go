package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func observeCompletion(t *testing.T, tracker completionTracker, pane herdrPaneState) (AgentCompletion, bool) {
	t.Helper()
	completion, emitted, err := tracker.observe(pane)
	if err != nil {
		t.Fatalf("observe pane: %v", err)
	}
	return completion, emitted
}

func TestCompletionTrackerEmitsOnlyNewDoneTransitions(t *testing.T) {
	tracker := newCompletionTracker([]herdrPaneState{
		{PaneID: "working", AgentStatus: "working"},
		{PaneID: "already-done", AgentStatus: "done"},
	})

	completion, ok := observeCompletion(t, tracker, herdrPaneState{
		PaneID:                "working",
		Agent:                 "omp",
		AgentStatus:           "done",
		TerminalTitle:         "fallback title",
		TerminalTitleStripped: "Review ready",
	})
	if !ok {
		t.Fatal("working-to-done transition did not emit a completion")
	}
	if completion != (AgentCompletion{Agent: "omp", Title: "Review ready"}) {
		t.Fatalf("completion = %+v", completion)
	}

	if _, ok := observeCompletion(t, tracker, herdrPaneState{PaneID: "working", AgentStatus: "done"}); ok {
		t.Fatal("repeated done update emitted a duplicate completion")
	}
	if _, ok := observeCompletion(t, tracker, herdrPaneState{PaneID: "already-done", AgentStatus: "done"}); ok {
		t.Fatal("snapshot-seeded done pane emitted a completion")
	}
	if _, ok := observeCompletion(t, tracker, herdrPaneState{PaneID: "working", AgentStatus: "working"}); ok {
		t.Fatal("done-to-working transition emitted a completion")
	}
	if _, ok := observeCompletion(t, tracker, herdrPaneState{PaneID: "working", AgentStatus: "done"}); !ok {
		t.Fatal("second working-to-done transition did not emit")
	}

	completion, ok = observeCompletion(t, tracker, herdrPaneState{
		PaneID:        "new-pane",
		AgentStatus:   "done",
		TerminalTitle: "New pane finished",
	})
	if !ok || completion.Title != "New pane finished" {
		t.Fatalf("new done pane completion = %+v, emitted=%v", completion, ok)
	}
}

func TestCompletionTrackerFailsClosedAtStateAndFieldBounds(t *testing.T) {
	tracker := make(completionTracker, maxHerdrPanes)
	for index := range maxHerdrPanes {
		tracker[strconv.Itoa(index)] = "working"
	}
	if _, emitted, err := tracker.observe(herdrPaneState{PaneID: "overflow", AgentStatus: "done"}); emitted || !errors.Is(err, errCompletionTrackerFull) {
		t.Fatalf("overflow observation = (emitted %v, error %v), want pane-limit error", emitted, err)
	}
	if len(tracker) != maxHerdrPanes {
		t.Fatalf("tracker size = %d, want %d", len(tracker), maxHerdrPanes)
	}
	if _, emitted, err := tracker.observe(herdrPaneState{
		PaneID:      strings.Repeat("x", maxHerdrFieldBytes+1),
		AgentStatus: "done",
	}); emitted || err == nil {
		t.Fatalf("oversized observation = (emitted %v, error %v), want validation error", emitted, err)
	}
}

func TestHerdrMessageDecoderBoundsInput(t *testing.T) {
	payload := `{"value":"` + strings.Repeat("x", maxHerdrMessageBytes) + `"}`
	decoder, reader := newBoundedHerdrDecoder(strings.NewReader(payload))
	var value map[string]string
	err := decodeHerdrMessage(decoder, reader, &value)
	if !errors.Is(err, errHerdrMessageTooLarge) {
		t.Fatalf("decode error = %v, want %v", err, errHerdrMessageTooLarge)
	}
}

func TestHerdrSnapshotBoundsPaneCount(t *testing.T) {
	panes := make([]herdrPaneState, maxHerdrPanes+1)
	if err := validateHerdrPanes(panes); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("validateHerdrPanes() error = %v, want pane limit error", err)
	}
}

func TestHerdrCompletionSourceRetainsTransitionsDuringSnapshot(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on fake Herdr socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverErrors := make(chan error, 1)
	go func() {
		initialConn, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer initialConn.Close()
		var initialRequest map[string]any
		if err := json.NewDecoder(initialConn).Decode(&initialRequest); err != nil {
			serverErrors <- err
			return
		}
		if err := json.NewEncoder(initialConn).Encode(map[string]any{
			"id": initialRequest["id"],
			"result": map[string]any{
				"type": "session_snapshot",
				"snapshot": map[string]any{"panes": []map[string]string{{
					"pane_id": "w1:p1", "agent_status": "working",
				}}},
			},
		}); err != nil {
			serverErrors <- err
			return
		}
		eventConn, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer eventConn.Close()
		var subscriptionRequest map[string]any
		if err := json.NewDecoder(eventConn).Decode(&subscriptionRequest); err != nil {
			serverErrors <- err
			return
		}
		if subscriptionRequest["method"] != "events.subscribe" {
			serverErrors <- errors.New("expected subscription before reconciliation")
			return
		}
		encoder := json.NewEncoder(eventConn)
		if err := encoder.Encode(map[string]any{
			"id":     herdrCompletionsRequestID,
			"result": map[string]string{"type": "subscription_started"},
		}); err != nil {
			serverErrors <- err
			return
		}

		snapshotConn, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer snapshotConn.Close()
		var snapshotRequest map[string]any
		if err := json.NewDecoder(snapshotConn).Decode(&snapshotRequest); err != nil {
			serverErrors <- err
			return
		}
		// A whole completion occurs while the snapshot is in flight. Its final
		// working state cannot reveal the intervening done transition.
		for _, status := range []string{"done", "working"} {
			if err := encoder.Encode(map[string]any{
				"event": "pane.agent_status_changed",
				"data": map[string]string{
					"pane_id":      "w1:p1",
					"agent":        "omp",
					"agent_status": status,
					"title":        "Review ready",
				},
			}); err != nil {
				serverErrors <- err
				return
			}
		}
		serverErrors <- json.NewEncoder(snapshotConn).Encode(map[string]any{
			"id": herdrSnapshotRequestID,
			"result": map[string]any{
				"type": "session_snapshot",
				"snapshot": map[string]any{
					"panes": []map[string]string{{
						"pane_id":      "w1:p1",
						"agent":        "omp",
						"agent_status": "working",
					}},
				},
			},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stop := errors.New("completion received")
	var completion AgentCompletion
	source := &herdrCompletionSource{socketPath: socketPath}
	err = source.Watch(ctx, func(value AgentCompletion) error {
		completion = value
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("Watch error = %v, want completion sentinel", err)
	}
	if completion != (AgentCompletion{Agent: "omp", Title: "Review ready"}) {
		t.Fatalf("completion = %+v", completion)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("fixture server: %v", err)
	}
}

type retryingCompletionSource struct {
	calls chan struct{}
}

func (s *retryingCompletionSource) Watch(context.Context, func(AgentCompletion) error) error {
	s.calls <- struct{}{}
	return errors.New("fixture completion source unavailable")
}

func TestCompletionSourceRetriesAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &retryingCompletionSource{calls: make(chan struct{}, 2)}
	done := make(chan struct{})
	go func() {
		forwardAgentCompletions(ctx, source, func(AgentCompletion) error { return nil })
		close(done)
	}()

	for attempt := 1; attempt <= 2; attempt++ {
		select {
		case <-source.calls:
		case <-time.After(3 * time.Second):
			t.Fatalf("completion source attempt %d did not occur", attempt)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion retry loop did not stop after cancellation")
	}
}

func TestHerdrSnapshotRejectsMalformedExternalResponses(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		response string
		want     string
	}{
		{name: "malformed JSON", response: "{", want: "read Herdr session snapshot"},
		{
			name:     "wrong response identity",
			response: `{"id":"wrong","result":{"type":"session_snapshot","snapshot":{"panes":[]}}}`,
			want:     "invalid session snapshot",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "herdr.sock")
			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatalf("listen on fixture socket: %v", err)
			}
			defer listener.Close()

			serverErrors := make(chan error, 1)
			go func() {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil {
					serverErrors <- acceptErr
					return
				}
				defer conn.Close()
				var request map[string]any
				if decodeErr := json.NewDecoder(conn).Decode(&request); decodeErr != nil {
					serverErrors <- decodeErr
					return
				}
				if request["method"] != "session.snapshot" {
					serverErrors <- errors.New("request was not session.snapshot")
					return
				}
				_, writeErr := io.WriteString(conn, testCase.response)
				serverErrors <- writeErr
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err = (&herdrCompletionSource{socketPath: socketPath}).snapshot(ctx)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("snapshot error = %v, want text %q", err, testCase.want)
			}
			if serverErr := <-serverErrors; serverErr != nil {
				t.Fatalf("fixture server: %v", serverErr)
			}
		})
	}
}

func TestHerdrSnapshotReportsUnavailableSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "missing.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := (&herdrCompletionSource{socketPath: socketPath}).snapshot(ctx)
	if err == nil || !strings.Contains(err.Error(), "connect to Herdr snapshot socket") {
		t.Fatalf("snapshot error = %v, want unavailable socket error", err)
	}
}
