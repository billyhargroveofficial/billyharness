package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/memory"
)

func memoryCmd(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			memoryUsage()
			return nil
		}
	}
	out, err := memory.RunCommand(config.Default().InstructionSettings(), strings.Join(args, " "))
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, out)
	return nil
}

func memoryUsage() {
	fmt.Println("usage:")
	fmt.Println("  fast-agent-harness memory list [query=TEXT] [source=home|profile]")
	fmt.Println("  fast-agent-harness memory search query=TEXT [source=home|profile]")
	fmt.Println("  fast-agent-harness memory read topic=NAME|path=topics/name.md [source=home|profile]")
	fmt.Println("  fast-agent-harness memory add type=user topic=NAME summary=\"...\" path=topics/name.md [body=\"...\"] confirm=true")
	fmt.Println("  fast-agent-harness memory replace type=user topic=NAME summary=\"...\" path=topics/name.md [body=\"...\"] confirm=true")
	fmt.Println("  fast-agent-harness memory remove topic=NAME|path=topics/name.md confirm=true")
}
