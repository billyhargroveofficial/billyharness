package telegrambot

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type managedProcessSnapshotter interface {
	ProcessSnapshot(context.Context) (gatewayapi.ManagedProcessResponse, error)
}

func (b *Bot) startManagedProcessWatch(ctx context.Context) {
	if !b.managedProcessWatchEnabled() {
		return
	}
	snapshotter, ok := b.harness.(managedProcessSnapshotter)
	if !ok {
		return
	}
	interval := b.opts.ProcessWatchInterval
	recipients := b.processWatchRecipients()
	last := map[string]bool{}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := b.pollManagedProcessesOnce(ctx, snapshotter, recipients, last); err != nil {
					log.Printf("telegram process watch: %v", err)
				}
			}
		}
	}()
}

func (b *Bot) managedProcessWatchEnabled() bool {
	return b != nil &&
		b.opts.SendEnabled &&
		!b.opts.DryRunDefault &&
		b.opts.ProcessWatchInterval > 0 &&
		len(b.processWatchRecipients()) > 0
}

func (b *Bot) processWatchRecipients() []int64 {
	if b == nil {
		return nil
	}
	seen := map[int64]bool{}
	for chatID := range b.opts.AllowedChatIDs {
		if chatID != 0 {
			seen[chatID] = true
		}
	}
	for userID := range b.opts.AllowedOperatorUserIDs {
		if userID > 0 {
			seen[userID] = true
		}
	}
	out := make([]int64, 0, len(seen))
	for chatID := range seen {
		out = append(out, chatID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (b *Bot) pollManagedProcessesOnce(ctx context.Context, snapshotter managedProcessSnapshotter, recipients []int64, last map[string]bool) error {
	if b == nil || snapshotter == nil || len(recipients) == 0 {
		return nil
	}
	if last == nil {
		last = map[string]bool{}
	}
	snapshot, err := snapshotter.ProcessSnapshot(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, process := range snapshot.Processes.Processes {
		id := strings.TrimSpace(process.ID)
		if id == "" {
			continue
		}
		previousRunning, known := last[id]
		finished := (!known && !process.Running) || (known && previousRunning && !process.Running)
		last[id] = process.Running
		seen[id] = true
		if !finished {
			continue
		}
		text := redactTelegramText(formatManagedProcessFinished(process))
		for _, chatID := range recipients {
			if _, err := b.client.SendMessage(ctx, chatID, text, "", 0); err != nil {
				return fmt.Errorf("send process finished chat=%d process=%s: %w", chatID, id, err)
			}
		}
	}
	for id := range last {
		if !seen[id] {
			delete(last, id)
		}
	}
	return nil
}

func formatManagedProcessFinished(process protocol.ManagedProcessStatus) string {
	var lines []string
	lines = append(lines, "Billyharness process finished")
	lines = append(lines, "id: "+strings.TrimSpace(process.ID))
	if process.ExitError != "" {
		lines = append(lines, "exit: "+process.ExitError)
	} else {
		lines = append(lines, fmt.Sprintf("exit: %d", process.ExitCode))
	}
	if process.ElapsedMS > 0 {
		lines = append(lines, "elapsed: "+formatManagedProcessElapsed(process.ElapsedMS))
	}
	if tail := strings.TrimSpace(process.OutputTailPreview); tail != "" {
		lines = append(lines, "tail:")
		lines = append(lines, tail)
	}
	return strings.Join(lines, "\n")
}

func formatManagedProcessElapsed(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return d.String()
	}
	return d.Round(time.Second).String()
}
