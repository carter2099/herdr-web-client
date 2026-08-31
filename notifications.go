package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

const (
	herdrSnapshotRequestID    = "herdr-web-client-snapshot"
	herdrCompletionsRequestID = "herdr-web-client-completions"
	maxHerdrMessageBytes      = 1 << 20
	maxHerdrPanes             = 4096
	maxHerdrFieldBytes        = 4096
)

var errHerdrMessageTooLarge = errors.New("herdr message exceeds size limit")
var errCompletionTrackerFull = errors.New("herdr completion tracker reached its pane limit")

// AgentCompletion is one background agent transition that Herdr considers done.
type AgentCompletion struct {
	Agent string
	Title string
}

// AgentCompletionSource streams semantic completion events from Herdr.
type AgentCompletionSource interface {
	Watch(context.Context, func(AgentCompletion) error) error
}

type boundedHerdrReader struct {
	source    io.Reader
	remaining int64
}

func (r *boundedHerdrReader) reset() {
	r.remaining = maxHerdrMessageBytes
}

func (r *boundedHerdrReader) Read(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var probe [1]byte
		count, err := r.source.Read(probe[:])
		if count > 0 {
			return 0, errHerdrMessageTooLarge
		}
		return 0, err
	}
	if int64(len(payload)) > r.remaining {
		payload = payload[:r.remaining]
	}
	count, err := r.source.Read(payload)
	r.remaining -= int64(count)
	return count, err
}

func newBoundedHerdrDecoder(source io.Reader) (*json.Decoder, *boundedHerdrReader) {
	reader := &boundedHerdrReader{source: source}
	reader.reset()
	return json.NewDecoder(reader), reader
}

func decodeHerdrMessage(decoder *json.Decoder, reader *boundedHerdrReader, value any) error {
	reader.reset()
	return decoder.Decode(value)
}

type herdrCompletionSource struct {
	socketPath string
}

type herdrAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type herdrPaneState struct {
	PaneID                string `json:"pane_id"`
	WorkspaceID           string `json:"workspace_id"`
	Agent                 string `json:"agent"`
	AgentStatus           string `json:"agent_status"`
	TerminalTitle         string `json:"terminal_title"`
	TerminalTitleStripped string `json:"terminal_title_stripped"`
}

type herdrSnapshotResponse struct {
	ID     string         `json:"id"`
	Error  *herdrAPIError `json:"error"`
	Result struct {
		Type     string `json:"type"`
		Snapshot struct {
			Panes []herdrPaneState `json:"panes"`
		} `json:"snapshot"`
	} `json:"result"`
}

type herdrSubscriptionResponse struct {
	ID     string         `json:"id"`
	Error  *herdrAPIError `json:"error"`
	Result struct {
		Type string `json:"type"`
	} `json:"result"`
}

type herdrPaneEvent struct {
	Event string `json:"event"`
	Data  struct {
		Type string         `json:"type"`
		Pane herdrPaneState `json:"pane"`
	} `json:"data"`
}

type completionTracker map[string]string

func newHerdrCompletionSource(socketPath string) AgentCompletionSource {
	return &herdrCompletionSource{socketPath: socketPath}
}

func newCompletionTracker(panes []herdrPaneState) completionTracker {
	capacity := len(panes)
	if capacity > maxHerdrPanes {
		capacity = maxHerdrPanes
	}
	tracker := make(completionTracker, capacity)
	for _, pane := range panes {
		if pane.PaneID != "" && len(tracker) < maxHerdrPanes {
			tracker[pane.PaneID] = pane.AgentStatus
		}
	}
	return tracker
}

func validHerdrPane(pane herdrPaneState) bool {
	return len(pane.PaneID) <= maxHerdrFieldBytes &&
		len(pane.WorkspaceID) <= maxHerdrFieldBytes &&
		len(pane.Agent) <= maxHerdrFieldBytes &&
		len(pane.AgentStatus) <= maxHerdrFieldBytes &&
		len(pane.TerminalTitle) <= maxHerdrFieldBytes &&
		len(pane.TerminalTitleStripped) <= maxHerdrFieldBytes
}

func validateHerdrPanes(panes []herdrPaneState) error {
	if len(panes) > maxHerdrPanes {
		return fmt.Errorf("herdr snapshot contains %d panes; limit is %d", len(panes), maxHerdrPanes)
	}
	for _, pane := range panes {
		if !validHerdrPane(pane) {
			return errors.New("herdr pane contains an oversized field")
		}
	}
	return nil
}

func validHerdrAPIError(value *herdrAPIError) bool {
	return value == nil || (len(value.Code) <= maxHerdrFieldBytes && len(value.Message) <= maxHerdrFieldBytes)
}

func (t completionTracker) observe(pane herdrPaneState) (AgentCompletion, bool, error) {
	if !validHerdrPane(pane) {
		return AgentCompletion{}, false, errors.New("herdr pane contains an oversized field")
	}
	if pane.PaneID == "" || pane.AgentStatus == "" {
		return AgentCompletion{}, false, nil
	}
	previous, known := t[pane.PaneID]
	if !known && len(t) >= maxHerdrPanes {
		return AgentCompletion{}, false, errCompletionTrackerFull
	}
	t[pane.PaneID] = pane.AgentStatus
	if pane.AgentStatus != "done" || (known && previous == "done") {
		return AgentCompletion{}, false, nil
	}
	title := strings.TrimSpace(pane.TerminalTitleStripped)
	if title == "" {
		title = strings.TrimSpace(pane.TerminalTitle)
	}
	return AgentCompletion{Agent: strings.TrimSpace(pane.Agent), Title: title}, true, nil
}

func (s *herdrCompletionSource) Watch(ctx context.Context, emit func(AgentCompletion) error) error {
	if s == nil || strings.TrimSpace(s.socketPath) == "" {
		return errors.New("herdr socket path is required")
	}
	if emit == nil {
		return errors.New("completion sink is required")
	}

	panes, err := s.snapshot(ctx)
	if err != nil {
		return err
	}
	if err := validateHerdrPanes(panes); err != nil {
		return err
	}
	tracker := newCompletionTracker(panes)

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("connect to Herdr event socket: %w", err)
	}
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer func() {
		stopClose()
		_ = conn.Close()
	}()

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(map[string]any{
		"id":     herdrCompletionsRequestID,
		"method": "events.subscribe",
		"params": map[string]any{
			"subscriptions": []map[string]string{{"type": "pane.updated"}},
		},
	}); err != nil {
		return fmt.Errorf("subscribe to Herdr pane events: %w", err)
	}

	decoder, reader := newBoundedHerdrDecoder(conn)
	var started herdrSubscriptionResponse
	if err := decodeHerdrMessage(decoder, reader, &started); err != nil {
		return completionReadError(ctx, "read Herdr subscription response", err)
	}
	if !validHerdrAPIError(started.Error) {
		return errors.New("herdr completion subscription error is oversized")
	}
	if started.Error != nil {
		return fmt.Errorf("herdr rejected completion subscription: %s: %s", started.Error.Code, started.Error.Message)
	}
	if started.ID != herdrCompletionsRequestID || started.Result.Type != "subscription_started" {
		return errors.New("herdr returned an invalid completion subscription response")
	}

	latestPanes, err := s.snapshot(ctx)
	if err != nil {
		return fmt.Errorf("reconcile Herdr pane snapshot: %w", err)
	}
	if err := validateHerdrPanes(latestPanes); err != nil {
		return fmt.Errorf("reconcile Herdr pane snapshot: %w", err)
	}
	for _, pane := range latestPanes {
		completion, completed, err := tracker.observe(pane)
		if err != nil {
			return fmt.Errorf("track reconciled Herdr pane: %w", err)
		}
		if completed {
			if err := emit(completion); err != nil {
				return err
			}
		}
	}

	for {
		var event herdrPaneEvent
		if err := decodeHerdrMessage(decoder, reader, &event); err != nil {
			return completionReadError(ctx, "read Herdr pane event", err)
		}
		if event.Event != "pane_updated" || event.Data.Type != "pane_updated" {
			continue
		}
		if !validHerdrPane(event.Data.Pane) {
			return errors.New("herdr pane event contains an oversized field")
		}
		completion, completed, err := tracker.observe(event.Data.Pane)
		if err != nil {
			return fmt.Errorf("track Herdr pane event: %w", err)
		}
		if completed {
			if err := emit(completion); err != nil {
				return err
			}
		}
	}
}

func (s *herdrCompletionSource) snapshot(ctx context.Context) ([]herdrPaneState, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", s.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to Herdr snapshot socket: %w", err)
	}
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer func() {
		stopClose()
		_ = conn.Close()
	}()

	if err := json.NewEncoder(conn).Encode(map[string]any{
		"id":     herdrSnapshotRequestID,
		"method": "session.snapshot",
		"params": map[string]any{},
	}); err != nil {
		return nil, fmt.Errorf("request Herdr session snapshot: %w", err)
	}

	decoder, reader := newBoundedHerdrDecoder(conn)
	var response herdrSnapshotResponse
	if err := decodeHerdrMessage(decoder, reader, &response); err != nil {
		return nil, completionReadError(ctx, "read Herdr session snapshot", err)
	}
	if !validHerdrAPIError(response.Error) {
		return nil, errors.New("herdr snapshot error is oversized")
	}
	if response.Error != nil {
		return nil, fmt.Errorf("herdr rejected session snapshot: %s: %s", response.Error.Code, response.Error.Message)
	}
	if response.ID != herdrSnapshotRequestID || response.Result.Type != "session_snapshot" {
		return nil, errors.New("herdr returned an invalid session snapshot")
	}
	if err := validateHerdrPanes(response.Result.Snapshot.Panes); err != nil {
		return nil, err
	}
	return response.Result.Snapshot.Panes, nil
}

func completionReadError(ctx context.Context, action string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: stream closed", action)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func forwardAgentCompletions(ctx context.Context, source AgentCompletionSource, emit func(AgentCompletion) error) {
	if source == nil {
		return
	}
	delay := time.Second
	for {
		started := time.Now()
		err := source.Watch(ctx, emit)
		if ctx.Err() != nil {
			return
		}
		log.Printf("Herdr completion stream unavailable: %v", err)
		if time.Since(started) >= 30*time.Second {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 15*time.Second {
			delay *= 2
			if delay > 15*time.Second {
				delay = 15 * time.Second
			}
		}
	}
}
