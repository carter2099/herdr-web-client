package main

import "testing"

type protocolCorpusCase struct {
	name      string
	payload   string
	wantError bool
	wantNonce string
	wantCols  int
	wantRows  int
}

var helloCorpus = []protocolCorpusCase{
	{name: "valid", payload: `{"type":"hello","nonce":"nonce-1","cols":80,"rows":24}`, wantNonce: "nonce-1", wantCols: 80, wantRows: 24},
	{name: "normalizes low dimensions", payload: `{"type":"hello","nonce":"low","cols":1,"rows":1}`, wantNonce: "low", wantCols: minColumns, wantRows: minRows},
	{name: "normalizes high dimensions", payload: `{"type":"hello","nonce":"high","cols":9999,"rows":9999}`, wantNonce: "high", wantCols: maxColumns, wantRows: maxRows},
	{name: "unknown field", payload: `{"type":"hello","nonce":"nonce","cols":80,"rows":24,"extra":true}`, wantError: true},
	{name: "duplicate field", payload: `{"type":"hello","nonce":"nonce","cols":80,"rows":24,"rows":25}`, wantError: true},
	{name: "trailing value", payload: `{"type":"hello","nonce":"nonce","cols":80,"rows":24}{}`, wantError: true},
	{name: "missing nonce", payload: `{"type":"hello","cols":80,"rows":24}`, wantError: true},
	{name: "missing columns", payload: `{"type":"hello","nonce":"nonce","rows":24}`, wantError: true},
	{name: "missing rows", payload: `{"type":"hello","nonce":"nonce","cols":80}`, wantError: true},
	{name: "empty nonce", payload: `{"type":"hello","nonce":"","cols":80,"rows":24}`, wantError: true},
	{name: "wrong type", payload: `{"type":"hello","nonce":"nonce","cols":"80","rows":24}`, wantError: true},
	{name: "wrong message", payload: `{"type":"resize","nonce":"nonce","cols":80,"rows":24}`, wantError: true},
	{name: "null", payload: `null`, wantError: true},
	{name: "malformed", payload: `{"type":"hello","nonce":"nonce","cols":80,`, wantError: true},
}

var resizeCorpus = []protocolCorpusCase{
	{name: "valid", payload: `{"type":"resize","cols":120,"rows":40}`, wantCols: 120, wantRows: 40},
	{name: "normalizes low dimensions", payload: `{"type":"resize","cols":1,"rows":1}`, wantCols: minColumns, wantRows: minRows},
	{name: "normalizes high dimensions", payload: `{"type":"resize","cols":9999,"rows":9999}`, wantCols: maxColumns, wantRows: maxRows},
	{name: "unknown field", payload: `{"type":"resize","cols":80,"rows":24,"extra":true}`, wantError: true},
	{name: "duplicate field", payload: `{"type":"resize","cols":80,"rows":24,"rows":25}`, wantError: true},
	{name: "trailing value", payload: `{"type":"resize","cols":80,"rows":24}{}`, wantError: true},
	{name: "missing columns", payload: `{"type":"resize","rows":24}`, wantError: true},
	{name: "missing rows", payload: `{"type":"resize","cols":80}`, wantError: true},
	{name: "wrong type", payload: `{"type":"resize","cols":"80","rows":24}`, wantError: true},
	{name: "wrong message", payload: `{"type":"hello","cols":80,"rows":24}`, wantError: true},
	{name: "null", payload: `null`, wantError: true},
	{name: "malformed", payload: `{"type":"resize","cols":80,`, wantError: true},
}

func TestProtocolDecoderCorpus(t *testing.T) {
	t.Run("hello", func(t *testing.T) {
		for _, testCase := range helloCorpus {
			t.Run(testCase.name, func(t *testing.T) {
				nonce, dimensions, err := decodeHello([]byte(testCase.payload))
				if testCase.wantError {
					if err == nil {
						t.Fatalf("decodeHello(%s) succeeded: nonce=%q dimensions=%+v", testCase.payload, nonce, dimensions)
					}
					return
				}
				if err != nil {
					t.Fatalf("decodeHello(%s): %v", testCase.payload, err)
				}
				if nonce != testCase.wantNonce || dimensions.Cols != testCase.wantCols || dimensions.Rows != testCase.wantRows {
					t.Fatalf("decodeHello(%s) = nonce %q, dimensions %+v; want nonce %q, dimensions %dx%d", testCase.payload, nonce, dimensions, testCase.wantNonce, testCase.wantCols, testCase.wantRows)
				}
			})
		}
	})

	t.Run("resize", func(t *testing.T) {
		for _, testCase := range resizeCorpus {
			t.Run(testCase.name, func(t *testing.T) {
				dimensions, err := decodeResize([]byte(testCase.payload))
				if testCase.wantError {
					if err == nil {
						t.Fatalf("decodeResize(%s) succeeded: dimensions=%+v", testCase.payload, dimensions)
					}
					return
				}
				if err != nil {
					t.Fatalf("decodeResize(%s): %v", testCase.payload, err)
				}
				if dimensions.Cols != testCase.wantCols || dimensions.Rows != testCase.wantRows {
					t.Fatalf("decodeResize(%s) = dimensions %+v; want %dx%d", testCase.payload, dimensions, testCase.wantCols, testCase.wantRows)
				}
			})
		}
	})
}

func FuzzDecodeHello(f *testing.F) {
	for _, testCase := range helloCorpus {
		f.Add([]byte(testCase.payload))
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		nonce, dimensions, err := decodeHello(payload)
		if err != nil {
			return
		}
		if nonce == "" {
			t.Fatal("successful hello has an empty nonce")
		}
		if dimensions.Cols < minColumns || dimensions.Cols > maxColumns {
			t.Fatalf("successful hello has columns outside bounds: %d", dimensions.Cols)
		}
		if dimensions.Rows < minRows || dimensions.Rows > maxRows {
			t.Fatalf("successful hello has rows outside bounds: %d", dimensions.Rows)
		}
	})
}

func FuzzDecodeResize(f *testing.F) {
	for _, testCase := range resizeCorpus {
		f.Add([]byte(testCase.payload))
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		dimensions, err := decodeResize(payload)
		if err != nil {
			return
		}
		if dimensions.Cols < minColumns || dimensions.Cols > maxColumns {
			t.Fatalf("successful resize has columns outside bounds: %d", dimensions.Cols)
		}
		if dimensions.Rows < minRows || dimensions.Rows > maxRows {
			t.Fatalf("successful resize has rows outside bounds: %d", dimensions.Rows)
		}
	})
}
