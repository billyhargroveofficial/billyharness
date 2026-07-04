package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const memoryDriftPolicySessionLocked = "session_locked"

func lockedMemoryContextStatus(messages []protocol.Message) gatewayapi.ContextMemory {
	lockedHash := lockedMemoryContextHash(messages)
	if lockedHash == "" {
		return gatewayapi.ContextMemory{}
	}
	return gatewayapi.ContextMemory{
		Policy:     memoryDriftPolicySessionLocked,
		Status:     "locked",
		LockedHash: lockedHash,
	}
}

func sessionMemoryContextStatus(settings config.InstructionSettings, messages []protocol.Message) gatewayapi.ContextMemory {
	lockedHash := lockedMemoryContextHash(messages)
	if !settings.MemoryEnabled {
		if lockedHash == "" {
			return gatewayapi.ContextMemory{}
		}
		return gatewayapi.ContextMemory{
			Policy:       memoryDriftPolicySessionLocked,
			Status:       "missing",
			LockedHash:   lockedHash,
			CurrentError: "disabled",
		}
	}

	current := currentMemoryContext(settings)
	currentHash := current.hash
	if lockedHash == "" && currentHash == "" {
		return gatewayapi.ContextMemory{}
	}

	status := "current"
	switch {
	case lockedHash == "" && currentHash != "":
		status = "added"
	case lockedHash != "" && currentHash == "":
		status = "missing"
	case lockedHash != currentHash:
		status = "changed"
	}
	return gatewayapi.ContextMemory{
		Policy:          memoryDriftPolicySessionLocked,
		Status:          status,
		LockedHash:      lockedHash,
		CurrentHash:     currentHash,
		CurrentRoots:    current.roots,
		CurrentEntries:  current.entries,
		CurrentWarnings: current.warnings,
		CurrentCapped:   current.capped,
	}
}

func lockedMemoryContextHash(messages []protocol.Message) string {
	for _, msg := range messages {
		if hash := memoryContextHash(msg); hash != "" {
			return hash
		}
	}
	return ""
}

type memoryContextDigest struct {
	hash     string
	roots    int
	entries  int
	warnings int
	capped   bool
}

func currentMemoryContext(settings config.InstructionSettings) memoryContextDigest {
	memoryOnly := config.InstructionSettings{
		Profile:               settings.Profile,
		MemoryEnabled:         settings.MemoryEnabled,
		MemorySummaryMaxBytes: settings.MemorySummaryMaxBytes,
		MemoryIndexMaxBytes:   settings.MemoryIndexMaxBytes,
		MemoryTopicMaxBytes:   settings.MemoryTopicMaxBytes,
	}
	for _, msg := range agent.InitialMessagesFromSettings(memoryOnly) {
		if hash := memoryContextHash(msg); hash != "" {
			return memoryContextDigest{
				hash:     hash,
				roots:    countMemoryContextLines(msg.Content, "- source="),
				entries:  countMemoryContextLines(msg.Content, "- type="),
				warnings: countMemoryContextWarnings(msg.Content),
				capped:   strings.Contains(msg.Content, "\ncap_flags: "),
			}
		}
	}
	return memoryContextDigest{}
}

func memoryContextHash(msg protocol.Message) string {
	if msg.Role != protocol.RoleUser {
		return ""
	}
	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "# Memory context") {
		return ""
	}
	body := memoryContextBody(content)
	if body == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func memoryContextBody(content string) string {
	start := strings.Index(content, "<MEMORY_CONTEXT>")
	end := strings.Index(content, "</MEMORY_CONTEXT>")
	if start < 0 || end < start {
		return ""
	}
	end += len("</MEMORY_CONTEXT>")
	return strings.TrimSpace(content[start:end])
}

func countMemoryContextLines(content, prefix string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			count++
		}
	}
	return count
}

func countMemoryContextWarnings(content string) int {
	inWarnings := false
	count := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "warnings:":
			inWarnings = true
		case inWarnings && strings.HasPrefix(trimmed, "- "):
			count++
		case inWarnings && trimmed != "":
			inWarnings = false
		}
	}
	return count
}

func instructionSettingsFromSessionSnapshot(snapshot sessionSnapshotProjection) config.InstructionSettings {
	return config.InstructionSettings{
		Profile:                snapshot.Profile,
		WorkspaceRoots:         append([]string(nil), snapshot.ToolPolicy.WorkspaceRoots...),
		ProjectDocMaxBytes:     snapshot.ToolPolicy.ProjectDocMaxBytes,
		ProjectDocFallbacks:    append([]string(nil), snapshot.ToolPolicy.ProjectDocFallbacks...),
		ProjectContextMaxBytes: snapshot.ToolPolicy.ProjectContextMaxBytes,
		MemoryEnabled:          snapshot.ToolPolicy.MemoryEnabled,
		MemorySummaryMaxBytes:  snapshot.ToolPolicy.MemorySummaryMaxBytes,
		MemoryIndexMaxBytes:    snapshot.ToolPolicy.MemoryIndexMaxBytes,
		MemoryTopicMaxBytes:    snapshot.ToolPolicy.MemoryTopicMaxBytes,
	}
}
