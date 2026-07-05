package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/attachments"
)

const (
	defaultAttachmentsGCMaxAge   = 30 * 24 * time.Hour
	defaultAttachmentsGCMaxBytes = int64(1 << 30)
)

func attachmentsCmd(args []string) error {
	if len(args) == 0 {
		printAttachmentsUsage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "gc":
		return attachmentsGCCmd(args[1:], os.Stdout)
	case "help", "-h", "--help":
		printAttachmentsUsage(os.Stdout)
		return nil
	default:
		printAttachmentsUsage(os.Stderr)
		return fmt.Errorf("unknown attachments command %q", args[0])
	}
}

func attachmentsGCCmd(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("attachments gc", flag.ExitOnError)
	maxAge := fs.Duration("max-age", defaultAttachmentsGCMaxAge, "delete attachment files older than this duration; 0 disables age pruning")
	maxBytes := fs.Int64("max-bytes", defaultAttachmentsGCMaxBytes, "keep attachment store under this many bytes; 0 disables size pruning")
	dryRun := fs.Bool("dry-run", false, "print current usage without deleting files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("attachments gc takes no positional arguments")
	}
	if *maxAge < 0 {
		return fmt.Errorf("-max-age must be >= 0")
	}
	if *maxBytes < 0 {
		return fmt.Errorf("-max-bytes must be >= 0")
	}
	store := attachments.DefaultStore()
	if *dryRun {
		fileCount, totalBytes, err := store.Usage()
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "attachments gc dry-run: %s has %d file(s), %s; max-age=%s max-bytes=%s\n",
			store.Root,
			fileCount,
			humanBytes(totalBytes),
			maxAge.String(),
			humanBytes(*maxBytes),
		)
		return nil
	}
	removed, removedBytes, err := store.Prune(*maxAge, *maxBytes)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "attachments gc: removed %d file(s), %s from %s\n", removed, humanBytes(removedBytes), store.Root)
	return nil
}

func printAttachmentsUsage(w io.Writer) {
	fmt.Fprintln(w, "attachments commands:")
	fmt.Fprintln(w, "  attachments gc [-max-age=720h] [-max-bytes=1073741824] [-dry-run]")
}
