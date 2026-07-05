package main

import (
	"fmt"
	"os"
)

var (
	version     = "0.1.0"
	buildCommit = "unknown"
	buildTime   = "unknown"
)

type buildMetadata struct {
	Version string
	Commit  string
	BuiltAt string
}

func currentBuildMetadata() buildMetadata {
	return buildMetadata{
		Version: version,
		Commit:  buildCommit,
		BuiltAt: buildTime,
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return serve(nil)
	}
	command, ok := lookupSubcommand(os.Args[1])
	if !ok {
		usage()
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
	return command.Run(os.Args[2:])
}

func usage() {
	writeUsage(os.Stdout)
}
