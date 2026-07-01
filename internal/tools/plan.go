package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	maxTodoItems      = 20
	maxTodoIDRunes    = 64
	maxTodoTextRunes  = 240
	defaultTodoStatus = "pending"
	defaultTodoPrio   = "medium"
)

func (r *Registry) addTodoWrite() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "todo_write",
			Description: "Replace the session-scoped agent work plan with a bounded todo list. Use it for multi-step work tracking; submit the full current list each time.",
			Parameters:  raw(`{"type":"object","properties":{"todos":{"type":"array","maxItems":20,"items":{"type":"object","properties":{"id":{"type":"string"},"content":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed","blocked"]},"priority":{"type":"string","enum":["high","medium","low"]}},"required":["id","content","status"],"additionalProperties":false}}},"required":["todos"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Parallel: ParallelMetadata{Policy: ParallelPolicyExclusiveWorkspace, Cancellable: true, MaxConcurrency: 1},
		Handler:  handleTodoWrite,
	})
}

func handleTodoWrite(_ context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		Todos []protocol.TodoItem `json:"todos"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	state, err := normalizeTodoState(in.Todos)
	if err != nil {
		result := errorResult("todo_invalid", err.Error())
		return result, err
	}
	summary := todoStateSummary(state)
	return Result{
		Content: todoStateDetails(state),
		Metadata: map[string]any{
			"todo_state":       state,
			"todo_total":       len(state.Todos),
			"todo_pending":     state.Pending,
			"todo_in_progress": state.InProgress,
			"todo_completed":   state.Completed,
			"todo_blocked":     state.Blocked,
			"display_summary":  summary,
		},
	}, nil
}

func normalizeTodoState(items []protocol.TodoItem) (protocol.TodoState, error) {
	if len(items) > maxTodoItems {
		return protocol.TodoState{}, fmt.Errorf("todo list has %d items, max %d", len(items), maxTodoItems)
	}
	seen := map[string]struct{}{}
	out := protocol.TodoState{Todos: make([]protocol.TodoItem, 0, len(items))}
	for i, item := range items {
		normalized, err := normalizeTodoItem(item)
		if err != nil {
			return protocol.TodoState{}, fmt.Errorf("todo %d: %w", i+1, err)
		}
		if _, ok := seen[normalized.ID]; ok {
			return protocol.TodoState{}, fmt.Errorf("duplicate todo id %q", normalized.ID)
		}
		seen[normalized.ID] = struct{}{}
		switch normalized.Status {
		case "pending":
			out.Pending++
		case "in_progress":
			out.InProgress++
		case "completed":
			out.Completed++
		case "blocked":
			out.Blocked++
		}
		out.Todos = append(out.Todos, normalized)
	}
	if out.InProgress > 1 {
		return protocol.TodoState{}, fmt.Errorf("todo list has %d in_progress items, max 1", out.InProgress)
	}
	return out, nil
}

func normalizeTodoItem(item protocol.TodoItem) (protocol.TodoItem, error) {
	item.ID = compactTodoText(item.ID, maxTodoIDRunes)
	item.Content = compactTodoText(item.Content, maxTodoTextRunes)
	item.Status = strings.TrimSpace(item.Status)
	item.Priority = strings.TrimSpace(item.Priority)
	if item.ID == "" {
		return protocol.TodoItem{}, fmt.Errorf("id required")
	}
	if item.Content == "" {
		return protocol.TodoItem{}, fmt.Errorf("content required")
	}
	if item.Status == "" {
		item.Status = defaultTodoStatus
	}
	if !validTodoStatus(item.Status) {
		return protocol.TodoItem{}, fmt.Errorf("invalid status %q", item.Status)
	}
	if item.Priority == "" {
		item.Priority = defaultTodoPrio
	}
	if !validTodoPriority(item.Priority) {
		return protocol.TodoItem{}, fmt.Errorf("invalid priority %q", item.Priority)
	}
	return item, nil
}

func validTodoStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "blocked":
		return true
	default:
		return false
	}
}

func validTodoPriority(priority string) bool {
	switch priority {
	case "high", "medium", "low":
		return true
	default:
		return false
	}
}

func compactTodoText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "..."
}

func todoStateSummary(state protocol.TodoState) string {
	total := len(state.Todos)
	parts := []string{fmt.Sprintf("plan %d todo%s", total, pluralSuffix(total))}
	if state.InProgress > 0 {
		parts = append(parts, fmt.Sprintf("%d in progress", state.InProgress))
	}
	if state.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", state.Pending))
	}
	if state.Completed > 0 {
		parts = append(parts, fmt.Sprintf("%d completed", state.Completed))
	}
	if state.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", state.Blocked))
	}
	if current, ok := currentTodo(state); ok {
		parts = append(parts, "now: "+current.Content)
	}
	return strings.Join(parts, " · ")
}

func todoStateDetails(state protocol.TodoState) string {
	if len(state.Todos) == 0 {
		return "Plan: empty"
	}
	lines := []string{todoStateSummary(state)}
	for _, item := range state.Todos {
		lines = append(lines, fmt.Sprintf("- [%s] (%s) %s: %s", item.Status, item.Priority, item.ID, item.Content))
	}
	return strings.Join(lines, "\n")
}

func currentTodo(state protocol.TodoState) (protocol.TodoItem, bool) {
	for _, item := range state.Todos {
		if item.Status == "in_progress" {
			return item, true
		}
	}
	return protocol.TodoItem{}, false
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
