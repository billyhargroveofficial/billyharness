package jobruntime

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

// ResolveEffectiveAuthority intersects every persisted/runtime capability
// envelope, narrows provider access to the persisted execution route, and
// rejects access to storage-owned filesystem roots. It fails closed when the
// store boundary or route provider is missing.
func ResolveEffectiveAuthority(
	store jobstore.Store,
	server jobs.Authority,
	spec jobs.JobSpec,
	role jobs.RoleSpec,
	item jobs.WorkItem,
) (jobs.Authority, error) {
	if store == nil {
		return jobs.DenyAllAuthority(), errors.New("job store is required for authority resolution")
	}
	if err := spec.Validate(); err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("job spec: %w", err)
	}
	if err := role.Validate(); err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("role: %w", err)
	}
	var persistedRole *jobs.RoleSpec
	for index := range spec.Roles {
		if spec.Roles[index].ID == role.ID {
			persistedRole = &spec.Roles[index]
			break
		}
	}
	if persistedRole == nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("role %q is not declared by the job", role.ID)
	}
	if !reflect.DeepEqual(*persistedRole, role) {
		return jobs.DenyAllAuthority(), fmt.Errorf("role %q differs from the persisted job role", role.ID)
	}
	if err := item.Validate(); err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("work item: %w", err)
	}
	if item.RoleID != role.ID {
		return jobs.DenyAllAuthority(), fmt.Errorf("work item role %q does not match role %q", item.RoleID, role.ID)
	}
	if err := spec.Route.Validate(); err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("persisted execution route: %w", err)
	}
	providerID := strings.TrimSpace(spec.Route.ProviderID)
	if providerID == "" || providerID == "*" {
		return jobs.DenyAllAuthority(), errors.New("persisted execution route requires a concrete provider")
	}
	roleParent, err := jobs.IntersectAuthority(spec.Authority, role.Authority)
	if err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("intersect job and role authority: %w", err)
	}
	if err := jobs.ValidateChildAuthority(roleParent, item.Authority); err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("work item authority: %w", err)
	}

	effective, err := jobs.IntersectAuthority(server, spec.Authority, role.Authority, item.Authority)
	if err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("intersect invocation authority: %w", err)
	}
	if !authorityAllowsProvider(effective, providerID) {
		return jobs.DenyAllAuthority(), fmt.Errorf("effective authority does not allow persisted route provider %q", providerID)
	}
	// Preserve every already-intersected dimension while replacing a possible
	// provider wildcard with the one immutable route selected at job creation.
	effective.Providers = []string{providerID}
	if err := effective.Validate(); err != nil {
		return jobs.DenyAllAuthority(), fmt.Errorf("narrow route provider: %w", err)
	}

	protectedRoots := store.ProtectedRoots()
	if len(protectedRoots) == 0 {
		return jobs.DenyAllAuthority(), errors.New("job store returned no protected roots")
	}
	for index, protected := range protectedRoots {
		if protected == "*" || !filepath.IsAbs(protected) || filepath.Clean(protected) != protected {
			return jobs.DenyAllAuthority(), fmt.Errorf("job store protected root %d is not canonical and absolute", index)
		}
	}
	for _, dimension := range []struct {
		name  string
		roots []string
	}{
		{name: "read", roots: effective.ReadRoots},
		{name: "write", roots: effective.WriteRoots},
	} {
		for _, root := range dimension.roots {
			if root == "*" {
				return jobs.DenyAllAuthority(), fmt.Errorf("effective %s authority wildcard overlaps protected job store roots", dimension.name)
			}
			for _, protected := range protectedRoots {
				if filesystemRootsOverlap(root, protected) {
					return jobs.DenyAllAuthority(), fmt.Errorf(
						"effective %s root %q overlaps protected job store root %q",
						dimension.name,
						root,
						protected,
					)
				}
			}
		}
	}
	return effective, nil
}

func authorityAllowsProvider(authority jobs.Authority, providerID string) bool {
	if authority.Mode != jobs.AuthorityModeAllowList || providerID == "" || providerID == "*" {
		return false
	}
	for _, allowed := range authority.Providers {
		if allowed == "*" || allowed == providerID {
			return true
		}
	}
	return false
}

func filesystemRootsOverlap(left, right string) bool {
	return rootContains(left, right) || rootContains(right, left)
}

func rootContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
