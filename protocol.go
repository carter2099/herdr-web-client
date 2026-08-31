package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	webSocketSubprotocol = "herdr-web-client.v1"
	minColumns           = 20
	maxColumns           = 400
	minRows              = 5
	maxRows              = 200
)

var (
	errExpectedHello     = errors.New("expected hello message")
	errInvalidHello      = errors.New("invalid hello message")
	errInvalidResize     = errors.New("invalid resize message")
	errBinaryBeforeHello = errors.New("binary input is not allowed before hello")
	errTextAfterHello    = errors.New("unexpected text message")
)

// Dimensions is the bounded terminal size exchanged by the protocol and
// passed to the PTY. Values are normalized before they reach a process.
type Dimensions struct {
	Cols int
	Rows int
}

func (d Dimensions) normalized() Dimensions {
	if d.Cols < minColumns {
		d.Cols = minColumns
	} else if d.Cols > maxColumns {
		d.Cols = maxColumns
	}
	if d.Rows < minRows {
		d.Rows = minRows
	} else if d.Rows > maxRows {
		d.Rows = maxRows
	}
	return d
}

type helloMessage struct {
	Type  string `json:"type"`
	Nonce string `json:"nonce"`
	Cols  *int   `json:"cols"`
	Rows  *int   `json:"rows"`
}

type resizeMessage struct {
	Type string `json:"type"`
	Cols *int   `json:"cols"`
	Rows *int   `json:"rows"`
}

type serverReadyMessage struct {
	Type string `json:"type"`
}

type serverExitMessage struct {
	Type string `json:"type"`
	Code int    `json:"code"`
}

type serverErrorMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type serverAgentDoneMessage struct {
	Type  string `json:"type"`
	Agent string `json:"agent,omitempty"`
	Title string `json:"title,omitempty"`
}

func decodeJSONMessage(payload []byte, dst any) error {
	duplicateDecoder := json.NewDecoder(bytes.NewReader(payload))
	if err := rejectDuplicateJSONKeys(duplicateDecoder); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectDuplicateJSONKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := walkJSONValue(decoder, token); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, token json.Token) error {
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			keys[key] = struct{}{}
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("JSON object did not terminate")
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := walkJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("JSON array did not terminate")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func decodeHello(payload []byte) (nonce string, dimensions Dimensions, err error) {
	var message helloMessage
	if err := decodeJSONMessage(payload, &message); err != nil {
		return "", Dimensions{}, fmt.Errorf("%w: %v", errInvalidHello, err)
	}
	if message.Type != "hello" || message.Nonce == "" || message.Cols == nil || message.Rows == nil {
		return "", Dimensions{}, errInvalidHello
	}
	return message.Nonce, (Dimensions{Cols: *message.Cols, Rows: *message.Rows}).normalized(), nil
}

func decodeResize(payload []byte) (Dimensions, error) {
	var message resizeMessage
	if err := decodeJSONMessage(payload, &message); err != nil {
		return Dimensions{}, fmt.Errorf("%w: %v", errInvalidResize, err)
	}
	if message.Type != "resize" || message.Cols == nil || message.Rows == nil {
		return Dimensions{}, errInvalidResize
	}
	return (Dimensions{Cols: *message.Cols, Rows: *message.Rows}).normalized(), nil
}

func encodeReady() []byte {
	return mustMarshal(serverReadyMessage{Type: "ready"})
}

func encodeExit(code int) []byte {
	return mustMarshal(serverExitMessage{Type: "exit", Code: code})
}

func encodeError(message string) []byte {
	return mustMarshal(serverErrorMessage{Type: "error", Message: message})
}

func encodeAgentDone(completion AgentCompletion) []byte {
	return mustMarshal(serverAgentDoneMessage{
		Type:  "agent-done",
		Agent: completion.Agent,
		Title: completion.Title,
	})
}

func mustMarshal(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
