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
	WorldID        string
	Key            WorldKey
	ProfileVersion string
	ScheduleHash   string
	Source         string
	StartsAt       time.Time
	EndsAt         time.Time
	CreatedAt      time.Time
	Bootstrap      []events.Event
	Events         events.EventSchedule
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
	world.Events = append(events.EventSchedule(nil), world.Events...)
	world.Bootstrap = append([]events.Event(nil), world.Bootstrap...)
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
