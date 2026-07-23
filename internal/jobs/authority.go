package jobs

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type AuthorityMode string

const (
	AuthorityModeDenyAll   AuthorityMode = "deny_all"
	AuthorityModeAllowList AuthorityMode = "allow_list"
)

// Authority is an explicit, fail-closed capability envelope. The zero value is
// invalid and grants nothing. Empty dimensions in allow-list mode grant
// nothing; "*" is an explicit unconstrained dimension which is still narrowed
// by intersection with server and job envelopes.
type Authority struct {
	Mode         AuthorityMode `json:"mode"`
	Tools        []string      `json:"tools,omitempty"`
	ReadRoots    []string      `json:"read_roots,omitempty"`
	WriteRoots   []string      `json:"write_roots,omitempty"`
	NetworkHosts []string      `json:"network_hosts,omitempty"`
	Providers    []string      `json:"providers,omitempty"`
}

func DenyAllAuthority() Authority {
	return Authority{Mode: AuthorityModeDenyAll}
}

func (a Authority) Validate() error {
	switch a.Mode {
	case AuthorityModeDenyAll:
		if len(a.Tools)+len(a.ReadRoots)+len(a.WriteRoots)+len(a.NetworkHosts)+len(a.Providers) != 0 {
			return fmt.Errorf("deny_all authority cannot contain allow-list entries")
		}
		return nil
	case AuthorityModeAllowList:
	default:
		return fmt.Errorf("authority mode must be %q or %q", AuthorityModeDenyAll, AuthorityModeAllowList)
	}
	for _, dimension := range []struct {
		label  string
		values []string
	}{
		{label: "tools", values: a.Tools},
		{label: "network_hosts", values: a.NetworkHosts},
		{label: "providers", values: a.Providers},
	} {
		if err := validateStringSet(dimension.label, dimension.values); err != nil {
			return err
		}
	}
	if err := validateRoots("read_roots", a.ReadRoots); err != nil {
		return err
	}
	return validateRoots("write_roots", a.WriteRoots)
}

func (a Authority) IsSubsetOf(parent Authority) bool {
	if a.Validate() != nil || parent.Validate() != nil {
		return false
	}
	if a.Mode == AuthorityModeDenyAll {
		return true
	}
	if parent.Mode == AuthorityModeDenyAll {
		return authorityEmpty(a)
	}
	return stringSetSubset(a.Tools, parent.Tools) &&
		rootSetSubset(a.ReadRoots, parent.ReadRoots) &&
		rootSetSubset(a.WriteRoots, parent.WriteRoots) &&
		stringSetSubset(a.NetworkHosts, parent.NetworkHosts) &&
		stringSetSubset(a.Providers, parent.Providers)
}

func ValidateChildAuthority(parent, child Authority) error {
	if err := parent.Validate(); err != nil {
		return fmt.Errorf("parent authority: %w", err)
	}
	if err := child.Validate(); err != nil {
		return fmt.Errorf("child authority: %w", err)
	}
	if !child.IsSubsetOf(parent) {
		return fmt.Errorf("child authority broadens parent authority")
	}
	return nil
}

func EffectiveAuthority(server, job, role Authority) (Authority, error) {
	return IntersectAuthority(server, job, role)
}

func IntersectAuthority(authorities ...Authority) (Authority, error) {
	if len(authorities) == 0 {
		return DenyAllAuthority(), fmt.Errorf("at least one authority is required")
	}
	denyAll := false
	for i, authority := range authorities {
		if err := authority.Validate(); err != nil {
			return DenyAllAuthority(), fmt.Errorf("authority %d: %w", i, err)
		}
		if authority.Mode == AuthorityModeDenyAll {
			denyAll = true
		}
	}
	if denyAll {
		return DenyAllAuthority(), nil
	}
	out := authorities[0]
	for _, authority := range authorities[1:] {
		out.Tools = intersectStringSets(out.Tools, authority.Tools)
		out.ReadRoots = intersectRootSets(out.ReadRoots, authority.ReadRoots)
		out.WriteRoots = intersectRootSets(out.WriteRoots, authority.WriteRoots)
		out.NetworkHosts = intersectStringSets(out.NetworkHosts, authority.NetworkHosts)
		out.Providers = intersectStringSets(out.Providers, authority.Providers)
	}
	out.Mode = AuthorityModeAllowList
	return canonicalAuthority(out), nil
}

func validateStringSet(label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("%s entries must be non-empty and trimmed", label)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s contains duplicate %q", label, value)
		}
		seen[value] = struct{}{}
	}
	if len(values) > 1 {
		if _, universal := seen["*"]; universal {
			return fmt.Errorf("%s wildcard must be the only entry", label)
		}
	}
	return nil
}

func validateRoots(label string, roots []string) error {
	if err := validateStringSet(label, roots); err != nil {
		return err
	}
	for _, root := range roots {
		if root == "*" {
			if len(roots) != 1 {
				return fmt.Errorf("%s wildcard must be the only entry", label)
			}
			continue
		}
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("%s root %q must be absolute and clean", label, root)
		}
	}
	return nil
}

func stringSetSubset(child, parent []string) bool {
	if contains(parent, "*") {
		return true
	}
	if contains(child, "*") {
		return false
	}
	parentSet := make(map[string]struct{}, len(parent))
	for _, value := range parent {
		parentSet[value] = struct{}{}
	}
	for _, value := range child {
		if _, exists := parentSet[value]; !exists {
			return false
		}
	}
	return true
}

func rootSetSubset(child, parent []string) bool {
	if contains(parent, "*") {
		return true
	}
	if contains(child, "*") {
		return false
	}
	for _, childRoot := range child {
		covered := false
		for _, parentRoot := range parent {
			if rootWithin(childRoot, parentRoot) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func intersectStringSets(left, right []string) []string {
	if contains(left, "*") {
		return sortedClone(right)
	}
	if contains(right, "*") {
		return sortedClone(left)
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	var out []string
	for _, value := range left {
		if _, exists := rightSet[value]; exists {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func intersectRootSets(left, right []string) []string {
	if contains(left, "*") {
		return canonicalRoots(right)
	}
	if contains(right, "*") {
		return canonicalRoots(left)
	}
	var out []string
	for _, leftRoot := range left {
		for _, rightRoot := range right {
			switch {
			case rootWithin(leftRoot, rightRoot):
				out = append(out, leftRoot)
			case rootWithin(rightRoot, leftRoot):
				out = append(out, rightRoot)
			}
		}
	}
	return canonicalRoots(out)
}

func rootWithin(child, parent string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func canonicalAuthority(authority Authority) Authority {
	authority.Tools = sortedUnique(authority.Tools)
	authority.ReadRoots = canonicalRoots(authority.ReadRoots)
	authority.WriteRoots = canonicalRoots(authority.WriteRoots)
	authority.NetworkHosts = sortedUnique(authority.NetworkHosts)
	authority.Providers = sortedUnique(authority.Providers)
	if authorityEmpty(authority) {
		return DenyAllAuthority()
	}
	return authority
}

func authorityEmpty(authority Authority) bool {
	return len(authority.Tools)+len(authority.ReadRoots)+len(authority.WriteRoots)+
		len(authority.NetworkHosts)+len(authority.Providers) == 0
}

func canonicalRoots(roots []string) []string {
	roots = sortedUnique(roots)
	var out []string
	for _, root := range roots {
		redundant := false
		for _, parent := range out {
			if rootWithin(root, parent) {
				redundant = true
				break
			}
		}
		if !redundant {
			out = append(out, root)
		}
	}
	return out
}

func sortedClone(values []string) []string {
	return sortedUnique(values)
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
