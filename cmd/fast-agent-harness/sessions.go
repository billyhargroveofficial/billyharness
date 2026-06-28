package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gateway"
)

func sessionsCmd(args []string) error {
	return sessionsCommand(args, os.Stdout)
}

func sessionsCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return sessionsListCommand(nil, out)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list", "ls":
		return sessionsListCommand(args[1:], out)
	case "inspect", "show":
		return sessionsInspectCommand(args[1:], out)
	default:
		return fmt.Errorf("unknown sessions command %q", args[0])
	}
}

func sessionsListCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions list", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list, err := gateway.ListStoredSessions(*dir)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	fmt.Fprintf(out, "billyharness sessions\n")
	fmt.Fprintf(out, "dir: %s\n", list.Dir)
	if len(list.Sessions) == 0 {
		fmt.Fprintln(out, "sessions: none")
	}
	for _, session := range list.Sessions {
		marker := "jsonl"
		if session.Legacy {
			marker = "legacy"
		}
		fmt.Fprintf(out, "- %s %s messages=%d history=%d events=%d last=%s replay=%t\n",
			session.ID,
			marker,
			session.MessageCount,
			session.HistorySeq,
			session.EventSeq,
			emptyDash(session.LastEvent),
			session.OfflineReplayReady,
		)
	}
	for _, warning := range list.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	return nil
}

func sessionsInspectCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions inspect", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sessions inspect [-dir DIR] [-json] SESSION_ID")
	}
	inspection, err := gateway.InspectStoredSession(*dir, fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(inspection)
	}
	printSessionInspection(out, inspection)
	return nil
}

func printSessionInspection(out io.Writer, inspection gateway.StoredSessionInspection) {
	fmt.Fprintf(out, "billyharness session\n")
	fmt.Fprintf(out, "id: %s\n", inspection.SessionID)
	fmt.Fprintf(out, "dir: %s\n", inspection.Dir)
	if inspection.SessionDir != "" {
		fmt.Fprintf(out, "session dir: %s\n", inspection.SessionDir)
	}
	fmt.Fprintf(out, "mode: %s\n", sessionMode(inspection.Legacy))
	fmt.Fprintf(out, "messages: %d\n", inspection.MessageCount)
	fmt.Fprintf(out, "offline replay: %t\n", inspection.OfflineReplayReady)
	if inspection.Manifest.SchemaVersion != 0 || inspection.Manifest.SessionID != "" {
		fmt.Fprintf(out, "manifest: schema=%d history=%s events=%s snapshots=%s,%s,%s\n",
			inspection.Manifest.SchemaVersion,
			emptyDash(inspection.Manifest.HistoryJSONL),
			emptyDash(inspection.Manifest.EventsJSONL),
			emptyDash(inspection.Manifest.ConfigSnapshotJSON),
			emptyDash(inspection.Manifest.ModelProviderSnapshotJSON),
			emptyDash(inspection.Manifest.MCPSnapshotJSON),
		)
	}
	fmt.Fprintf(out, "history: exists=%t records=%d messages=%d sha=%s\n",
		inspection.History.Exists,
		inspection.History.Records,
		inspection.History.MessageCount,
		emptyDash(inspection.History.HistorySHA256),
	)
	fmt.Fprintf(out, "events: exists=%t records=%d last=%s output_refs=%d\n",
		inspection.Events.Exists,
		inspection.Events.Records,
		emptyDash(inspection.Events.LastEvent),
		inspection.Events.OutputRefs,
	)
	fmt.Fprintln(out, "files:")
	for _, file := range inspection.Files {
		fmt.Fprintf(out, "  - %s exists=%t bytes=%d path=%s\n", file.Name, file.Exists, file.Bytes, file.Path)
	}
	for _, warning := range inspection.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
}

func sessionMode(legacy bool) string {
	if legacy {
		return "legacy"
	}
	return "jsonl"
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
