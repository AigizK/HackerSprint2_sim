package generator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

var (
	ErrWorldNotFound      = errors.New("world not found")
	ErrWorldAlreadyExists = errors.New("world already exists")
)

type WorldKey struct {
	Seed             int64
	ProfileHash      string
	GeneratorVersion string
}

type WorldDefinition struct {
	WorldID        string               `json:"world_id"`
	Key            WorldKey             `json:"key"`
	ProfileVersion string               `json:"profile_version"`
	ScheduleHash   string               `json:"schedule_hash"`
	Source         string               `json:"source"`
	StartsAt       time.Time            `json:"starts_at"`
	EndsAt         time.Time            `json:"ends_at"`
	CreatedAt      time.Time            `json:"created_at"`
	Bootstrap      []events.Event       `json:"bootstrap"`
	Events         events.EventSchedule `json:"events"`
}

type WorldRepository interface {
	FindWorld(ctx context.Context, key WorldKey) (WorldDefinition, error)
	CreateWorld(ctx context.Context, world WorldDefinition) error
}

type MemoryWorldRepository struct {
	mu     sync.RWMutex
	worlds map[WorldKey]WorldDefinition
}

func NewMemoryWorldRepository() *MemoryWorldRepository {
	return &MemoryWorldRepository{worlds: make(map[WorldKey]WorldDefinition)}
}

func (r *MemoryWorldRepository) FindWorld(_ context.Context, key WorldKey) (WorldDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	world, exists := r.worlds[key]
	if !exists {
		return WorldDefinition{}, ErrWorldNotFound
	}
	// A repository is only a catalog. Generated events are always hydrated by
	// the deterministic generator and never trusted as persisted source data.
	world.Events = nil
	world.Bootstrap = nil
	return world, nil
}

func (r *MemoryWorldRepository) CreateWorld(_ context.Context, world WorldDefinition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.worlds[world.Key]; exists {
		return ErrWorldAlreadyExists
	}
	world.Events = append(events.EventSchedule(nil), world.Events...)
	world.Bootstrap = append([]events.Event(nil), world.Bootstrap...)
	r.worlds[world.Key] = world
	return nil
}
