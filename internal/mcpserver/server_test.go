package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestServeInitializeListAndCall(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"time_now","arguments":{}}}`,
		"",
	}, "\n")
	var output bytes.Buffer
	server := New(tools.NewRegistry(config.Default()))
	if err := server.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}

	responses := readResponses(t, output.String())
	if len(responses) != 3 {
		t.Fatalf("responses len = %d output=%s", len(responses), output.String())
	}
	if responses[0]["id"].(float64) != 1 {
		t.Fatalf("initialize response = %#v", responses[0])
	}
	initResult := responses[0]["result"].(map[string]any)
	if initResult["protocolVersion"] != protocolVersion {
		t.Fatalf("protocolVersion = %#v", initResult["protocolVersion"])
	}
	listResult := responses[1]["result"].(map[string]any)
	listTools := listResult["tools"].([]any)
	if len(listTools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	callResult := responses[2]["result"].(map[string]any)
	content := callResult["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("call content = %#v", content)
	}
	if callResult["isError"] != false {
		t.Fatalf("isError = %#v", callResult["isError"])
	}
}

func TestServeUnknownMethodReturnsJSONRPCError(t *testing.T) {
	var output bytes.Buffer
	server := New(tools.NewRegistry(config.Default()))
	err := server.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":"x","method":"nope"}`+"\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	responses := readResponses(t, output.String())
	if len(responses) != 1 {
		t.Fatalf("responses len = %d", len(responses))
	}
	rpcErr := responses[0]["error"].(map[string]any)
	if rpcErr["code"].(float64) != -32601 {
		t.Fatalf("error = %#v", rpcErr)
	}
}

func readResponses(t *testing.T, out string) []map[string]any {
	t.Helper()
	var responses []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, msg)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return responses
}
