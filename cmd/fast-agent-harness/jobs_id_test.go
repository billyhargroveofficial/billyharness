package main

import (
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

func TestNewClientJobIDIsPortableAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	for range 256 {
		jobID := newClientJobID()
		if err := jobstore.ValidatePortableID(jobID); err != nil {
			t.Fatalf("generated job id %q is not portable: %v", jobID, err)
		}
		if _, duplicate := seen[jobID]; duplicate {
			t.Fatalf("duplicate generated job id %q", jobID)
		}
		seen[jobID] = struct{}{}
	}
}
