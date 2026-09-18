package chat

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
)

// MemoryControlStore exercises the same snapshot and generation rules as the
// persistent store without a database. It is for tests and explicit local demos.
type MemoryControlStore struct {
	mu     sync.RWMutex
	state  ControlState
	domain string
}

var memoryControlStoreSequence atomic.Uint64

// NewMemoryControlStore builds an empty store whose storage domain is unique, so
// two memory corpora can never share a projection domain by accident.
func NewMemoryControlStore() *MemoryControlStore {
	return &MemoryControlStore{
		state:  newControlState(),
		domain: fmt.Sprintf("chat-memory-%d", memoryControlStoreSequence.Add(1)),
	}
}

func (s *MemoryControlStore) StorageDomain() string { return s.domain }

func (s *MemoryControlStore) Load(_ context.Context) (ControlState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneControlState(s.state)
}

func (s *MemoryControlStore) Generation(_ context.Context) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Generation, nil
}

func (s *MemoryControlStore) Save(_ context.Context, expectedGeneration uint64, state ControlState) (uint64, error) {
	if state.Generation != expectedGeneration {
		return 0, fmt.Errorf("%w: snapshot=%d expected=%d", ErrControlStoreConflict, state.Generation, expectedGeneration)
	}
	if expectedGeneration == math.MaxUint64 {
		return 0, fmt.Errorf("%w: generation overflow", ErrControlStoreConflict)
	}

	clone, err := cloneControlState(state)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Generation != expectedGeneration {
		return 0, fmt.Errorf("%w: current=%d expected=%d", ErrControlStoreConflict, s.state.Generation, expectedGeneration)
	}
	clone.Generation = expectedGeneration + 1
	s.state = clone
	return clone.Generation, nil
}
