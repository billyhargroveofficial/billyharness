#!/usr/bin/env python3
import json
import sys


def send(message):
    print(json.dumps(message, separators=(",", ":")), flush=True)


def ok(request_id, result):
    send({"jsonrpc": "2.0", "id": request_id, "result": result})


def err(request_id, code, message):
    send({"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}})


for line in sys.stdin:
    if not line.strip():
        continue
    try:
        req = json.loads(line)
    except json.JSONDecodeError:
        continue
    method = req.get("method")
    request_id = req.get("id")
    if method == "notifications/initialized":
        continue
    if method == "initialize":
        ok(request_id, {
            "protocolVersion": "2025-06-18",
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": "billy-fake-mcp", "version": "1.0.0"},
        })
    elif method == "tools/list":
        ok(request_id, {"tools": [{
            "name": "echo",
            "description": "Echo text back unchanged.",
            "inputSchema": {
                "type": "object",
                "properties": {"text": {"type": "string"}},
                "required": ["text"],
                "additionalProperties": False,
            },
        }]})
    elif method == "tools/call":
        params = req.get("params") or {}
        if params.get("name") != "echo":
            err(request_id, -32602, "unknown tool")
            continue
        args = params.get("arguments") or {}
        ok(request_id, {
            "content": [{"type": "text", "text": str(args.get("text", ""))}],
            "isError": False,
        })
    else:
        err(request_id, -32601, "method not found")
