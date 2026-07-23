package jobs

import (
	"reflect"
	"strings"
	"testing"
)

func TestAuthorityFailsClosedAndRequiresExplicitMode(t *testing.T) {
	t.Parallel()

	if err := (Authority{}).Validate(); err == nil {
		t.Fatal("zero authority validated; want explicit fail-closed mode")
	}
	if (Authority{}).IsSubsetOf(DenyAllAuthority()) {
		t.Fatal("invalid zero authority reported as a valid child")
	}
	if err := DenyAllAuthority().Validate(); err != nil {
		t.Fatalf("DenyAllAuthority().Validate(): %v", err)
	}
	if err := (Authority{
		Mode:  AuthorityModeDenyAll,
		Tools: []string{"read"},
	}).Validate(); err == nil {
		t.Fatal("deny_all authority with entries validated")
	}
}

func TestEffectiveAuthorityIntersectsEveryDimension(t *testing.T) {
	t.Parallel()

	server := Authority{
		Mode:         AuthorityModeAllowList,
		Tools:        []string{"edit", "read"},
		ReadRoots:    []string{"/workspace"},
		WriteRoots:   []string{"/workspace"},
		NetworkHosts: []string{"docs.example", "search.example"},
		Providers:    []string{"qwen", "test"},
	}
	job := Authority{
		Mode:         AuthorityModeAllowList,
		Tools:        []string{"read"},
		ReadRoots:    []string{"/workspace/project"},
		WriteRoots:   []string{"/workspace/project"},
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"qwen"},
	}
	role := Authority{
		Mode:         AuthorityModeAllowList,
		Tools:        []string{"*"},
		ReadRoots:    []string{"/workspace/project/notes"},
		WriteRoots:   nil,
		NetworkHosts: []string{"*"},
		Providers:    []string{"*"},
	}

	got, err := EffectiveAuthority(server, job, role)
	if err != nil {
		t.Fatalf("EffectiveAuthority(): %v", err)
	}
	want := Authority{
		Mode:         AuthorityModeAllowList,
		Tools:        []string{"read"},
		ReadRoots:    []string{"/workspace/project/notes"},
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"qwen"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective authority:\n got: %#v\nwant: %#v", got, want)
	}
	for name, parent := range map[string]Authority{
		"server": server,
		"job":    job,
		"role":   role,
	} {
		if !got.IsSubsetOf(parent) {
			t.Fatalf("effective authority broadens %s authority", name)
		}
	}

	reordered, err := IntersectAuthority(role, server, job)
	if err != nil {
		t.Fatalf("reordered IntersectAuthority(): %v", err)
	}
	if !reflect.DeepEqual(reordered, got) {
		t.Fatalf("intersection depends on input order:\n got: %#v\nwant: %#v", reordered, got)
	}
}

func TestValidateChildAuthorityRejectsEveryExpansionClass(t *testing.T) {
	t.Parallel()

	parent := Authority{
		Mode:         AuthorityModeAllowList,
		Tools:        []string{"read"},
		ReadRoots:    []string{"/workspace/project"},
		WriteRoots:   []string{"/workspace/project/output"},
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"qwen"},
	}
	validChild := Authority{
		Mode:         AuthorityModeAllowList,
		Tools:        []string{"read"},
		ReadRoots:    []string{"/workspace/project/notes"},
		WriteRoots:   nil,
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"qwen"},
	}
	if err := ValidateChildAuthority(parent, validChild); err != nil {
		t.Fatalf("valid narrow child: %v", err)
	}

	tests := map[string]func(Authority) Authority{
		"tool": func(child Authority) Authority {
			child.Tools = []string{"read", "shell"}
			return child
		},
		"read root": func(child Authority) Authority {
			child.ReadRoots = []string{"/workspace"}
			return child
		},
		"write root": func(child Authority) Authority {
			child.WriteRoots = []string{"/workspace/project"}
			return child
		},
		"network": func(child Authority) Authority {
			child.NetworkHosts = []string{"docs.example", "other.example"}
			return child
		},
		"provider": func(child Authority) Authority {
			child.Providers = []string{"qwen", "other"}
			return child
		},
		"wildcard": func(child Authority) Authority {
			child.Tools = []string{"*"}
			return child
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := ValidateChildAuthority(parent, mutate(validChild))
			if err == nil || !strings.Contains(err.Error(), "broadens") {
				t.Fatalf("ValidateChildAuthority() error = %v, want broadening rejection", err)
			}
		})
	}
}

func TestAuthorityIntersectionWithDisjointEnvelopeDeniesAll(t *testing.T) {
	t.Parallel()

	left := Authority{Mode: AuthorityModeAllowList, Tools: []string{"read"}}
	right := Authority{Mode: AuthorityModeAllowList, Tools: []string{"edit"}}
	got, err := IntersectAuthority(left, right)
	if err != nil {
		t.Fatalf("IntersectAuthority(): %v", err)
	}
	if !reflect.DeepEqual(got, DenyAllAuthority()) {
		t.Fatalf("disjoint intersection = %#v, want explicit deny_all", got)
	}
}

func TestAuthorityIntersectionValidatesAllInputsBeforeDenying(t *testing.T) {
	t.Parallel()

	got, err := IntersectAuthority(DenyAllAuthority(), Authority{})
	if err == nil {
		t.Fatalf("IntersectAuthority() = %#v, nil; want invalid second authority", got)
	}
	if !reflect.DeepEqual(got, DenyAllAuthority()) {
		t.Fatalf("failed intersection fallback = %#v, want deny_all", got)
	}
}
