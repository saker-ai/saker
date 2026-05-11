package tool

import (
	"encoding/json"
	"testing"
)

// FuzzConvertMCPSchema feeds raw JSON bytes through convertMCPSchema, which
// is responsible for normalising MCP tool input schemas of varying shapes
// into the local *JSONSchema struct. The function accepts the input as
// json.RawMessage when the bytes look like JSON, and falls back to a
// generic map[string]interface{} decode when the strongly-typed unmarshal
// loses required fields.
//
// Property under test: must not panic on any byte sequence. Returning an
// error is acceptable; returning a partial schema is acceptable; panics
// are bugs.
func FuzzConvertMCPSchema(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{}`),
		[]byte(`{"type":"object","properties":{"foo":{"type":"string"}}}`),
		[]byte(`{"type":"object","required":["a","b"]}`),
		[]byte(`{"type":"array","items":{"type":"string"}}`),
		[]byte(`{"type":"unknown_type_xxx"}`),
		[]byte(`{"properties":` + nestedObject(32) + `}`),
		[]byte(`null`),
		[]byte(``),
		[]byte(`not json at all`),
		[]byte(`{"required":[1,2,3]}`), // wrong required-element types
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		// Drive both code paths: json.RawMessage (preferred) and []byte
		// (alternative branch in the type switch).
		_, err1 := convertMCPSchema(json.RawMessage(raw))
		_ = err1
		_, err2 := convertMCPSchema(raw)
		_ = err2

		// And the default branch: hand it an arbitrary `any` value derived
		// from the bytes. If it parses to something json/Marshal can round-
		// trip, push it back through the function.
		var generic any
		if json.Unmarshal(raw, &generic) == nil {
			_, err3 := convertMCPSchema(generic)
			_ = err3
		}
	})
}

// nestedObject builds {"a":{"a":{...{"a":1}...}}} for `depth` levels — a
// useful adversarial seed for stack/recursion behaviour in the JSON parser.
func nestedObject(depth int) string {
	open := ""
	close := ""
	for i := 0; i < depth; i++ {
		open += `{"a":`
		close += `}`
	}
	return open + `1` + close
}
