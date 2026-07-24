package jobruntime

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

func TestResolveEffectiveAuthorityIntersectsAndNarrowsRouteProvider(t *testing.T) {
	t.Parallel()
	store := &protectedRootTestStore{roots: []string{"/private/billyharness/jobs"}}
	spec, role, item, server := validAuthorityInputs(t)

	effective, err := ResolveEffectiveAuthority(store, server, spec, role, item)
	if err != nil {
		t.Fatalf("ResolveEffectiveAuthority(): %v", err)
	}
	want := jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"read"},
		ReadRoots:    []string{"/workspace/project/notes"},
		WriteRoots:   []string{"/workspace/project/output"},
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"qwen"},
	}
	if !reflect.DeepEqual(effective, want) {
		t.Fatalf("effective authority:\n got: %#v\nwant: %#v", effective, want)
	}
	if !effective.IsSubsetOf(server) || !effective.IsSubsetOf(spec.Authority) || !effective.IsSubsetOf(role.Authority) || !effective.IsSubsetOf(item.Authority) {
		t.Fatal("effective authority broadened an input envelope")
	}

	effective.Providers[0] = "mutated"
	if got := store.ProtectedRoots(); !reflect.DeepEqual(got, []string{"/private/billyharness/jobs"}) {
		t.Fatalf("authority result mutated store roots: %v", got)
	}
}

func TestResolveEffectiveAuthorityFailsClosedWithoutPersistedProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*jobs.JobSpec, *jobs.RoleSpec, *jobs.WorkItem, *jobs.Authority)
		want   string
	}{
		{name: "zero route", mutate: func(spec *jobs.JobSpec, _ *jobs.RoleSpec, _ *jobs.WorkItem, _ *jobs.Authority) {
			spec.Route = jobs.ExecutionRoute{}
		}, want: "route"},
		{name: "provider denied by server", mutate: func(_ *jobs.JobSpec, _ *jobs.RoleSpec, _ *jobs.WorkItem, server *jobs.Authority) {
			server.Providers = []string{"other"}
		}, want: "does not allow"},
		{name: "provider denied by role", mutate: func(spec *jobs.JobSpec, role *jobs.RoleSpec, _ *jobs.WorkItem, _ *jobs.Authority) {
			role.Authority.Providers = []string{"other"}
			replacePersistedRole(spec, *role)
		}, want: "broadens"},
		{name: "provider denied by item", mutate: func(_ *jobs.JobSpec, _ *jobs.RoleSpec, item *jobs.WorkItem, _ *jobs.Authority) {
			item.Authority.Providers = nil
		}, want: "does not allow"},
		{name: "zero server authority", mutate: func(_ *jobs.JobSpec, _ *jobs.RoleSpec, _ *jobs.WorkItem, server *jobs.Authority) {
			*server = jobs.Authority{}
		}, want: "intersect"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec, role, item, server := validAuthorityInputs(t)
			test.mutate(&spec, &role, &item, &server)
			got, err := ResolveEffectiveAuthority(
				&protectedRootTestStore{roots: []string{"/private/billyharness/jobs"}},
				server,
				spec,
				role,
				item,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if !reflect.DeepEqual(got, jobs.DenyAllAuthority()) {
				t.Fatalf("failed resolution = %#v, want deny_all", got)
			}
		})
	}
}

func TestResolveEffectiveAuthorityRejectsEveryProtectedRootOverlap(t *testing.T) {
	t.Parallel()
	protected := "/private/billyharness/jobs"
	tests := []struct {
		name      string
		dimension string
		root      string
	}{
		{name: "read same", dimension: "read", root: protected},
		{name: "read ancestor", dimension: "read", root: "/private/billyharness"},
		{name: "read descendant", dimension: "read", root: protected + "/job-1"},
		{name: "read wildcard", dimension: "read", root: "*"},
		{name: "write same", dimension: "write", root: protected},
		{name: "write ancestor", dimension: "write", root: "/private"},
		{name: "write descendant", dimension: "write", root: protected + "/artifacts"},
		{name: "write wildcard", dimension: "write", root: "*"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec, role, item, server := validAuthorityInputs(t)
			setAuthorityRoots := func(authority *jobs.Authority) {
				if test.dimension == "read" {
					authority.ReadRoots = []string{test.root}
				} else {
					authority.WriteRoots = []string{test.root}
				}
			}
			setAuthorityRoots(&server)
			setAuthorityRoots(&spec.Authority)
			setAuthorityRoots(&role.Authority)
			setAuthorityRoots(&item.Authority)
			replacePersistedRole(&spec, role)

			got, err := ResolveEffectiveAuthority(
				&protectedRootTestStore{roots: []string{protected}},
				server,
				spec,
				role,
				item,
			)
			if err == nil || !strings.Contains(err.Error(), test.dimension) || !strings.Contains(err.Error(), "protected") {
				t.Fatalf("error = %v, want protected %s overlap", err, test.dimension)
			}
			if !reflect.DeepEqual(got, jobs.DenyAllAuthority()) {
				t.Fatalf("failed resolution = %#v, want deny_all", got)
			}
		})
	}
}

func TestResolveEffectiveAuthorityValidatesStoreBoundaryAndRoleIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		store jobstore.Store
		role  string
		want  string
	}{
		{name: "nil store", store: nil, want: "store is required"},
		{name: "no protected roots", store: &protectedRootTestStore{}, want: "no protected roots"},
		{name: "relative protected root", store: &protectedRootTestStore{roots: []string{"relative/jobs"}}, want: "canonical and absolute"},
		{name: "unclean protected root", store: &protectedRootTestStore{roots: []string{"/private/../private/jobs"}}, want: "canonical and absolute"},
		{name: "wildcard protected root", store: &protectedRootTestStore{roots: []string{"*"}}, want: "canonical and absolute"},
		{name: "role mismatch", store: &protectedRootTestStore{roots: []string{"/private/jobs"}}, role: "different.role", want: "does not match"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec, role, item, server := validAuthorityInputs(t)
			if test.role != "" {
				item.RoleID = test.role
			}
			got, err := ResolveEffectiveAuthority(test.store, server, spec, role, item)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if !reflect.DeepEqual(got, jobs.DenyAllAuthority()) {
				t.Fatalf("failed resolution = %#v, want deny_all", got)
			}
		})
	}
}

func validAuthorityInputs(t *testing.T) (jobs.JobSpec, jobs.RoleSpec, jobs.WorkItem, jobs.Authority) {
	t.Helper()
	workflow, err := jobs.CompilePreset(jobs.PresetGeneral, 1)
	if err != nil {
		t.Fatal(err)
	}
	jobAuthority := jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"read", "search"},
		ReadRoots:    []string{"/workspace/project"},
		WriteRoots:   []string{"/workspace/project/output"},
		NetworkHosts: []string{"docs.example", "search.example"},
		Providers:    []string{"*"},
	}
	roleAuthority := jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"read"},
		ReadRoots:    []string{"/workspace/project/notes"},
		WriteRoots:   []string{"/workspace/project/output"},
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"*"},
	}
	for index := range workflow.Roles {
		workflow.Roles[index].Authority = roleAuthority
	}
	role := workflow.Roles[0]
	for _, candidate := range workflow.Roles {
		if candidate.ID == "general.primary" {
			role = candidate
			break
		}
	}
	spec := jobs.JobSpec{
		ID:       "job-1",
		Goal:     "Produce one bounded result.",
		Preset:   workflow.Name,
		Workers:  workflow.Workers,
		Deadline: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
		Budget:   jobs.Budget{MaxCycles: 8, MaxAttempts: 32, MaxModelCalls: 128, MaxTokens: 1_000_000},
		Route: jobs.ExecutionRoute{
			ProviderID: "qwen",
			ModelID:    "qwen3.8-max-preview",
		},
		Workflow:  jobs.WorkflowControlFromWorkflow(workflow),
		Authority: jobAuthority,
		Roles:     workflow.Roles,
		Stages:    workflow.Stages,
	}
	item := jobs.WorkItem{
		ID:        "work-1",
		RoleID:    role.ID,
		Objective: "Inspect one bounded question.",
		Authority: roleAuthority,
	}
	server := jobs.Authority{
		Mode:         jobs.AuthorityModeAllowList,
		Tools:        []string{"read", "shell"},
		ReadRoots:    []string{"/workspace"},
		WriteRoots:   []string{"/workspace"},
		NetworkHosts: []string{"docs.example"},
		Providers:    []string{"qwen", "test"},
	}
	return spec, role, item, server
}

func replacePersistedRole(spec *jobs.JobSpec, role jobs.RoleSpec) {
	for index := range spec.Roles {
		if spec.Roles[index].ID == role.ID {
			spec.Roles[index] = role
			return
		}
	}
}

type protectedRootTestStore struct {
	roots []string
}

func (s *protectedRootTestStore) CoordinationKey() string { return "test:protected-roots" }

func (s *protectedRootTestStore) ProtectedRoots() []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.roots)
}

func (*protectedRootTestStore) Create(context.Context, jobs.JobSpec) (jobs.JobState, error) {
	return jobs.JobState{}, errors.New("unused")
}

func (*protectedRootTestStore) Append(context.Context, string, uint64, jobs.Event) (jobs.JobState, error) {
	return jobs.JobState{}, errors.New("unused")
}

func (*protectedRootTestStore) Load(context.Context, string) (jobs.JobState, error) {
	return jobs.JobState{}, errors.New("unused")
}

func (*protectedRootTestStore) List(context.Context) ([]jobstore.JobSummary, error) {
	return nil, errors.New("unused")
}

func (*protectedRootTestStore) PutArtifact(context.Context, string, string, string, string, io.Reader) (jobs.ArtifactRef, error) {
	return jobs.ArtifactRef{}, errors.New("unused")
}

func (*protectedRootTestStore) OpenArtifact(context.Context, string, string) (io.ReadCloser, jobs.ArtifactRef, error) {
	return nil, jobs.ArtifactRef{}, errors.New("unused")
}

func (*protectedRootTestStore) Close() error { return nil }
