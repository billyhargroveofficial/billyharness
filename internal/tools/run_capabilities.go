package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	scope              string
	allowedTools       []string
	allowedToolSet     map[string]struct{}
	allowedURLPrefixes []string
}

type CapabilityAttestation struct {
	Scope                    string
	AllowedToolsCount        int
	AllowedToolsSHA256       string
	AllowedURLPrefixesCount  int
	AllowedURLPrefixesSHA256 string
}

func (r *Registry) NewRunCapabilities(scope string, allowedTools, allowedURLPrefixes []string) (RunCapabilities, error) {
	scope = strings.TrimSpace(scope)
	switch scope {
	case "":
		if len(allowedTools) > 0 || len(allowedURLPrefixes) > 0 {
			return RunCapabilities{}, fmt.Errorf("a capability scope is required when per-run allowlists are configured")
		}
		return RunCapabilities{}, nil
	case protocol.CapabilityScopeIsolatedPlanV1, protocol.CapabilityScopeBoundedIsolatedPlanV1:
	default:
		return RunCapabilities{}, fmt.Errorf("unsupported capability scope %q", scope)
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
		scope:              c.scope,
		allowedTools:       append([]string(nil), c.allowedTools...),
		allowedURLPrefixes: append([]string(nil), c.allowedURLPrefixes...),
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
	if len(c.allowedToolSet) == 0 {
		return true
	}
	_, ok := c.allowedToolSet[name]
	return ok
}

func (c RunCapabilities) HasToolRestrictions() bool {
	return len(c.allowedToolSet) > 0
}

func (c RunCapabilities) HasURLRestrictions() bool {
	return len(c.allowedURLPrefixes) > 0
}

func (c RunCapabilities) AllowedURLPrefixes() []string {
	return append([]string(nil), c.allowedURLPrefixes...)
}

func (c RunCapabilities) Attestation() CapabilityAttestation {
	if c.scope == "" {
		return CapabilityAttestation{}
	}
	return CapabilityAttestation{
		Scope:                    c.scope,
		AllowedToolsCount:        len(c.allowedTools),
		AllowedToolsSHA256:       canonicalStringListSHA256(c.allowedTools),
		AllowedURLPrefixesCount:  len(c.allowedURLPrefixes),
		AllowedURLPrefixesSHA256: canonicalStringListSHA256(c.allowedURLPrefixes),
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
	if !capabilities.HasURLRestrictions() {
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
	return webtools.ValidateURLAgainstAllowedHTTPSPrefixes(args.URL, capabilities.AllowedURLPrefixes())
}
