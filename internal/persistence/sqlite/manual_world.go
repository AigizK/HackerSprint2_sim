package sqlite

import (
	"encoding/json"
	"fmt"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

type persistedEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type persistedManualWorld struct {
	WorldID        string                    `json:"world_id"`
	Key            generator.WorldKey        `json:"key"`
	ProfileVersion string                    `json:"profile_version"`
	ScheduleHash   string                    `json:"schedule_hash"`
	Source         string                    `json:"source"`
	StartsAt       string                    `json:"starts_at"`
	EndsAt         string                    `json:"ends_at"`
	CreatedAt      string                    `json:"created_at"`
	Bootstrap      []persistedEvent          `json:"bootstrap"`
	Events         []persistedScheduledEvent `json:"events"`
}

type persistedScheduledEvent struct {
	Sequence uint64         `json:"sequence"`
	OccursAt string         `json:"occurs_at"`
	Event    persistedEvent `json:"event"`
}

func encodePersistedEvent(event events.Event) (persistedEvent, error) {
	typeName, payload, err := simulation.EncodeEvent(event)
	return persistedEvent{Type: typeName, Payload: payload}, err
}

func encodeManualWorld(world generator.WorldDefinition) ([]byte, error) {
	persisted := persistedManualWorld{WorldID: world.WorldID, Key: world.Key, ProfileVersion: world.ProfileVersion,
		ScheduleHash: world.ScheduleHash, Source: world.Source, StartsAt: formatTime(world.StartsAt), EndsAt: formatTime(world.EndsAt),
		CreatedAt: formatTime(world.CreatedAt)}
	for _, event := range world.Bootstrap {
		encoded, err := encodePersistedEvent(event)
		if err != nil {
			return nil, err
		}
		persisted.Bootstrap = append(persisted.Bootstrap, encoded)
	}
	for _, scheduled := range world.Events {
		encoded, err := encodePersistedEvent(scheduled.Event)
		if err != nil {
			return nil, err
		}
		persisted.Events = append(persisted.Events, persistedScheduledEvent{Sequence: scheduled.Sequence, OccursAt: formatTime(scheduled.OccursAt), Event: encoded})
	}
	return json.Marshal(persisted)
}

func decodeManualWorld(payload []byte) (generator.WorldDefinition, error) {
	var persisted persistedManualWorld
	if len(payload) == 0 {
		return generator.WorldDefinition{}, fmt.Errorf("decode manual world definition: empty payload")
	}
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return generator.WorldDefinition{}, fmt.Errorf("decode manual world definition: %w", err)
	}
	world := generator.WorldDefinition{WorldID: persisted.WorldID, Key: persisted.Key, ProfileVersion: persisted.ProfileVersion,
		ScheduleHash: persisted.ScheduleHash, Source: persisted.Source}
	var err error
	if world.StartsAt, err = parseTime(persisted.StartsAt); err != nil {
		return generator.WorldDefinition{}, err
	}
	if world.EndsAt, err = parseTime(persisted.EndsAt); err != nil {
		return generator.WorldDefinition{}, err
	}
	if world.CreatedAt, err = parseTime(persisted.CreatedAt); err != nil {
		return generator.WorldDefinition{}, err
	}
	for _, encoded := range persisted.Bootstrap {
		event, err := simulation.DecodeEvent(encoded.Type, encoded.Payload)
		if err != nil {
			return generator.WorldDefinition{}, err
		}
		world.Bootstrap = append(world.Bootstrap, event)
	}
	for _, scheduled := range persisted.Events {
		at, err := parseTime(scheduled.OccursAt)
		if err != nil {
			return generator.WorldDefinition{}, err
		}
		event, err := simulation.DecodeEvent(scheduled.Event.Type, scheduled.Event.Payload)
		if err != nil {
			return generator.WorldDefinition{}, err
		}
		world.Events = append(world.Events, events.ScheduledWorldEvent{Sequence: scheduled.Sequence, OccursAt: at, Event: event})
	}
	return world, nil
}
