package tools

import (
	"encoding/json"
	"testing"
)

func FuzzValidateArgsSchema(f *testing.F) {
	seeds := []struct {
		schema string
		args   string
	}{
		{`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`, `{"path":"README.md"}`},
		{`{"type":"object","properties":{"limit":{"type":"integer"}}}`, `{"limit":10}`},
		{`{"type":"object","properties":{"topic":{"type":"string"},"path":{"type":"string"}},"anyOf":[{"required":["topic"]},{"required":["path"]}]}`, `{"topic":"ops"}`},
		{`{"type":"object","properties":{"x":{"pattern":"^[a-z]+$"}}}`, `{"x":"abc"}`},
		{`not json`, `{"x":1}`},
	}
	for _, seed := range seeds {
		f.Add(seed.schema, seed.args)
	}
	f.Fuzz(func(t *testing.T, schema string, args string) {
		if len(schema) > 4096 || len(args) > 4096 {
			t.Skip()
		}
		_ = validateArgs(json.RawMessage(schema), json.RawMessage(args))
	})
}
