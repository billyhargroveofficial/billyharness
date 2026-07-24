package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/jobagent"
	"github.com/billyhargroveofficial/billyharness/internal/jobruntime"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobservice"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

const durableJobShutdownTimeout = 30 * time.Second

const defaultDurableJobMaxConcurrency = 1

// The durable scheduler gets only structured, capability-enforceable native
// tools. Keep process execution, diagnostics, MCP, memory, skills, user-input,
// and ambient discovery out of this list.
var durableJobStructuredToolCandidates = []string{
	"fs_edit_file",
	"fs_find_files",
	"fs_glob",
	"fs_grep",
	"fs_list",
	"fs_make_dir",
	"fs_read_file",
	"fs_search",
	"fs_write_file",
	"time_now",
	"web_crawl",
	"web_extract",
	"web_fetch",
	"web_search",
}

type durableJobStack struct {
	store                    *jobstore.FileStore
	manager                  *jobservice.Manager
	authority                jobs.Authority
	invoker                  *jobruntime.LimitedInvoker
	maxConcurrentInvocations int
}

type durableJobStackOptions struct {
	maxConcurrentInvocations int
}

type durableJobStackOption func(*durableJobStackOptions) error

func withDurableJobMaxConcurrency(maxConcurrent int) durableJobStackOption {
	return func(options *durableJobStackOptions) error {
		if maxConcurrent < 1 || maxConcurrent > jobruntime.MaxLimitedInvokerConcurrency {
			return fmt.Errorf("durable job concurrency must be between 1 and %d", jobruntime.MaxLimitedInvokerConcurrency)
		}
		options.maxConcurrentInvocations = maxConcurrent
		return nil
	}
}

func defaultDurableJobStoreDir() string {
	return filepath.Join(config.BillyHomeDir(), "jobs")
}

func newDurableJobStack(ctx context.Context, cfg config.Config, root string, registry *tools.Registry, optionFns ...durableJobStackOption) (*durableJobStack, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("durable job store path is required")
	}
	stackOptions := durableJobStackOptions{maxConcurrentInvocations: defaultDurableJobMaxConcurrency}
	for _, option := range optionFns {
		if option == nil {
			continue
		}
		if err := option(&stackOptions); err != nil {
			return nil, err
		}
	}
	store, err := jobstore.NewFileStore(root, jobstore.Options{})
	if err != nil {
		return nil, fmt.Errorf("open durable job store: %w", err)
	}
	storeOwned := true
	defer func() {
		if storeOwned {
			_ = store.Close()
		}
	}()

	authority, err := durableJobServerAuthority(cfg, registry, store.ProtectedRoots())
	if err != nil {
		return nil, err
	}
	adapter, err := jobagent.New(durableJobBindingResolver(cfg), jobagent.WithRegistry(registry))
	if err != nil {
		return nil, fmt.Errorf("construct durable job agent adapter: %w", err)
	}
	limitedInvoker, err := jobruntime.NewLimitedInvoker(adapter, stackOptions.maxConcurrentInvocations)
	if err != nil {
		return nil, fmt.Errorf("construct durable job concurrency limiter: %w", err)
	}
	runner, err := jobruntime.NewRunner(store, limitedInvoker, jobruntime.RunnerOptions{ServerAuthority: authority})
	if err != nil {
		return nil, fmt.Errorf("construct durable job runner: %w", err)
	}
	manager, err := jobservice.New(store, runner)
	if err != nil {
		return nil, fmt.Errorf("construct durable job manager: %w", err)
	}
	stack := &durableJobStack{
		store:                    store,
		manager:                  manager,
		authority:                authority,
		invoker:                  limitedInvoker,
		maxConcurrentInvocations: stackOptions.maxConcurrentInvocations,
	}
	if err := manager.Recover(ctx); err != nil {
		// Recover may have admitted earlier RUNNING jobs before encountering a
		// later corrupt one. Stop those loops before releasing the store lock.
		shutdownErr := manager.Shutdown(context.Background())
		if shutdownErr == nil {
			shutdownErr = store.Close()
			storeOwned = false
		}
		return nil, errors.Join(fmt.Errorf("recover durable jobs: %w", err), shutdownErr)
	}
	storeOwned = false
	return stack, nil
}

// shutdown stops Manager before releasing the FileStore. The boolean reports
// whether it is safe for serve to close the shared tool registry afterwards.
func (stack *durableJobStack) shutdown(ctx context.Context) (bool, error) {
	if stack == nil {
		return true, nil
	}
	if stack.manager != nil {
		if err := stack.manager.Shutdown(ctx); err != nil {
			return false, fmt.Errorf("shut down durable job manager: %w", err)
		}
	}
	if stack.store != nil {
		if err := stack.store.Close(); err != nil {
			return true, fmt.Errorf("close durable job store: %w", err)
		}
	}
	return true, nil
}

func durableJobBindingResolver(base config.Config) jobagent.BindingResolver {
	return jobagent.BindingResolverFunc(func(_ context.Context, route jobs.ExecutionRoute) (config.ProviderBinding, error) {
		if err := route.Validate(); err != nil {
			return config.ProviderBinding{}, fmt.Errorf("validate persisted execution route: %w", err)
		}
		routeProvider := modelinfo.NormalizeProvider(route.ProviderID)
		baseProvider := modelinfo.NormalizeProvider(base.Provider)
		routeInfo := modelinfo.Provider(routeProvider)
		var routed config.Config
		switch {
		case routeInfo.Custom:
			if baseProvider != routeProvider || !modelinfo.Provider(baseProvider).Custom || strings.TrimSpace(base.BaseURL) == "" {
				return config.ProviderBinding{}, fmt.Errorf(
					"custom provider %q is not the gateway's explicitly configured custom binding",
					routeProvider,
				)
			}
			routed = base
		case baseProvider == routeProvider:
			routed = base
		default:
			// Start a different built-in provider from provider-owned defaults so
			// an explicit endpoint/key override for the gateway's primary provider
			// cannot leak into another route. Provider credentials remain external
			// references and are resolved by that provider's normal env/auth path.
			routed = config.BuiltIn()
			routed.CredentialFile = base.CredentialFile
			routed.CodexAuthFile = base.CodexAuthFile
			routed.CodexRefreshURL = base.CodexRefreshURL
			routed.CodexAuthAPIBaseURL = base.CodexAuthAPIBaseURL
			routed.CodexClientID = base.CodexClientID
			routed.CodexOriginator = base.CodexOriginator
			routed.RequestTimeout = base.RequestTimeout
			routed.StreamIdleTimeout = base.StreamIdleTimeout
		}
		routed.Provider = routeProvider
		routed.Model = route.ModelID
		routed.Thinking = route.Thinking
		routed.ReasoningEffort = route.ReasoningEffort
		routed.ProviderMaxRetries = 0
		routed.ApplyModelProviderDefaults()
		binding := routed.ProviderBinding()
		// Reassert persisted values after config projection. Adapter validates
		// provider/model compatibility and applies its per-attempt token caps.
		binding.Provider.Provider = route.ProviderID
		binding.Model.Model = route.ModelID
		binding.Model.Thinking = route.Thinking
		binding.Model.ReasoningEffort = route.ReasoningEffort
		binding.Limits.ProviderMaxRetries = 0
		return binding, nil
	})
}

func durableJobServerAuthority(cfg config.Config, registry *tools.Registry, protectedRoots []string) (jobs.Authority, error) {
	workspaceRoots, err := durableJobWorkspaceRoots(cfg.WorkspaceRoots, protectedRoots)
	if err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("durable job workspace authority: %w", err)
	}
	authority := jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        durableJobStructuredTools(registry),
		ReadRoots:    append([]string(nil), workspaceRoots...),
		WriteRoots:   append([]string(nil), workspaceRoots...),
		NetworkHosts: []string{"*"},
		Providers:    durableJobProviderIDs(cfg),
	}
	if err := authority.Validate(); err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("validate durable job server authority: %w", err)
	}
	return authority, nil
}

func durableJobProviderIDs(cfg config.Config) []string {
	providers := make([]string, 0, len(modelinfo.Providers())+1)
	for _, info := range modelinfo.Providers() {
		if info.ID == "" || info.Custom {
			continue
		}
		providers = append(providers, info.ID)
	}
	providers = append(providers, modelinfo.ProviderMock)
	baseProvider := modelinfo.NormalizeProvider(cfg.Provider)
	if baseProvider != "" && modelinfo.Provider(baseProvider).Custom && strings.TrimSpace(cfg.BaseURL) != "" {
		providers = append(providers, baseProvider)
	}
	sort.Strings(providers)
	unique := providers[:0]
	for _, providerID := range providers {
		if len(unique) == 0 || unique[len(unique)-1] != providerID {
			unique = append(unique, providerID)
		}
	}
	return unique
}

func durableJobStructuredTools(registry *tools.Registry) []string {
	if registry == nil {
		return nil
	}
	out := make([]string, 0, len(durableJobStructuredToolCandidates))
	for _, name := range durableJobStructuredToolCandidates {
		risk, ok := registry.Risk(name)
		if !ok {
			continue
		}
		switch protocol.RiskClass(risk) {
		case protocol.RiskLocalRead, protocol.RiskLocalWrite, protocol.RiskNetworkRead:
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func durableJobWorkspaceRoots(configured, protected []string) ([]string, error) {
	protectedCanonical := make([]string, 0, len(protected))
	for _, root := range protected {
		canonical, err := canonicalJobAuthorityRoot(root)
		if err != nil {
			return nil, fmt.Errorf("protected root %q: %w", root, err)
		}
		protectedCanonical = append(protectedCanonical, canonical)
	}
	var roots []string
	for _, root := range configured {
		if strings.TrimSpace(root) == "*" {
			return nil, errors.New("workspace root wildcard is not supported for durable jobs")
		}
		canonical, err := canonicalJobAuthorityRoot(root)
		if err != nil {
			return nil, fmt.Errorf("workspace root %q: %w", root, err)
		}
		overlapsProtected := false
		for _, denied := range protectedCanonical {
			if jobAuthorityPathsOverlap(canonical, denied) {
				overlapsProtected = true
				break
			}
		}
		if !overlapsProtected {
			roots = append(roots, canonical)
		}
	}
	sort.Strings(roots)
	unique := roots[:0]
	for _, root := range roots {
		covered := false
		for _, parent := range unique {
			if jobAuthorityRootWithin(root, parent) {
				covered = true
				break
			}
		}
		if !covered {
			unique = append(unique, root)
		}
	}
	return append([]string(nil), unique...), nil
}

func canonicalJobAuthorityRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if evaluated, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(evaluated)
	}
	return absolute, nil
}

func jobAuthorityPathsOverlap(left, right string) bool {
	return jobAuthorityRootWithin(left, right) || jobAuthorityRootWithin(right, left)
}

func jobAuthorityRootWithin(child, parent string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
