package mcpclient

func fakePromptsForMode(mode string) []map[string]any {
	if (mode == "close_once_then_new_tool" || mode == "notify_list_changed") && phaseExists() {
		return []map[string]any{{
			"name":        "new_review",
			"description": "Review after catalog refresh",
			"arguments": []map[string]any{{
				"name":        "target",
				"description": "path or topic",
				"required":    true,
			}},
		}}
	}
	return []map[string]any{{
		"name":        "review",
		"description": "Review a target",
		"arguments": []map[string]any{{
			"name":        "target",
			"description": "path or topic",
			"required":    true,
		}},
	}}
}

func fakeToolsForMode(mode string) []map[string]any {
	emptyObject := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	echoSchema := map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}
	switch mode {
	case "no_tools":
		return nil
	case "hang":
		return []map[string]any{{"name": "hang", "description": "Never responds", "inputSchema": emptyObject}}
	case "close_on_call":
		return []map[string]any{{"name": "close", "description": "Close transport", "inputSchema": emptyObject}}
	case "close_once_then_new_tool", "notify_list_changed":
		if phaseExists() {
			return []map[string]any{{"name": "new_echo", "description": "New echo text", "inputSchema": echoSchema}}
		}
		return []map[string]any{{"name": "echo", "description": "Echo text", "inputSchema": echoSchema}}
	case "large":
		return []map[string]any{{"name": "large", "description": "Large text", "inputSchema": emptyObject}}
	case "huge_raw":
		return []map[string]any{{"name": "huge_raw", "description": "Oversized raw response", "inputSchema": emptyObject}}
	default:
		return []map[string]any{
			{"name": "echo", "description": "Echo text", "inputSchema": echoSchema},
			{"name": "env", "description": "Show selected env", "inputSchema": emptyObject},
			{"name": "fail", "description": "Fail with secret", "inputSchema": emptyObject},
		}
	}
}

func response(id any, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcErrorResponse(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}
