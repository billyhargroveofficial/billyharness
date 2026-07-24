package jobruntime

import (
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

func TestCoordinatorKeyUsesStableStoreNamespaceNotAuthorityRoots(t *testing.T) {
	base := newRunnerTestStore(t, t.TempDir())
	first := &coordinationKeyTestStore{Store: base, namespace: "backend:first"}
	second := &coordinationKeyTestStore{Store: base, namespace: "backend:second"}
	alias := &coordinationKeyTestStore{Store: base, namespace: "backend:first"}

	firstKey, err := coordinatorKey(first, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := coordinatorKey(second, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	aliasKey, err := coordinatorKey(alias, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if firstKey == secondKey {
		t.Fatal("independent store namespaces sharing protected roots collided")
	}
	if firstKey != aliasKey {
		t.Fatal("decorators preserving one store namespace did not coordinate")
	}
}

type coordinationKeyTestStore struct {
	jobstore.Store
	namespace string
}

func (s *coordinationKeyTestStore) CoordinationKey() string { return s.namespace }
