package clientux

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const (
	TranscriptExportSourceCells    = "cells"
	TranscriptExportSourceMessages = "messages"
	TranscriptExportSourceEvents   = "events"
	TranscriptExportSourceCombined = "combined"
)

type TranscriptExportMetadata struct {
	SourceStore         string
	SourceMode          string
	TranscriptMode      string
	RuntimeMode         string
	LocalChatID         string
	GatewaySessionID    string
	LastGatewayEventSeq int64
	SeqRange            TranscriptExportSeqRange
	Provider            string
	Model               string
	Profile             string
	AccessMode          string
	ReasoningKind       string
	ReasoningEffort     string
	ExportedAt          time.Time
	RedactionMode       string
	Warnings            []string
	Extra               map[string]string
}

type TranscriptExportSeqRange struct {
	FirstKnown bool
	First      int64
	LastKnown  bool
	Last       int64
}

func NormalizeTranscriptExportSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", TranscriptExportSourceCells:
		return TranscriptExportSourceCells
	case TranscriptExportSourceMessages, "message":
		return TranscriptExportSourceMessages
	case TranscriptExportSourceEvents, "event":
		return TranscriptExportSourceEvents
	case TranscriptExportSourceCombined, "session":
		return TranscriptExportSourceCombined
	default:
		return ""
	}
}

func FormatTranscriptExportArtifact(meta TranscriptExportMetadata, body string, extraRedactions ...string) string {
	meta = normalizeTranscriptExportMetadata(meta)
	body = strings.TrimSpace(body)
	var b strings.Builder
	b.WriteString("# Billyharness Transcript Export\n\n")
	b.WriteString("metadata:\n")
	writeExportField(&b, "exported_at", meta.ExportedAt.UTC().Format(time.RFC3339))
	writeExportField(&b, "source_store", meta.SourceStore)
	writeExportField(&b, "source_mode", meta.SourceMode)
	writeExportField(&b, "transcript_mode", meta.TranscriptMode)
	writeExportField(&b, "runtime_mode", meta.RuntimeMode)
	writeExportField(&b, "local_chat_id", meta.LocalChatID)
	writeExportField(&b, "gateway_session_id", meta.GatewaySessionID)
	writeExportField(&b, "last_gateway_event_seq", fmt.Sprintf("%d", meta.LastGatewayEventSeq))
	writeExportField(&b, "seq_range", meta.SeqRange.String())
	writeExportField(&b, "provider", meta.Provider)
	writeExportField(&b, "model", meta.Model)
	writeExportField(&b, "profile", meta.Profile)
	writeExportField(&b, "access_mode", meta.AccessMode)
	writeExportField(&b, "reasoning", strings.TrimSpace(meta.ReasoningKind+"/"+meta.ReasoningEffort))
	writeExportField(&b, "redaction_mode", meta.RedactionMode)
	if len(meta.Extra) > 0 {
		keys := make([]string, 0, len(meta.Extra))
		for key := range meta.Extra {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			writeExportField(&b, key, meta.Extra[key])
		}
	}
	b.WriteString("\nwarnings:\n")
	if len(meta.Warnings) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, warning := range meta.Warnings {
			warning = strings.TrimSpace(warning)
			if warning == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(redactExportValue(warning, extraRedactions...))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n--- transcript ---\n")
	if body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r TranscriptExportSeqRange) String() string {
	start := "unknown"
	if r.FirstKnown {
		start = fmt.Sprintf("%d", r.First)
	}
	end := "unknown"
	if r.LastKnown {
		end = fmt.Sprintf("%d", r.Last)
	}
	return start + ".." + end
}

func normalizeTranscriptExportMetadata(meta TranscriptExportMetadata) TranscriptExportMetadata {
	meta.SourceMode = NormalizeTranscriptExportSource(meta.SourceMode)
	if meta.SourceMode == "" {
		meta.SourceMode = TranscriptExportSourceCells
	}
	if strings.TrimSpace(meta.SourceStore) == "" {
		meta.SourceStore = "unknown"
	}
	if strings.TrimSpace(meta.TranscriptMode) == "" {
		meta.TranscriptMode = "unknown"
	}
	if strings.TrimSpace(meta.RuntimeMode) == "" {
		meta.RuntimeMode = "unknown"
	}
	if strings.TrimSpace(meta.RedactionMode) == "" {
		meta.RedactionMode = "none"
	}
	if meta.ExportedAt.IsZero() {
		meta.ExportedAt = time.Now().UTC()
	}
	return meta
}

func writeExportField(b *strings.Builder, key, value string) {
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(redactExportValue(value))
	b.WriteString("\n")
}

func redactExportValue(value string, extra ...string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return strconv.Quote(secrets.Redact(value, extra...))
}
