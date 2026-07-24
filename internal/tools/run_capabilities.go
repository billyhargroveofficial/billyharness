package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/webtools"
)

const maxAllowedTools = 64

type runCapabilitiesContextKey struct{}

// RunCapabilities is an immutable, optional per-run reduction of the registry
// and native web transport. Zero values preserve the registry's legacy surface.
type RunCapabilities struct {
	scope                  string
	allowedTools           []string
	allowedToolSet         map[string]struct{}
	allowedURLPrefixes     []string
	allowedURLPathPrefixes []string
	readRoots              []string
	writeRoots             []string
	networkUnrestricted    bool
}

type CapabilityAttestation struct {
	Scope                    string
	AllowedToolsCount        int
	AllowedToolsSHA256       string
	AllowedURLPrefixesCount  int
	AllowedURLPrefixesSHA256 string
	ReadRootsCount           int
	ReadRootsSHA256          string
	WriteRootsCount          int
	WriteRootsSHA256         string
}

type durableJobToolKind uint8

const (
	durableJobToolStateless durableJobToolKind = iota + 1
	durableJobToolLocalRead
	durableJobToolLocalWrite
	durableJobToolNetworkRead
)

var durableJobToolKinds = map[string]durableJobToolKind{
	"time_now":      durableJobToolStateless,
	"fs_read_file":  durableJobToolLocalRead,
	"fs_list":       durableJobToolLocalRead,
	"fs_search":     durableJobToolLocalRead,
	"fs_grep":       durableJobToolLocalRead,
	"fs_glob":       durableJobToolLocalRead,
	"fs_find_files": durableJobToolLocalRead,
	"fs_write_file": durableJobToolLocalWrite,
	"fs_edit_file":  durableJobToolLocalWrite,
	"fs_make_dir":   durableJobToolLocalWrite,
	"web_search":    durableJobToolNetworkRead,
	"web_fetch":     durableJobToolNetworkRead,
	"web_extract":   durableJobToolNetworkRead,
	"web_crawl":     durableJobToolNetworkRead,
}

func (r *Registry) NewRunCapabilities(scope string, allowedTools, allowedURLPrefixes []string) (RunCapabilities, error) {
	scope = strings.TrimSpace(scope)
	if scope != "" && scope != protocol.CapabilityScopeIsolatedPlanV1 {
		return RunCapabilities{}, fmt.Errorf("unsupported capability scope %q", scope)
	}
	if scope == "" {
		if len(allowedTools) > 0 || len(allowedURLPrefixes) > 0 {
			return RunCapabilities{}, fmt.Errorf("a capability scope is required when per-run allowlists are configured")
		}
		return RunCapabilities{}, nil
	}
	if len(allowedTools) == 0 {
		return RunCapabilities{}, fmt.Errorf("capability scope %q requires a non-empty allowed_tools allowlist", scope)
	}
	if len(allowedURLPrefixes) == 0 {
		return RunCapabilities{}, fmt.Errorf("capability scope %q requires non-empty allowed_url_prefixes", scope)
	}
	toolsList, toolSet, err := r.normalizeAllowedTools(allowedTools)
	if err != nil {
		return RunCapabilities{}, err
	}
	prefixes, err := webtools.NormalizeAllowedHTTPSURLPrefixes(allowedURLPrefixes)
	if err != nil {
		return RunCapabilities{}, err
	}
	return RunCapabilities{
		scope:              scope,
		allowedTools:       toolsList,
		allowedToolSet:     toolSet,
		allowedURLPrefixes: prefixes,
	}, nil
}

// NewDurableJobRunCapabilities constructs the fail-closed tool envelope used
// by one durable job invocation. Only structured native tools whose effects
// can be enforced by this process are accepted. In particular, shell/execute,
// secret, MCP/external, network-write, memory, skill, cache, and helper tools
// are rejected even when they exist in the ambient registry.
//
// allowedURLPathPrefixes must contain explicit canonical HTTPS prefixes, or a
// single "*" for unrestricted public HTTPS. web_search is accepted only with
// that explicit wildcard because a search query cannot be constrained to a
// destination prefix.
func (r *Registry) NewDurableJobRunCapabilities(allowedTools, readRoots, writeRoots, allowedURLPathPrefixes []string) (RunCapabilities, error) {
	if len(allowedTools) == 0 {
		return RunCapabilities{}, fmt.Errorf("capability scope %q requires a non-empty allowed_tools allowlist", protocol.CapabilityScopeDurableJobV1)
	}
	toolsList, toolSet, err := r.normalizeAllowedTools(allowedTools)
	if err != nil {
		return RunCapabilities{}, err
	}
	reads, err := normalizeCapabilityRoots("read_roots", readRoots)
	if err != nil {
		return RunCapabilities{}, err
	}
	writes, err := normalizeCapabilityRoots("write_roots", writeRoots)
	if err != nil {
		return RunCapabilities{}, err
	}

	networkUnrestricted := len(allowedURLPathPrefixes) == 1 && allowedURLPathPrefixes[0] == "*"
	if len(allowedURLPathPrefixes) > 1 && containsCapabilityValue(allowedURLPathPrefixes, "*") {
		return RunCapabilities{}, fmt.Errorf("allowed_url_prefixes wildcard must be the only entry")
	}
	var prefixes []string
	if !networkUnrestricted {
		prefixes, err = webtools.NormalizeAllowedHTTPSURLPathPrefixes(allowedURLPathPrefixes)
		if err != nil {
			return RunCapabilities{}, err
		}
	}

	var needsReadRoots, needsWriteRoots, needsNetwork bool
	for _, name := range toolsList {
		kind, supported := durableJobToolKinds[name]
		if !supported {
			return RunCapabilities{}, fmt.Errorf("tool %q is not enforceable in durable job scope", name)
		}
		tool, _ := r.lookup(name)
		if err := validateDurableJobToolRisk(name, kind, tool.Spec.Risk); err != nil {
			return RunCapabilities{}, err
		}
		switch kind {
		case durableJobToolLocalRead:
			needsReadRoots = true
		case durableJobToolLocalWrite:
			needsWriteRoots = true
		case durableJobToolNetworkRead:
			needsNetwork = true
		}
		if name == "web_search" && !networkUnrestricted {
			return RunCapabilities{}, fmt.Errorf("tool %q requires allowed_url_prefixes=[\"*\"] because search destinations cannot be host-constrained", name)
		}
	}
	if needsReadRoots && len(reads) == 0 {
		return RunCapabilities{}, fmt.Errorf("durable job filesystem read tools require non-empty read_roots")
	}
	if needsWriteRoots && len(writes) == 0 {
		return RunCapabilities{}, fmt.Errorf("durable job filesystem write tools require non-empty write_roots")
	}
	if needsNetwork && len(prefixes) == 0 && !networkUnrestricted {
		return RunCapabilities{}, fmt.Errorf("durable job network tools require explicit allowed_url_prefixes or [\"*\"]")
	}
	if !needsReadRoots && len(reads) != 0 {
		return RunCapabilities{}, fmt.Errorf("read_roots were granted without an allowed filesystem read tool")
	}
	if !needsWriteRoots && len(writes) != 0 {
		return RunCapabilities{}, fmt.Errorf("write_roots were granted without an allowed filesystem write tool")
	}
	if !needsNetwork && (len(prefixes) != 0 || networkUnrestricted) {
		return RunCapabilities{}, fmt.Errorf("allowed_url_prefixes were granted without an allowed network tool")
	}

	return RunCapabilities{
		scope:                  protocol.CapabilityScopeDurableJobV1,
		allowedTools:           toolsList,
		allowedToolSet:         toolSet,
		allowedURLPathPrefixes: prefixes,
		readRoots:              reads,
		writeRoots:             writes,
		networkUnrestricted:    networkUnrestricted,
	}, nil
}

// IsDurableJobWriteTool reports whether a tool is one of the structured local
// mutations that durable jobs may expose to an explicitly designated writer.
func IsDurableJobWriteTool(name string) bool {
	return durableJobToolKinds[name] == durableJobToolLocalWrite
}

func validateDurableJobToolRisk(name string, kind durableJobToolKind, risk protocol.Risk) error {
	class := protocol.RiskClass(risk)
	want := protocol.Risk("")
	switch kind {
	case durableJobToolStateless, durableJobToolLocalRead:
		want = protocol.RiskLocalRead
	case durableJobToolLocalWrite:
		want = protocol.RiskLocalWrite
	case durableJobToolNetworkRead:
		want = protocol.RiskNetworkRead
	}
	if class != want {
		return fmt.Errorf("tool %q has risk %q (%q), want enforceable risk %q", name, risk, class, want)
	}
	return nil
}

func normalizeCapabilityRoots(label string, roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for index, root := range roots {
		if root == "" || strings.TrimSpace(root) != root || root == "*" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return nil, fmt.Errorf("%s[%d] must be a concrete absolute clean path", label, index)
		}
		resolved, err := resolvedPathForPolicy(root)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] resolve root: %w", label, index, err)
		}
		resolved = filepath.Clean(resolved)
		if _, exists := seen[resolved]; exists {
			return nil, fmt.Errorf("%s contains roots that resolve to duplicate %q", label, resolved)
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
	}
	sort.Strings(out)
	return out, nil
}

func containsCapabilityValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (r *Registry) normalizeAllowedTools(values []string) ([]string, map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, nil, nil
	}
	if r == nil {
		return nil, nil, fmt.Errorf("allowed_tools requires an available tool registry")
	}
	if len(values) > maxAllowedTools {
		return nil, nil, fmt.Errorf("allowed_tools has %d entries; maximum is %d", len(values), maxAllowedTools)
	}
	out := make([]string, 0, len(values))
	set := make(map[string]struct{}, len(values))
	for i, name := range values {
		if name == "" || strings.TrimSpace(name) != name {
			return nil, nil, fmt.Errorf("allowed_tools[%d] must be a non-empty canonical tool name without surrounding whitespace", i)
		}
		if _, ok := r.lookup(name); !ok {
			return nil, nil, fmt.Errorf("allowed_tools[%d] names unknown tool %q", i, name)
		}
		if _, ok := set[name]; ok {
			return nil, nil, fmt.Errorf("allowed_tools contains duplicate %q", name)
		}
		set[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, set, nil
}

func (c RunCapabilities) Clone() RunCapabilities {
	out := RunCapabilities{
		scope:                  c.scope,
		allowedTools:           append([]string(nil), c.allowedTools...),
		allowedURLPrefixes:     append([]string(nil), c.allowedURLPrefixes...),
		allowedURLPathPrefixes: append([]string(nil), c.allowedURLPathPrefixes...),
		readRoots:              append([]string(nil), c.readRoots...),
		writeRoots:             append([]string(nil), c.writeRoots...),
		networkUnrestricted:    c.networkUnrestricted,
	}
	if len(c.allowedToolSet) > 0 {
		out.allowedToolSet = make(map[string]struct{}, len(c.allowedToolSet))
		for name := range c.allowedToolSet {
			out.allowedToolSet[name] = struct{}{}
		}
	}
	return out
}

func (c RunCapabilities) Scope() string {
	return c.scope
}

func (c RunCapabilities) AllowsTool(name string) bool {
	if c.scope == "" && len(c.allowedToolSet) == 0 {
		return true
	}
	_, ok := c.allowedToolSet[name]
	return ok
}

func (c RunCapabilities) HasToolRestrictions() bool {
	return len(c.allowedToolSet) > 0
}

func (c RunCapabilities) HasURLRestrictions() bool {
	return len(c.allowedURLPrefixes) > 0 || len(c.allowedURLPathPrefixes) > 0
}

func (c RunCapabilities) AllowedURLPrefixes() []string {
	return append([]string(nil), c.allowedURLPrefixes...)
}

func (c RunCapabilities) AllowedURLPathPrefixes() []string {
	return append([]string(nil), c.allowedURLPathPrefixes...)
}

func (c RunCapabilities) ReadRoots() []string {
	return append([]string(nil), c.readRoots...)
}

func (c RunCapabilities) WriteRoots() []string {
	return append([]string(nil), c.writeRoots...)
}

func (c RunCapabilities) NetworkUnrestricted() bool {
	return c.networkUnrestricted
}

func (c RunCapabilities) RequiresHTTPS() bool {
	return c.scope == protocol.CapabilityScopeDurableJobV1 &&
		(c.networkUnrestricted || len(c.allowedURLPathPrefixes) > 0)
}

func (c RunCapabilities) workspaceRootsForRisk(risk protocol.Risk) ([]string, bool) {
	if c.scope != protocol.CapabilityScopeDurableJobV1 {
		return nil, false
	}
	switch protocol.RiskClass(risk) {
	case protocol.RiskLocalRead:
		return c.ReadRoots(), true
	case protocol.RiskLocalWrite:
		return c.WriteRoots(), true
	default:
		return nil, false
	}
}

func (c RunCapabilities) Attestation() CapabilityAttestation {
	if c.scope == "" {
		return CapabilityAttestation{}
	}
	networkPolicy := c.allowedURLPrefixes
	if c.scope == protocol.CapabilityScopeDurableJobV1 {
		networkPolicy = c.allowedURLPathPrefixes
		if c.networkUnrestricted {
			networkPolicy = []string{"*"}
		}
	}
	return CapabilityAttestation{
		Scope:                    c.scope,
		AllowedToolsCount:        len(c.allowedTools),
		AllowedToolsSHA256:       canonicalStringListSHA256(c.allowedTools),
		AllowedURLPrefixesCount:  len(networkPolicy),
		AllowedURLPrefixesSHA256: canonicalStringListSHA256(networkPolicy),
		ReadRootsCount:           len(c.readRoots),
		ReadRootsSHA256:          canonicalStringListSHA256(c.readRoots),
		WriteRootsCount:          len(c.writeRoots),
		WriteRootsSHA256:         canonicalStringListSHA256(c.writeRoots),
	}
}

func contextWithRunCapabilities(ctx context.Context, capabilities RunCapabilities) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, ok := ctx.Value(runCapabilitiesContextKey{}).(RunCapabilities); ok &&
		capabilities.Scope() == "" &&
		(existing.Scope() != "" || existing.HasToolRestrictions() || existing.HasURLRestrictions()) {
		return ctx
	}
	return context.WithValue(ctx, runCapabilitiesContextKey{}, capabilities.Clone())
}

func runCapabilitiesFromContext(ctx context.Context, fallback RunCapabilities) RunCapabilities {
	if fallback.Scope() != "" || fallback.HasToolRestrictions() || fallback.HasURLRestrictions() {
		return fallback.Clone()
	}
	if ctx != nil {
		if capabilities, ok := ctx.Value(runCapabilitiesContextKey{}).(RunCapabilities); ok {
			return capabilities.Clone()
		}
	}
	return fallback.Clone()
}

func (r *Registry) runCapabilitiesForContext(ctx context.Context) RunCapabilities {
	if r == nil {
		return runCapabilitiesFromContext(ctx, RunCapabilities{})
	}
	return runCapabilitiesFromContext(ctx, r.runCapabilities)
}

func canonicalStringListSHA256(values []string) string {
	canonical := append([]string{}, values...)
	sort.Strings(canonical)
	body, _ := json.Marshal(canonical)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (r *Registry) validateRunURLCapability(ctx context.Context, call protocol.ToolCall) error {
	capabilities := r.runCapabilitiesForContext(ctx)
	if !capabilities.HasURLRestrictions() && !capabilities.RequiresHTTPS() {
		return nil
	}
	switch call.Name {
	case "web_fetch", "web_extract", "web_crawl":
	default:
		return nil
	}
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return err
	}
	if capabilities.Scope() == protocol.CapabilityScopeDurableJobV1 {
		return webtools.ValidateURLAgainstAllowedHTTPSPathPrefixes(args.URL, capabilities.AllowedURLPathPrefixes())
	}
	return webtools.ValidateURLAgainstAllowedHTTPSPrefixes(args.URL, capabilities.AllowedURLPrefixes())
}
