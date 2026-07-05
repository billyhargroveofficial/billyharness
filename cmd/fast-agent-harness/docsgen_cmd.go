package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/docsgen"
)

func docsgenCmd(args []string) error {
	fs := flag.NewFlagSet("docsgen", flag.ExitOnError)
	check := fs.Bool("check", false, "check generated docs without writing")
	only := fs.String("only", "", "generate only the named target")
	outDir := fs.String("out", filepath.Join("docs", "generated"), "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets, err := docsgen.SelectTargets(strings.TrimSpace(*only))
	if err != nil {
		return err
	}
	if !*check {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			return err
		}
	}
	stale := false
	for _, target := range targets {
		body, err := target.Generate()
		if err != nil {
			return fmt.Errorf("generate %s: %w", target.Name, err)
		}
		path := filepath.Join(*outDir, target.Filename)
		existing, readErr := os.ReadFile(path)
		same := readErr == nil && bytes.Equal(existing, body)
		switch {
		case *check && same:
			fmt.Printf("unchanged %s\n", path)
		case *check:
			stale = true
			fmt.Printf("stale %s\n", path)
		case same:
			fmt.Printf("unchanged %s\n", path)
		default:
			if err := os.WriteFile(path, body, 0o644); err != nil {
				return err
			}
			fmt.Printf("written %s\n", path)
		}
	}
	if stale {
		return fmt.Errorf("generated docs are stale; run: go run ./cmd/fast-agent-harness docsgen")
	}
	return nil
}
