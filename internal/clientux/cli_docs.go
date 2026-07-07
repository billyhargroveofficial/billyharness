package clientux

// CLICommandDoc is frontend-neutral metadata for the top-level CLI command
// table. Package main attaches Run funcs to these entries.
type CLICommandDoc struct {
	Name    string
	Aliases []string
	Summary string
}

type DoctorCheckDoc struct {
	Name        string
	Description string
	Modes       []string
}

type DoctorCheckDocInput struct {
	DocsTargets     []string
	ManagedServices []DoctorManagedServiceDoc
}

type DoctorManagedServiceDoc struct {
	Service    string
	Subcommand string
	PIDFile    string
}

func CLICommandDocs() []CLICommandDoc {
	out := make([]CLICommandDoc, len(cliCommandDocs))
	for i, doc := range cliCommandDocs {
		out[i] = copyCLICommandDoc(doc)
	}
	return out
}

func DoctorCheckDocs(input DoctorCheckDocInput) []DoctorCheckDoc {
	docs := make([]DoctorCheckDoc, 0, len(baseDoctorCheckDocs)+len(input.DocsTargets)+len(input.ManagedServices)*5)
	for _, doc := range baseDoctorCheckDocs {
		docs = append(docs, copyDoctorCheckDoc(doc))
	}
	for _, target := range input.DocsTargets {
		docs = append(docs, DoctorCheckDoc{
			Name:        "docs:" + target,
			Description: "Compare the generated " + target + " reference fingerprint committed under docs/generated with the live binary registry data.",
			Modes:       []string{"local", "production", "deep"},
		})
	}
	docs = append(docs, copyDoctorCheckDoc(doctorCheckDoc{Name: "git status", Description: "Report whether the repository worktree is dirty.", Modes: []string{"local", "production"}}))
	docs = append(docs, copyDoctorCheckDoc(doctorCheckDoc{Name: "build check", Description: "Compile-check the CLI package unless -build=false disables it.", Modes: []string{"local", "production"}}))
	for _, service := range input.ManagedServices {
		docs = append(docs, DoctorCheckDoc{
			Name:        "service " + service.Service,
			Description: "Check whether the managed systemd unit is active, or report a skip when service checks are disabled.",
			Modes:       []string{"local", "production"},
		})
	}
	for _, service := range input.ManagedServices {
		docs = append(docs, DoctorCheckDoc{
			Name:        "process " + service.Subcommand + " duplicates",
			Description: "Check for duplicate live fast-agent-harness processes for the managed subcommand.",
			Modes:       []string{"local", "production"},
		})
	}
	for _, service := range input.ManagedServices {
		docs = append(docs, DoctorCheckDoc{
			Name:        "pid file " + service.PIDFile,
			Description: "Check whether the managed PID file is absent, valid, or stale.",
			Modes:       []string{"local", "production"},
		})
	}
	for _, service := range input.ManagedServices {
		docs = append(docs, DoctorCheckDoc{
			Name:        "service unit " + service.Service,
			Description: "Inspect selected systemd unit metadata for the production service.",
			Modes:       []string{"production"},
		})
	}
	for _, service := range input.ManagedServices {
		docs = append(docs, DoctorCheckDoc{
			Name:        "service journal " + service.Service,
			Description: "Scan the recent production service journal for crash or restart signals.",
			Modes:       []string{"production"},
		})
	}
	docs = append(docs,
		DoctorCheckDoc{Name: "gateway /health", Description: "Probe the local gateway health endpoint unless -gateway=false disables it.", Modes: []string{"local", "production"}},
		DoctorCheckDoc{Name: "gateway /ready", Description: "Probe the local gateway readiness endpoint unless -gateway=false disables it.", Modes: []string{"local", "production"}},
	)
	return docs
}

func copyCLICommandDoc(doc CLICommandDoc) CLICommandDoc {
	doc.Aliases = append([]string{}, doc.Aliases...)
	return doc
}

func copyDoctorCheckDoc(doc doctorCheckDoc) DoctorCheckDoc {
	return DoctorCheckDoc{Name: doc.Name, Description: doc.Description, Modes: append([]string{}, doc.Modes...)}
}

var cliCommandDocs = []CLICommandDoc{
	{Name: "run", Summary: "run one prompt through local or gateway mode"},
	{Name: "chat", Summary: "start interactive stdin chat"},
	{Name: "tui", Summary: "start the terminal UI"},
	{Name: "telegram", Summary: "start the Telegram bot adapter"},
	{Name: "serve", Aliases: []string{"gateway"}, Summary: "start the gateway server"},
	{Name: "help", Aliases: []string{"-h", "--help"}, Summary: "show top-level command help"},
	{Name: "mcp", Summary: "run the local MCP server adapter"},
	{Name: "config", Summary: "inspect resolved configuration"},
	{Name: "bench", Summary: "run benchmark and Terminal-Bench helpers"},
	{Name: "sessions", Aliases: []string{"session"}, Summary: "list, inspect, search, export, import, and index sessions"},
	{Name: "attachments", Aliases: []string{"attachment"}, Summary: "garbage-collect attachment store data"},
	{Name: "inspect-session", Summary: "inspect one stored session"},
	{Name: "memory", Summary: "manage local memory entries"},
	{Name: "commands", Aliases: []string{"command"}, Summary: "list and search the command registry"},
	{Name: "agentclub", Aliases: []string{"agent-club"}, Summary: "inspect agent-club capabilities and proposals"},
	{Name: "tools", Summary: "print native tool registry JSON"},
	{Name: "doctor", Aliases: []string{"health"}, Summary: "run diagnostics and readiness checks"},
	{Name: "hygiene", Summary: "run source and artifact hygiene checks"},
	{Name: "docsgen", Summary: "generate or check committed reference docs"},
}

type doctorCheckDoc struct {
	Name        string
	Description string
	Modes       []string
}

var baseDoctorCheckDocs = []doctorCheckDoc{
	{Name: "config provider/model", Description: "Check that the resolved provider and model are both set.", Modes: []string{"local", "production"}},
	{Name: "provider capability", Description: "Validate the resolved provider/model capability snapshot.", Modes: []string{"local", "production"}},
	{Name: "gateway bind address", Description: "Classify the configured gateway bind address as loopback or externally reachable.", Modes: []string{"local", "production"}},
	{Name: "auth configured", Description: "Check whether credential material exists for the active provider.", Modes: []string{"local", "production"}},
	{Name: "mcp allowlist", Description: "Check configured MCP servers against the allowed-server list.", Modes: []string{"local", "production"}},
	{Name: "tool catalog", Description: "Ensure the native tool registry exposes visible tools.", Modes: []string{"local", "production"}},
	{Name: "session store access", Description: "Check that the gateway session store path is readable and writable.", Modes: []string{"local", "production"}},
	{Name: "attachments store usage", Description: "Warn when the attachment store exceeds the default garbage-collection threshold.", Modes: []string{"local", "production"}},
}
