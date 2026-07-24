// Package jobagent adapts durable jobruntime invocations to the isolated
// in-process agent runtime.
//
// Tool-enabled invocations use an isolated, capability-filtered snapshot of
// the native registry. Ambient MCP, memory, skills, hooks, helper providers,
// caches, and unsafe process/external tools are never inherited.
package jobagent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/jobruntime"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/runtimehost"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

var (
	// ErrUnsupportedAuthority reports an effective authority that cannot be
	// represented by the current isolated agent/tool capability layer.
	ErrUnsupportedAuthority = errors.New("job authority is not enforceable by the isolated capability adapter")
	// ErrUnsupportedRoute reports a route that cannot be pinned without
	// silently changing provider/model semantics or losing a hard output cap.
	ErrUnsupportedRoute = errors.New("job execution route is not supported")
	// ErrUsageAccounting reports missing, inconsistent, or out-of-reservation
	// provider usage. The returned InvocationResult still carries the factual
	// usage observed before the error whenever the provider reported it.
	ErrUsageAccounting = errors.New("job provider usage accounting failed")
)

// Alibaba documents that Qwen's max_completion_tokens limit can differ from
// actual output usage by up to ten tokens. Keep that provider-side tolerance
// inside the durable reservation instead of letting it escape the hard cap.
const qwenCompletionTokenTolerance uint64 = 10

// BindingResolver resolves credentials and endpoints for one persisted route.
// Implementations must not take route information from model output. Adapter
// overwrites all execution-relevant model fields with the persisted route and
// invocation limits before constructing a fresh provider.
type BindingResolver interface {
	ResolveBinding(context.Context, jobs.ExecutionRoute) (config.ProviderBinding, error)
}

// BindingResolverFunc adapts a function to BindingResolver.
type BindingResolverFunc func(context.Context, jobs.ExecutionRoute) (config.ProviderBinding, error)

func (f BindingResolverFunc) ResolveBinding(ctx context.Context, route jobs.ExecutionRoute) (config.ProviderBinding, error) {
	return f(ctx, route)
}

// StaticBinding returns a resolver for a single provider binding. It is useful
// when one runner is already scoped to one configured provider. Route mismatch
// is still rejected by Adapter.Invoke.
func StaticBinding(binding config.ProviderBinding) BindingResolver {
	return BindingResolverFunc(func(context.Context, jobs.ExecutionRoute) (config.ProviderBinding, error) {
		return binding, nil
	})
}

// ProviderFactory constructs a fresh provider from the already-pinned and
// bounded binding. Reusing an ambient provider is intentionally unsupported:
// it could retain failover or a different construction-time output limit.
type ProviderFactory func(config.ProviderBinding) (provider.Provider, error)

type Option func(*options) error

type options struct {
	providerFactory ProviderFactory
	toolRegistry    *tools.Registry
}

// WithRegistry enables explicitly authorized structured native tools.
// The adapter always takes a fresh isolated capability snapshot; this does not
// expose the registry's ambient MCP catalog, instructions, memory, skills,
// helpers, cache, or tools outside the invocation allowlist.
func WithRegistry(registry *tools.Registry) Option {
	return func(options *options) error {
		if registry == nil {
			return errors.New("job agent tool registry is required")
		}
		options.toolRegistry = registry
		return nil
	}
}

// WithProviderFactory replaces provider.NewFromBinding. It exists primarily
// for deterministic fakes and must honor the supplied binding exactly.
func WithProviderFactory(factory ProviderFactory) Option {
	return func(options *options) error {
		if factory == nil {
			return errors.New("job agent provider factory is required")
		}
		options.providerFactory = factory
		return nil
	}
}

// Adapter executes one bounded isolated agent attempt.
type Adapter struct {
	resolver        BindingResolver
	providerFactory ProviderFactory
	toolRegistry    *tools.Registry
}

var _ jobruntime.Invoker = (*Adapter)(nil)

func New(resolver BindingResolver, opts ...Option) (*Adapter, error) {
	if resolver == nil {
		return nil, errors.New("job agent binding resolver is required")
	}
	resolved := options{providerFactory: provider.NewFromBinding}
	for _, option := range opts {
		if option == nil {
			continue
		}
		if err := option(&resolved); err != nil {
			return nil, err
		}
	}
	return &Adapter{resolver: resolver, providerFactory: resolved.providerFactory, toolRegistry: resolved.toolRegistry}, nil
}

func (a *Adapter) Invoke(ctx context.Context, invocation jobruntime.Invocation) (jobruntime.InvocationResult, error) {
	if a == nil || a.resolver == nil || a.providerFactory == nil {
		return jobruntime.InvocationResult{}, preflightFailure(errors.New("job agent adapter is not initialized"))
	}
	if err := invocation.Validate(); err != nil {
		return jobruntime.InvocationResult{}, preflightFailure(fmt.Errorf("validate job invocation: %w", err))
	}
	capabilities, activeRegistry, _, err := a.capabilitiesForInvocation(invocation)
	if err != nil {
		return jobruntime.InvocationResult{}, preflightFailure(err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return jobruntime.InvocationResult{}, interruptedPreflight(err)
	}
	if !invocation.Deadline.After(time.Now()) {
		return jobruntime.InvocationResult{}, interruptedPreflight(context.DeadlineExceeded)
	}

	base, err := a.resolver.ResolveBinding(ctx, invocation.Route)
	if err != nil {
		return jobruntime.InvocationResult{}, preflightFailure(fmt.Errorf("resolve binding for provider %q: %w", invocation.Route.ProviderID, err))
	}
	if err := ctx.Err(); err != nil {
		return jobruntime.InvocationResult{}, interruptedPreflight(err)
	}
	if !invocation.Deadline.After(time.Now()) {
		return jobruntime.InvocationResult{}, interruptedPreflight(context.DeadlineExceeded)
	}
	binding, err := pinBinding(base, invocation)
	if err != nil {
		return jobruntime.InvocationResult{}, preflightFailure(err)
	}
	accessMode := config.AccessModePlan
	autoApproveDangerous := false
	workspaceRoots := capabilities.ReadRoots()
	if invocation.Writer {
		accessMode = config.AccessModeBuild
		autoApproveDangerous = true
		workspaceRoots = capabilities.WriteRoots()
	}
	settings := runtimehost.Settings{
		ProviderBinding: binding,
		Runtime:         binding.Limits,
		ToolPolicy: config.ToolPolicySettings{
			AccessMode:           accessMode,
			AutoApproveDangerous: autoApproveDangerous,
			MemoryEnabled:        false,
			WorkspaceRoots:       workspaceRoots,
		},
	}
	bootstrapAgent := runtimehost.NewAgent(
		settings,
		nil,
		activeRegistry,
		runtimehost.WithContextMode(protocol.ContextModeIsolated),
		runtimehost.WithRunCapabilities(capabilities),
	)
	if bootstrapAgent == nil {
		return jobruntime.InvocationResult{}, preflightFailure(errors.New("construct isolated job prompt context: nil agent"))
	}
	initialMessages := bootstrapAgent.InitialMessages()
	var toolSpecs []protocol.ToolSpec
	if activeRegistry != nil {
		toolSpecs = activeRegistry.SnapshotWithToolPolicyAndCapabilities(ctx, settings.ToolPolicy, capabilities).Specs()
	}
	promptBudget, err := promptByteBudget(binding, invocation, initialMessages, toolSpecs)
	if err != nil {
		return jobruntime.InvocationResult{}, preflightFailure(err)
	}
	prompt, err := buildPromptWithinBudget(invocation, promptBudget)
	if err != nil {
		return jobruntime.InvocationResult{}, preflightFailure(fmt.Errorf("build job prompt: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return jobruntime.InvocationResult{}, interruptedPreflight(err)
	}
	if !invocation.Deadline.After(time.Now()) {
		return jobruntime.InvocationResult{}, interruptedPreflight(context.DeadlineExceeded)
	}

	// Prompt-fit is a true preflight: a route whose mandatory envelope cannot
	// fit never constructs or calls a provider.
	prov, err := a.providerFactory(binding)
	if err != nil {
		return jobruntime.InvocationResult{}, preflightFailure(fmt.Errorf("construct pinned provider %q: %w", invocation.Route.ProviderID, err))
	}
	if prov == nil {
		return jobruntime.InvocationResult{}, preflightFailure(errors.New("construct pinned provider: factory returned nil"))
	}
	if err := ctx.Err(); err != nil {
		return jobruntime.InvocationResult{}, interruptedPreflight(err)
	}
	if !invocation.Deadline.After(time.Now()) {
		return jobruntime.InvocationResult{}, interruptedPreflight(context.DeadlineExceeded)
	}

	runCtx, cancel := context.WithDeadline(ctx, invocation.Deadline)
	defer cancel()
	collector := newUsageCollector(invocation.Route, invocation.Limits, cancel, len(toolSpecs))
	agent := runtimehost.NewAgent(
		settings,
		prov,
		activeRegistry,
		runtimehost.WithContextMode(protocol.ContextModeIsolated),
		runtimehost.WithRunCapabilities(capabilities),
	)
	if agent == nil {
		return jobruntime.InvocationResult{}, preflightFailure(errors.New("construct isolated job agent: nil agent"))
	}
	messages := append(initialMessages, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	messages, runErr := agent.RunMessages(runCtx, messages, collector.Emit)
	usage, accountingErr := collector.Result()
	result := jobruntime.InvocationResult{Usage: usage}
	if runErr != nil || accountingErr != nil {
		dispatch, usageProvenance := collector.Provenance()
		cause := errors.Join(runErr, accountingErr)
		if providerErr, rejected := providerRejectedBeforeGeneration(cause); rejected &&
			dispatch == jobruntime.DispatchDispatched && collector.RejectionPrecededNoGeneration() {
			// A provider HTTP rejection (429/5xx/auth/bad request/context)
			// happens before that response stream. It is no-generation for the
			// whole invocation only when this is the first and sole model call;
			// after any earlier round, tool effects may already have happened and
			// the incomplete invocation must stay unknown/fail-closed.
			if result.Usage.ModelCalls == 0 {
				result.Usage.ModelCalls = 1
			}
			result.Usage.InputTokens = 0
			result.Usage.OutputTokens = 0
			if providerErr.Retryable() {
				return result, jobruntime.NewTransientInvocationFailure(
					cause, dispatch, jobruntime.UsageNoGeneration, providerErr.RetryAfter,
				)
			}
			return result, jobruntime.NewInvocationFailure(cause, dispatch, jobruntime.UsageNoGeneration)
		}
		if providerErr, retryable := retryableProviderFailure(cause); retryable &&
			dispatch == jobruntime.DispatchDispatched && usageProvenance == jobruntime.UsageUnknown {
			// DNS/reset/stream failures cannot prove whether generation or billing
			// occurred. Preserve that uncertainty and let the runtime burn the full
			// reservation; only a read-only attempt may then be retried.
			return result, jobruntime.NewTransientInvocationFailure(
				cause, dispatch, usageProvenance, providerErr.RetryAfter,
			)
		}
		return result, jobruntime.NewInvocationFailure(cause, dispatch, usageProvenance)
	}

	answer, err := finalAssistantResult(messages)
	if err != nil {
		return result, jobruntime.NewInvocationFailure(err, jobruntime.DispatchDispatched, jobruntime.UsageFactual)
	}
	if len(answer) > jobruntime.MaxInvocationResultBytes {
		result.Status = jobs.AttemptStatusFailed
		result.Error = fmt.Sprintf("assistant result exceeds limit %d", jobruntime.MaxInvocationResultBytes)
		if validateErr := result.ValidateFor(invocation); validateErr != nil {
			return result, jobruntime.NewInvocationFailure(fmt.Errorf("normalize oversized assistant result: %w", validateErr), jobruntime.DispatchDispatched, jobruntime.UsageFactual)
		}
		return result, nil
	}
	result.Result = answer
	if invocation.Kind == jobruntime.InvocationKindSupervisor {
		proposal, parseErr := jobruntime.ParseSupervisorProposal([]byte(answer), invocation.AllowedNextRoleIDs)
		if parseErr != nil {
			result.Status = jobs.AttemptStatusFailed
			result.Error = compactError("invalid supervisor proposal: "+parseErr.Error(), jobruntime.MaxInvocationErrorBytes)
			if validateErr := result.ValidateFor(invocation); validateErr != nil {
				return result, jobruntime.NewInvocationFailure(fmt.Errorf("normalize invalid supervisor proposal: %w", validateErr), jobruntime.DispatchDispatched, jobruntime.UsageFactual)
			}
			return result, nil
		}
		result.Proposal = &proposal
	}
	result.Status = jobs.AttemptStatusSucceeded
	if err := result.ValidateFor(invocation); err != nil {
		return result, jobruntime.NewInvocationFailure(fmt.Errorf("validate job agent result: %w", err), jobruntime.DispatchDispatched, jobruntime.UsageFactual)
	}
	return result, nil
}

func retryableProviderFailure(err error) (*provider.ProviderError, bool) {
	var providerErr *provider.ProviderError
	if !errors.As(err, &providerErr) || providerErr == nil || !providerErr.Retryable() {
		return nil, false
	}
	return providerErr, true
}

func providerRejectedBeforeGeneration(err error) (*provider.ProviderError, bool) {
	var providerErr *provider.ProviderError
	if !errors.As(err, &providerErr) || providerErr == nil {
		return nil, false
	}
	switch providerErr.Kind {
	case provider.ErrorRateLimit, provider.ErrorAuth,
		provider.ErrorBadRequest, provider.ErrorContextOverflow:
		if providerErr.Status == 0 {
			return nil, false
		}
		return providerErr, true
	default:
		return nil, false
	}
}

func preflightFailure(err error) error {
	return jobruntime.NewFatalPreflightFailure(err)
}

// interruptedPreflight is deliberately retryable. A daemon shutdown, operator
// cancellation, or deadline racing with local setup proves that no provider
// dispatch or writer side effect occurred, but it does not prove that the
// immutable invocation is invalid. Persisting it as abandoned lets recovery
// resume the same durable work item after restart.
func interruptedPreflight(err error) error {
	return jobruntime.NewInvocationFailure(err, jobruntime.DispatchNotDispatched, jobruntime.UsageUnknown)
}

func validateProviderOnlyAuthority(authority jobs.Authority) error {
	if authority.Mode != jobs.AuthorityModeAllowList || len(authority.Providers) != 1 {
		return fmt.Errorf("%w: exactly one provider grant is required", ErrUnsupportedAuthority)
	}
	if len(authority.Tools) != 0 || len(authority.ReadRoots) != 0 || len(authority.WriteRoots) != 0 || len(authority.NetworkHosts) != 0 {
		return fmt.Errorf(
			"%w: tools=%d read_roots=%d write_roots=%d network_hosts=%d; use a capability-aware adapter",
			ErrUnsupportedAuthority,
			len(authority.Tools),
			len(authority.ReadRoots),
			len(authority.WriteRoots),
			len(authority.NetworkHosts),
		)
	}
	return nil
}

func (a *Adapter) capabilitiesForInvocation(invocation jobruntime.Invocation) (tools.RunCapabilities, *tools.Registry, int, error) {
	authority := invocation.Authority
	if authority.Mode != jobs.AuthorityModeAllowList || len(authority.Providers) != 1 {
		return tools.RunCapabilities{}, nil, 0, fmt.Errorf("%w: exactly one provider grant is required", ErrUnsupportedAuthority)
	}
	if len(authority.Tools) == 0 {
		if err := validateProviderOnlyAuthority(authority); err != nil {
			return tools.RunCapabilities{}, nil, 0, err
		}
		return tools.RunCapabilities{}, nil, 0, nil
	}
	if a.toolRegistry == nil {
		return tools.RunCapabilities{}, nil, 0, fmt.Errorf("%w: tool authority requires an available native tool registry", ErrUnsupportedAuthority)
	}

	allowedTools := make([]string, 0, len(authority.Tools))
	for _, name := range authority.Tools {
		if !invocation.Writer && tools.IsDurableJobWriteTool(name) {
			continue
		}
		allowedTools = append(allowedTools, name)
	}
	if len(allowedTools) == 0 {
		if len(authority.ReadRoots) != 0 || len(authority.NetworkHosts) != 0 || len(authority.WriteRoots) != 0 {
			return tools.RunCapabilities{}, nil, 0, fmt.Errorf("%w: resource grants remain after role filtering removed every allowed tool", ErrUnsupportedAuthority)
		}
		return tools.RunCapabilities{}, nil, 0, nil
	}
	prefixes, err := networkHostsToHTTPSPrefixes(authority.NetworkHosts)
	if err != nil {
		return tools.RunCapabilities{}, nil, 0, fmt.Errorf("%w: %v", ErrUnsupportedAuthority, err)
	}
	capabilities, err := a.toolRegistry.NewDurableJobRunCapabilities(
		allowedTools,
		authority.ReadRoots,
		authority.WriteRoots,
		prefixes,
	)
	if err != nil {
		return tools.RunCapabilities{}, nil, 0, fmt.Errorf("%w: %v", ErrUnsupportedAuthority, err)
	}
	return capabilities, a.toolRegistry, len(allowedTools), nil
}

func networkHostsToHTTPSPrefixes(hosts []string) ([]string, error) {
	if len(hosts) == 1 && hosts[0] == "*" {
		return []string{"*"}, nil
	}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if strings.ContainsAny(host, "/?#@*") {
			return nil, fmt.Errorf("network host %q must be an exact host or host:port without URL syntax", host)
		}
		out = append(out, "https://"+host+"/")
	}
	return out, nil
}

func pinBinding(base config.ProviderBinding, invocation jobruntime.Invocation) (config.ProviderBinding, error) {
	route := invocation.Route
	canonicalProvider := modelinfo.NormalizeProvider(route.ProviderID)
	if canonicalProvider == "" || canonicalProvider != route.ProviderID {
		return config.ProviderBinding{}, fmt.Errorf("%w: persisted provider %q is not canonical (canonical %q)", ErrUnsupportedRoute, route.ProviderID, canonicalProvider)
	}
	resolvedBaseProvider := modelinfo.NormalizeProvider(base.Provider.Provider)
	if resolvedBaseProvider != canonicalProvider {
		return config.ProviderBinding{}, fmt.Errorf(
			"%w: binding resolver returned provider %q for persisted provider %q",
			ErrUnsupportedRoute,
			base.Provider.Provider,
			route.ProviderID,
		)
	}
	resolvedModelProvider := modelinfo.ProviderForModel(route.ModelID, canonicalProvider)
	if resolvedModelProvider != canonicalProvider {
		return config.ProviderBinding{}, fmt.Errorf(
			"%w: model %q resolves to provider %q, not persisted provider %q",
			ErrUnsupportedRoute,
			route.ModelID,
			resolvedModelProvider,
			canonicalProvider,
		)
	}
	if invocation.Limits.ModelCalls > uint64(math.MaxInt) || invocation.Limits.MaxOutputTokens > uint64(math.MaxInt) {
		return config.ProviderBinding{}, fmt.Errorf("%w: invocation limits exceed platform int range", ErrUnsupportedRoute)
	}
	providerOutputLimit := invocation.Limits.MaxOutputTokens
	// The provider-neutral runtime reserves against its own hard ceiling. A
	// concrete model may advertise a smaller output window; pin the transport
	// to the stricter cap instead of rejecting an otherwise valid route. The
	// durable reservation remains the outer accounting bound.
	if modelLimit := modelinfo.Lookup(route.ModelID).MaxOutputTokens; modelLimit > 0 && providerOutputLimit > uint64(modelLimit) {
		providerOutputLimit = uint64(modelLimit)
	}
	// Unknown/custom model metadata cannot supply a capability ceiling. Respect
	// any positive configured binding cap instead of replacing a working 4k/8k
	// custom route with the provider-neutral 64k reservation default.
	if modelinfo.Lookup(route.ModelID).MaxOutputTokens <= 0 {
		for _, configuredLimit := range []int{base.Model.MaxTokens, base.Limits.MaxTokens} {
			if configuredLimit > 0 && providerOutputLimit > uint64(configuredLimit) {
				providerOutputLimit = uint64(configuredLimit)
			}
		}
	}
	if canonicalProvider == modelinfo.ProviderQwen && qwenThinkingEnabled(route.Thinking) {
		if providerOutputLimit <= qwenCompletionTokenTolerance {
			return config.ProviderBinding{}, fmt.Errorf(
				"%w: Qwen reasoning max output tokens must exceed provider tolerance %d",
				ErrUnsupportedRoute,
				qwenCompletionTokenTolerance,
			)
		}
		providerOutputLimit -= qwenCompletionTokenTolerance
	}

	base.Provider.Provider = canonicalProvider
	base.Model.Model = route.ModelID
	base.Model.Thinking = route.Thinking
	base.Model.ReasoningEffort = route.ReasoningEffort
	base.Model.MaxTokens = int(providerOutputLimit)
	base.Limits.MaxTokens = int(providerOutputLimit)
	base.Limits.MaxToolRounds = int(invocation.Limits.ModelCalls)
	base.Limits.MaxParallelTools = 1
	base.Limits.ProviderMaxRetries = 0
	return base, nil
}

func qwenThinkingEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "enabled", "on", "true", "1", "yes", "":
		return true
	default:
		return false
	}
}

func finalAssistantResult(messages []protocol.Message) (string, error) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != protocol.RoleAssistant {
			continue
		}
		if len(messages[index].ToolCalls) != 0 {
			return "", errors.New("isolated job run ended on a tool-calling assistant message")
		}
		if strings.TrimSpace(messages[index].Content) == "" {
			return "", errors.New("isolated job run returned an empty final assistant result")
		}
		return messages[index].Content, nil
	}
	return "", errors.New("isolated job run returned no final assistant message")
}

func compactError(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return value
}
