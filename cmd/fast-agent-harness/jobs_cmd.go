package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

const (
	defaultJobDuration    = 6 * time.Hour
	defaultJobWorkers     = 1
	defaultJobMaxCycles   = 8
	defaultJobMaxCalls    = 128
	defaultJobMaxTokens   = 1_000_000
	defaultJobMaxAttempts = 128
)

type repeatedJobFlag []string

func (values *repeatedJobFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedJobFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func jobsCmd(args []string) error {
	return jobsCommand(args, os.Stdout)
}

func jobsCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return jobsListCommand(nil, out)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "help", "-h", "--help":
		printJobsUsage(out)
		return nil
	case "create", "new":
		return jobsCreateCommand(args[1:], out)
	case "list", "ls":
		return jobsListCommand(args[1:], out)
	case "show", "get", "inspect":
		return jobsShowCommand(args[1:], out)
	case "run", "start":
		return jobsActionCommand("run", args[1:], out)
	case "pause":
		return jobsActionCommand("pause", args[1:], out)
	case "resume":
		return jobsActionCommand("resume", args[1:], out)
	case "cancel":
		return jobsActionCommand("cancel", args[1:], out)
	default:
		return fmt.Errorf("unknown jobs command %q", args[0])
	}
}

func printJobsUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: fast-agent-harness jobs <command> [args]")
	fmt.Fprintln(out, "  jobs create [flags] GOAL")
	fmt.Fprintln(out, "  jobs list [-gateway URL] [-json]")
	fmt.Fprintln(out, "  jobs show [-gateway URL] [-json] JOB_ID")
	fmt.Fprintln(out, "  jobs run|pause|resume|cancel [-gateway URL] [-json] JOB_ID")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Built-in presets: %s\n", strings.Join(jobs.BuiltInPresetNames(), ", "))
	fmt.Fprintln(out, "Authority is fail-closed: tools, roots, and network hosts are granted only by explicit create flags.")
	printJobProviderTermsNotice(out)
}

func jobsCreateCommand(args []string, out io.Writer) error {
	fs := newJobFlagSet("jobs create", out)
	gatewayURL := fs.String("gateway", "", "gateway base URL; auto-discovered when omitted")
	jsonOut := fs.Bool("json", false, "print JSON")
	jobID := fs.String("job-id", "", "portable idempotency ID; generated when omitted")
	goalFlag := fs.String("goal", "", "job goal; alternatively pass it as positional arguments")
	preset := fs.String("preset", jobs.PresetGeneral, "built-in workflow preset")
	workers := fs.Int("workers", defaultJobWorkers, "parallel workers per ordinary stage (1-4)")
	minCycles := fs.Uint64("min-cycles", 1, "minimum complete review cycles before supervisor may finish")
	providerID := fs.String("provider", "", "execution provider; defaults to resolved config")
	modelID := fs.String("model", "", "execution model or model alias; defaults to resolved config")
	thinking := fs.String("thinking", "", "provider thinking mode; defaults to resolved config")
	var reasoning string
	fs.StringVar(&reasoning, "reasoning", "", "reasoning effort; defaults to resolved config")
	fs.StringVar(&reasoning, "reasoning-effort", "", "alias for -reasoning")
	durationText := fs.String("duration", "", "maximum wall time, for example 6h or 24h; defaults to 6h")
	deadlineText := fs.String("deadline", "", "absolute RFC3339 deadline; mutually exclusive with -duration")
	minRuntimeText := fs.String("min-runtime", "", "minimum wall-clock delay before success, for example 5h; queued, paused, and offline time count")
	cadenceText := fs.String("cadence", "", "durable delay between autonomous review cycles; derived from -min-runtime and -max-cycles when omitted")
	maxCycles := fs.Uint64("max-cycles", defaultJobMaxCycles, "maximum complete workflow cycles")
	maxAttempts := fs.Uint64("max-attempts", defaultJobMaxAttempts, "maximum persisted attempts")
	maxModelCalls := fs.Uint64("max-model-calls", defaultJobMaxCalls, "maximum model calls")
	maxTokens := fs.Uint64("max-tokens", defaultJobMaxTokens, "maximum total provider-reported tokens")
	autoStart := fs.Bool("start", true, "start immediately after durable creation")
	var tools, readRoots, writeRoots, networkHosts repeatedJobFlag
	fs.Var(&tools, "tool", "allowed tool name; repeat for multiple tools")
	fs.Var(&readRoots, "read-root", "allowed filesystem read root; repeat for multiple roots")
	fs.Var(&writeRoots, "write-root", "allowed filesystem write root; repeat for multiple roots")
	fs.Var(&networkHosts, "network-host", "allowed network host; repeat for multiple hosts")
	help, err := parseJobFlagSet(fs, args)
	if err != nil {
		return err
	}
	if help {
		printJobProviderTermsNotice(out)
		return nil
	}

	goal, err := resolveJobGoal(*goalFlag, fs.Args())
	if err != nil {
		return err
	}
	route, err := resolveJobRoute(*providerID, *modelID, *thinking, reasoning)
	if err != nil {
		return err
	}
	warnJobProviderTerms(route, os.Stderr)
	durationSeconds, deadline, err := resolveJobWallClock(*durationText, *deadlineText)
	if err != nil {
		return err
	}
	minRuntimeSeconds, err := resolveOptionalJobDuration("min-runtime", *minRuntimeText)
	if err != nil {
		return err
	}
	cadenceSeconds, err := resolveOptionalJobDuration("cadence", *cadenceText)
	if err != nil {
		return err
	}
	cadenceSeconds, err = resolveJobCadence(minRuntimeSeconds, cadenceSeconds, *maxCycles)
	if err != nil {
		return err
	}
	read, err := normalizeExplicitJobRoots(readRoots)
	if err != nil {
		return fmt.Errorf("read root: %w", err)
	}
	write, err := normalizeExplicitJobRoots(writeRoots)
	if err != nil {
		return fmt.Errorf("write root: %w", err)
	}
	resolvedJobID := strings.TrimSpace(*jobID)
	if resolvedJobID == "" {
		resolvedJobID = newClientJobID()
	}
	request := gatewayapi.CreateJobRequest{
		JobID:             resolvedJobID,
		Goal:              goal,
		Preset:            strings.ToLower(strings.TrimSpace(*preset)),
		Workers:           *workers,
		MinCycles:         *minCycles,
		Route:             route,
		DurationSeconds:   durationSeconds,
		Deadline:          deadline,
		MinRuntimeSeconds: minRuntimeSeconds,
		CadenceSeconds:    cadenceSeconds,
		Budget: jobs.Budget{
			MaxCycles:     *maxCycles,
			MaxAttempts:   *maxAttempts,
			MaxModelCalls: *maxModelCalls,
			MaxTokens:     *maxTokens,
		},
		Authority: jobs.Authority{
			Mode:         jobs.AuthorityModeAllowList,
			Tools:        uniqueExplicitJobValues(toStrings(tools)),
			ReadRoots:    read,
			WriteRoots:   write,
			NetworkHosts: uniqueExplicitJobValues(toStrings(networkHosts)),
			Providers:    []string{route.ProviderID},
		},
		AutoStart: *autoStart,
	}
	if _, _, err := request.ResolveSchedule(time.Now().UTC()); err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	ctx, stop := processContext()
	defer stop()
	client, err := jobsGatewayClient(ctx, *gatewayURL)
	if err != nil {
		return err
	}
	response, err := client.CreateJob(ctx, request)
	if err != nil {
		return fmt.Errorf("create job %s: %w", request.JobID, err)
	}
	return printJobResponse(out, response, *jsonOut, true)
}

func printJobProviderTermsNotice(out io.Writer) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Provider terms: unattended jobs require an endpoint and plan that explicitly permit automation.")
	fmt.Fprintln(out, "WARNING: the built-in qwen route defaults to Qwen Token Plan Individual, whose current terms allow interactive programming/agent-tool use only and prohibit automated scripts, application backends, and non-interactive batch processing.")
}

func printQwenJobTermsWarning(out io.Writer) {
	fmt.Fprintln(out, "WARNING: provider=qwen defaults to Qwen Token Plan Individual. Its current terms allow interactive programming/agent-tool use only and prohibit automated scripts, application backends, and non-interactive batch processing. Do not run this job unattended unless your configured endpoint and provider terms explicitly permit automation. Token Plan concurrency: Lite 1-2, Standard 3-4, Pro 6-8 agents.")
}

func warnJobProviderTerms(route jobs.ExecutionRoute, out io.Writer) {
	if route.ProviderID == modelinfo.ProviderQwen {
		printQwenJobTermsWarning(out)
	}
}

func jobsListCommand(args []string, out io.Writer) error {
	fs := newJobFlagSet("jobs list", out)
	gatewayURL := fs.String("gateway", "", "gateway base URL; auto-discovered when omitted")
	jsonOut := fs.Bool("json", false, "print JSON")
	help, err := parseJobFlagSet(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: jobs list [-gateway URL] [-json]")
	}
	ctx, stop := processContext()
	defer stop()
	client, err := jobsGatewayClient(ctx, *gatewayURL)
	if err != nil {
		return err
	}
	responses, err := client.ListJobs(ctx)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJobJSON(out, gatewayapi.JobListResponse{Jobs: responses})
	}
	if len(responses) == 0 {
		fmt.Fprintln(out, "jobs: none")
		return nil
	}
	fmt.Fprintln(out, "billyharness jobs")
	for _, response := range responses {
		printJobListSummary(out, response)
	}
	return nil
}

func jobsShowCommand(args []string, out io.Writer) error {
	fs := newJobFlagSet("jobs show", out)
	gatewayURL := fs.String("gateway", "", "gateway base URL; auto-discovered when omitted")
	jsonOut := fs.Bool("json", false, "print JSON")
	help, err := parseJobFlagSet(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	jobID, err := singleJobID(fs, "jobs show [-gateway URL] [-json] JOB_ID")
	if err != nil {
		return err
	}
	ctx, stop := processContext()
	defer stop()
	client, err := jobsGatewayClient(ctx, *gatewayURL)
	if err != nil {
		return err
	}
	response, err := client.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	return printJobResponse(out, response, *jsonOut, true)
}

func jobsActionCommand(action string, args []string, out io.Writer) error {
	fs := newJobFlagSet("jobs "+action, out)
	gatewayURL := fs.String("gateway", "", "gateway base URL; auto-discovered when omitted")
	jsonOut := fs.Bool("json", false, "print JSON")
	help, err := parseJobFlagSet(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	jobID, err := singleJobID(fs, "jobs "+action+" [-gateway URL] [-json] JOB_ID")
	if err != nil {
		return err
	}
	ctx, stop := processContext()
	defer stop()
	client, err := jobsGatewayClient(ctx, *gatewayURL)
	if err != nil {
		return err
	}
	var response gatewayapi.JobResponse
	switch action {
	case "run":
		response, err = client.RunJob(ctx, jobID)
	case "pause":
		response, err = client.PauseJob(ctx, jobID)
	case "resume":
		response, err = client.ResumeJob(ctx, jobID)
	case "cancel":
		response, err = client.CancelJob(ctx, jobID)
	default:
		return fmt.Errorf("unsupported jobs action %q", action)
	}
	if err != nil {
		return err
	}
	return printJobResponse(out, response, *jsonOut, false)
}

func newJobFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}

func parseJobFlagSet(fs *flag.FlagSet, args []string) (bool, error) {
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	return false, nil
}

func singleJobID(fs *flag.FlagSet, usage string) (string, error) {
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return "", fmt.Errorf("usage: %s", usage)
	}
	return strings.TrimSpace(fs.Arg(0)), nil
}

func resolveJobGoal(flagGoal string, positional []string) (string, error) {
	flagGoal = strings.TrimSpace(flagGoal)
	positionalGoal := strings.TrimSpace(strings.Join(positional, " "))
	if flagGoal != "" && positionalGoal != "" {
		return "", errors.New("job goal must be provided either with -goal or as positional arguments, not both")
	}
	if flagGoal != "" {
		return flagGoal, nil
	}
	if positionalGoal == "" {
		return "", errors.New("job goal is required")
	}
	return positionalGoal, nil
}

func resolveJobRoute(providerValue, modelValue, thinkingValue, reasoningValue string) (jobs.ExecutionRoute, error) {
	providerExplicit := strings.TrimSpace(providerValue) != ""
	modelExplicit := strings.TrimSpace(modelValue) != ""
	providerID := modelinfo.NormalizeProvider(providerValue)
	modelID := modelinfo.NormalizeAlias(modelValue)
	configuredProvider := ""
	configuredModel := ""
	configuredThinking := ""
	configuredReasoning := ""
	if !providerExplicit || strings.TrimSpace(thinkingValue) == "" || strings.TrimSpace(reasoningValue) == "" {
		cfg, err := resolveRuntimeConfig()
		if err != nil {
			return jobs.ExecutionRoute{}, err
		}
		configuredProvider = cfg.Provider
		configuredModel = cfg.Model
		configuredThinking = cfg.Thinking
		configuredReasoning = cfg.ReasoningEffort
	}

	if providerID == "" {
		providerID = modelinfo.NormalizeProvider(configuredProvider)
	}
	if modelID == "" {
		if providerExplicit {
			provider := modelinfo.Provider(providerID)
			if len(provider.Models) == 0 {
				return jobs.ExecutionRoute{}, fmt.Errorf("model is required for provider %q", providerID)
			}
			modelID = provider.Models[0]
		} else {
			modelID = modelinfo.NormalizeAlias(configuredModel)
		}
	}
	if !providerExplicit && modelExplicit {
		providerID = modelinfo.ProviderForModel(modelID, configuredProvider)
	}
	providerID = modelinfo.NormalizeProvider(providerID)
	if providerID == "" || modelID == "" {
		return jobs.ExecutionRoute{}, errors.New("provider and model are required")
	}
	model := modelinfo.Lookup(modelID)
	provider := modelinfo.Provider(providerID)
	if providerExplicit && modelExplicit && model.Provider != "" && model.Provider != providerID && !provider.Custom {
		return jobs.ExecutionRoute{}, fmt.Errorf("model %q belongs to provider %q, not %q", modelID, model.Provider, providerID)
	}
	if strings.TrimSpace(thinkingValue) == "" {
		thinkingValue = configuredThinking
	}
	if strings.TrimSpace(reasoningValue) == "" {
		reasoningValue = configuredReasoning
	}
	route := jobs.ExecutionRoute{
		ProviderID:      providerID,
		ModelID:         modelID,
		Thinking:        strings.ToLower(strings.TrimSpace(thinkingValue)),
		ReasoningEffort: modelinfo.NormalizeReasoningEffort(reasoningValue),
	}
	if err := route.Validate(); err != nil {
		return jobs.ExecutionRoute{}, fmt.Errorf("route: %w", err)
	}
	if err := modelinfo.ValidateCapabilityPolicy(modelinfo.CapabilityPolicyRequest{
		Provider:           route.ProviderID,
		Model:              route.ModelID,
		Thinking:           route.Thinking,
		ReasoningEffort:    route.ReasoningEffort,
		AllowUnknownModels: provider.Custom,
	}); err != nil {
		return jobs.ExecutionRoute{}, err
	}
	return route, nil
}

func resolveJobWallClock(durationText, deadlineText string) (uint64, *time.Time, error) {
	durationText = strings.TrimSpace(durationText)
	deadlineText = strings.TrimSpace(deadlineText)
	if durationText != "" && deadlineText != "" {
		return 0, nil, errors.New("-duration and -deadline are mutually exclusive")
	}
	if deadlineText != "" {
		deadline, err := time.Parse(time.RFC3339, deadlineText)
		if err != nil {
			return 0, nil, fmt.Errorf("deadline must use RFC3339: %w", err)
		}
		deadline = deadline.UTC()
		return 0, &deadline, nil
	}
	if durationText == "" {
		durationText = defaultJobDuration.String()
	}
	duration, err := time.ParseDuration(durationText)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid duration %q: %w", durationText, err)
	}
	if duration <= 0 || duration%time.Second != 0 {
		return 0, nil, errors.New("duration must be a positive whole number of seconds")
	}
	return uint64(duration / time.Second), nil, nil
}

func resolveOptionalJobDuration(flagName, value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid -%s duration %q: %w", flagName, value, err)
	}
	if duration <= 0 || duration%time.Second != 0 {
		return 0, fmt.Errorf("-%s must be a positive whole number of seconds", flagName)
	}
	return uint64(duration / time.Second), nil
}

func resolveJobCadence(minRuntimeSeconds, cadenceSeconds, maxCycles uint64) (uint64, error) {
	if cadenceSeconds > 0 || minRuntimeSeconds == 0 {
		return cadenceSeconds, nil
	}
	if maxCycles < 2 {
		return 0, errors.New("-min-runtime requires -max-cycles of at least 2")
	}
	intervals := maxCycles - 1
	derived := minRuntimeSeconds / intervals
	if minRuntimeSeconds%intervals != 0 {
		derived++
	}
	return derived, nil
}

func normalizeExplicitJobRoots(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "*" {
			out = append(out, value)
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.Clean(absolute))
	}
	return uniqueExplicitJobValues(out), nil
}

func uniqueExplicitJobValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func toStrings(values repeatedJobFlag) []string {
	return append([]string(nil), values...)
}

func jobsGatewayClient(ctx context.Context, gatewayURL string) (*gatewayclient.Client, error) {
	gatewayURL = normalizeGatewayURL(gatewayURL)
	if gatewayURL == "" {
		cfg, err := resolveRuntimeConfig()
		if err != nil {
			return nil, err
		}
		discovered, ok := discoverGatewayURL(ctx, cfg)
		if !ok {
			return nil, fmt.Errorf("gateway unavailable: %s", gatewayapi.UnavailableHint(normalizeGatewayURL(cfg.GatewayAddr)))
		}
		gatewayURL = discovered
	}
	return gatewayclient.New(gatewayURL), nil
}

func printJobResponse(out io.Writer, response gatewayapi.JobResponse, jsonOut, detailed bool) error {
	if jsonOut {
		return writeJobJSON(out, response)
	}
	if detailed {
		printJobDetails(out, response)
	} else {
		printJobSummary(out, response)
	}
	return nil
}

func writeJobJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printJobSummary(out io.Writer, response gatewayapi.JobResponse) {
	state := response.State
	jobID := emptyJobValue(state.Spec.ID)
	status := emptyJobValue(string(state.Status))
	stage := currentJobStage(state)
	deadline := "-"
	if !state.Spec.Deadline.IsZero() {
		deadline = state.Spec.Deadline.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(out, "- %s status=%s active=%t preset=%s workers=%d cycle=%d stage=%s calls=%d tokens=%d deadline=%s",
		jobID,
		status,
		response.Active,
		emptyJobValue(state.Spec.Preset),
		state.Spec.Workers,
		state.Cycle,
		stage,
		state.Usage.ModelCalls,
		state.Usage.TotalTokens(),
		deadline,
	)
	if state.TerminalReason != "" {
		fmt.Fprintf(out, " reason=%s", state.TerminalReason)
	}
	if strings.TrimSpace(response.LastError) != "" {
		fmt.Fprintf(out, " error=%q", strings.TrimSpace(response.LastError))
	}
	fmt.Fprintln(out)
}

func printJobListSummary(out io.Writer, response gatewayapi.JobSummaryResponse) {
	deadline := "-"
	if !response.Deadline.IsZero() {
		deadline = response.Deadline.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(out, "- %s status=%s active=%t preset=%s cycle=%d calls=%d tokens=%d deadline=%s",
		emptyJobValue(response.ID), emptyJobValue(string(response.Status)), response.Active,
		emptyJobValue(response.Preset), response.Cycle, response.Usage.ModelCalls,
		response.Usage.TotalTokens(), deadline,
	)
	if response.TerminalReason != "" {
		fmt.Fprintf(out, " reason=%s", response.TerminalReason)
	}
	if strings.TrimSpace(response.LastError) != "" {
		fmt.Fprintf(out, " error=%q", strings.TrimSpace(response.LastError))
	}
	fmt.Fprintln(out)
}

func printJobDetails(out io.Writer, response gatewayapi.JobResponse) {
	state := response.State
	fmt.Fprintf(out, "job: %s\n", emptyJobValue(state.Spec.ID))
	fmt.Fprintf(out, "status: %s active=%t", emptyJobValue(string(state.Status)), response.Active)
	if state.TerminalReason != "" {
		fmt.Fprintf(out, " terminal_reason=%s", state.TerminalReason)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "goal: %s\n", emptyJobValue(strings.TrimSpace(state.Spec.Goal)))
	fmt.Fprintf(out, "workflow: preset=%s workers=%d cycle=%d stage=%s\n",
		emptyJobValue(state.Spec.Preset), state.Spec.Workers, state.Cycle, currentJobStage(state))
	fmt.Fprintf(out, "route: provider=%s model=%s thinking=%s reasoning=%s\n",
		emptyJobValue(state.Spec.Route.ProviderID),
		emptyJobValue(state.Spec.Route.ModelID),
		emptyJobValue(state.Spec.Route.Thinking),
		emptyJobValue(state.Spec.Route.ReasoningEffort),
	)
	deadline := "-"
	if !state.Spec.Deadline.IsZero() {
		deadline = state.Spec.Deadline.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(out, "deadline: %s\n", deadline)
	notBefore := "-"
	if !state.Spec.NotBeforeComplete.IsZero() {
		notBefore = state.Spec.NotBeforeComplete.UTC().Format(time.RFC3339)
	}
	nextWake := "manual-or-none"
	if !state.NextWakeAt.IsZero() {
		nextWake = state.NextWakeAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(out, "schedule: not_before_complete=%s cadence=%s next_wake=%s\n",
		notBefore, (time.Duration(state.Spec.CycleCadenceSeconds) * time.Second).String(), nextWake)
	fmt.Fprintf(out, "usage: cycles=%d min=%d max=%d attempts=%d/%d calls=%d/%d tokens=%d/%d input=%d output=%d\n",
		state.Usage.Cycles, state.Spec.EffectiveMinCycles(), state.Spec.Budget.MaxCycles,
		state.Usage.Attempts, state.Spec.Budget.MaxAttempts,
		state.Usage.ModelCalls, state.Spec.Budget.MaxModelCalls,
		state.Usage.TotalTokens(), state.Spec.Budget.MaxTokens,
		state.Usage.InputTokens, state.Usage.OutputTokens,
	)
	fmt.Fprintln(out, "cycle_floor: min is the earliest successful-completion cycle, not a wall-clock target")
	fmt.Fprintf(out, "history: attempts=%d completed_batches=%d artifacts=%d (paged by API)\n",
		response.History.Attempts, response.History.CompletedBatches, response.History.Artifacts)
	fmt.Fprintf(out, "authority: tools=%s read_roots=%s write_roots=%s network_hosts=%s providers=%s\n",
		jobValues(state.Spec.Authority.Tools),
		jobValues(state.Spec.Authority.ReadRoots),
		jobValues(state.Spec.Authority.WriteRoots),
		jobValues(state.Spec.Authority.NetworkHosts),
		jobValues(state.Spec.Authority.Providers),
	)
	if strings.TrimSpace(response.LastError) != "" {
		fmt.Fprintf(out, "last_error: %s\n", strings.TrimSpace(response.LastError))
	}
	if strings.TrimSpace(state.FinalResult) != "" {
		fmt.Fprintln(out, "final_result:")
		fmt.Fprintln(out, state.FinalResult)
	}
}

func currentJobStage(state jobs.JobState) string {
	if state.Status.IsTerminal() {
		return "terminal"
	}
	if state.CurrentBatch != nil && strings.TrimSpace(state.CurrentBatch.StageID) != "" {
		return state.CurrentBatch.StageID
	}
	if state.NextStageIndex >= 0 && state.NextStageIndex < len(state.Spec.Workflow.StageOrder) {
		return state.Spec.Workflow.StageOrder[state.NextStageIndex]
	}
	return "-"
}

func emptyJobValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func jobValues(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ",")
}
