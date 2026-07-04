package tools

import (
	"encoding/json"
	"testing"
)

func BenchmarkToolSchemaValidation(b *testing.B) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"limit":{"type":"integer"},
			"include_hidden":{"type":"boolean"},
			"mode":{"type":"string","enum":["summary","full"]}
		},
		"required":["path"],
		"additionalProperties":false
	}`)
	validArgs := json.RawMessage(`{"path":"internal/gateway","limit":100,"include_hidden":false,"mode":"summary"}`)
	invalidArgs := json.RawMessage(`{"path":"","limit":"many","extra":true}`)

	for _, tc := range []struct {
		name string
		args json.RawMessage
	}{
		{name: "valid_object", args: validArgs},
		{name: "invalid_object", args: invalidArgs},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				err := validateArgs(schema, tc.args)
				if tc.name == "valid_object" && err != nil {
					b.Fatal(err)
				}
				if tc.name == "invalid_object" && err == nil {
					b.Fatal("expected validation error")
				}
			}
		})
	}
}
