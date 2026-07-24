package jobruntime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

// jobCoordinator is process-wide for one durable store namespace and job. A
// Runner instance is only a facade; ownership and cancellation must also work
// when Run and RequestCancel arrive through different Runner instances.
type jobCoordinator struct {
	run  sync.Mutex
	gate sync.Mutex

	generation  uint64
	active      *activeDispatch
	quarantined bool
}

type activeDispatch struct {
	generation uint64
	cancel     context.CancelFunc
}

type coordinatorEntry struct {
	coordinator *jobCoordinator
	references  uint64
}

var processCoordinators = struct {
	sync.Mutex
	entries map[string]*coordinatorEntry
}{entries: make(map[string]*coordinatorEntry)}

func acquireJobCoordinator(store jobstore.Store, jobID string) (*jobCoordinator, func(), error) {
	if err := jobstore.ValidatePortableID(jobID); err != nil {
		return nil, nil, fmt.Errorf("job id: %w", err)
	}
	key, err := coordinatorKey(store, jobID)
	if err != nil {
		return nil, nil, err
	}
	processCoordinators.Lock()
	entry := processCoordinators.entries[key]
	if entry == nil {
		entry = &coordinatorEntry{coordinator: &jobCoordinator{}}
		processCoordinators.entries[key] = entry
	}
	entry.references++
	processCoordinators.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			processCoordinators.Lock()
			defer processCoordinators.Unlock()
			current := processCoordinators.entries[key]
			if current != entry || current.references == 0 {
				return
			}
			current.references--
			if current.references == 0 {
				delete(processCoordinators.entries, key)
			}
		})
	}
	return entry.coordinator, release, nil
}

func coordinatorKey(store jobstore.Store, jobID string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("job store is required")
	}
	namespace := store.CoordinationKey()
	if strings.TrimSpace(namespace) == "" {
		return "", fmt.Errorf("job store must expose a coordination key")
	}
	var key strings.Builder
	key.WriteString("billyharness/jobruntime/coordinator/v2|")
	key.WriteString(strconv.Itoa(len(namespace)))
	key.WriteByte(':')
	key.WriteString(namespace)
	key.WriteByte('|')
	key.WriteString(strconv.Itoa(len(jobID)))
	key.WriteByte(':')
	key.WriteString(jobID)
	return key.String(), nil
}

// installDispatch must be called with gate held before the first durable
// AttemptStarted append. It lets cancellation through another Runner cancel
// preparation as well as already-dispatched calls.
func (c *jobCoordinator) installDispatch(cancel context.CancelFunc) uint64 {
	c.generation++
	c.active = &activeDispatch{generation: c.generation, cancel: cancel}
	return c.generation
}

func (c *jobCoordinator) clearDispatch(generation uint64) {
	c.gate.Lock()
	defer c.gate.Unlock()
	if c.active != nil && c.active.generation == generation {
		c.active = nil
	}
}

func (c *jobCoordinator) cancelActiveLocked() {
	if c.active != nil && c.active.cancel != nil {
		c.active.cancel()
	}
}

func (c *jobCoordinator) isQuarantined() bool {
	c.gate.Lock()
	defer c.gate.Unlock()
	return c.quarantined
}

func (c *jobCoordinator) setQuarantined(value bool) {
	c.gate.Lock()
	c.quarantined = value
	c.gate.Unlock()
}
