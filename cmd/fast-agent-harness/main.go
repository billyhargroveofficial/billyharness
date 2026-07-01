package main

import (
	"fmt"
	"os"
)

var version = "0.1.0"

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
	switch os.Args[1] {
	case "run":
		return runOnce(os.Args[2:])
	case "chat":
		return chat(os.Args[2:])
	case "tui":
		return tuiCmd(os.Args[2:])
	case "telegram":
		return telegramCmd(os.Args[2:])
	case "serve", "gateway":
		return serve(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return nil
	case "mcp":
		return mcp(os.Args[2:])
	case "config":
		return configCommand(os.Args[2:], os.Stdout)
	case "bench":
		return benchCmd(os.Args[2:])
	case "sessions", "session":
		return sessionsCmd(os.Args[2:])
	case "memory":
		return memoryCmd(os.Args[2:])
	case "commands", "command":
		return commandsCmd(os.Args[2:])
	case "tools":
		return printTools()
	case "doctor", "health":
		return doctorCmd(os.Args[2:])
	case "hygiene":
		return hygieneCmd(os.Args[2:])
	default:
		usage()
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func usage() {
	fmt.Println("fast-agent-harness-go")
	fmt.Println("default:")
	fmt.Println("  fast-agent-harness                 start gateway using billyharness config")
	fmt.Println("commands:")
	fmt.Println("  run [-mock] <prompt>")
	fmt.Println("  run [-gateway http://127.0.0.1:8765] <prompt>")
	fmt.Println("  tui [-gateway http://127.0.0.1:8765] [-model deepseek-v4-flash]")
	fmt.Println("  telegram [-gateway http://127.0.0.1:8765] [-model deepseek-v4-flash]")
	fmt.Println("  chat [-mock]")
	fmt.Println("  chat [-gateway http://127.0.0.1:8765]")
	fmt.Println("  serve|gateway [-mock] [-addr 127.0.0.1:8765]")
	fmt.Println("  mcp")
	fmt.Println("  config inspect [-json]")
	fmt.Println("  config mcp-migrate [-file FILE] [-json]")
	fmt.Println("  sessions list [-dir DIR] [-json]")
	fmt.Println("  sessions inspect [-dir DIR] [-json] SESSION_ID")
	fmt.Println("  sessions context [-dir DIR] [-json] SESSION_ID")
	fmt.Println("  sessions index rebuild|show|delete [-dir DIR] [-json]")
	fmt.Println("  sessions search|tools|errors|usage|runs [-dir DIR] [-limit N] [-json]")
	fmt.Println("  sessions import [-input FILE] [-format auto|jsonl|markdown] [-json]")
	fmt.Println("  memory list|search|read|add|replace|remove")
	fmt.Println("  commands list|search [-limit N] [-json]")
	fmt.Println("  bench run -tasks tasks.jsonl -out runs [-model deepseek-v4-flash] [-max-rounds 100]")
	fmt.Println("  bench terminal-bench export -tasks tasks.jsonl -out tb-dataset")
	fmt.Println("  bench terminal-bench import -dataset tb-dataset [-out tasks.jsonl]")
	fmt.Println("  tools")
	fmt.Println("  doctor|health [-json] [-strict] [-build=true] [-services=true] [-gateway=true]")
	fmt.Println("  hygiene [-json] [-strict] [-repo DIR]")
}
