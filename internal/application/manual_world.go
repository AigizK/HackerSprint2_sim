package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

type ManualWorldInput struct {
	Seed       int64
	StartsAt   time.Time
	EndsAt     time.Time
	Bootstrap  []events.Event
	Events     events.EventSchedule
	Evaluation generator.WorldEvaluation
}

func RegisterManualWorld(ctx context.Context, catalog generator.WorldRepository, input ManualWorldInput) (generator.WorldDefinition, error) {
	if input.Seed >= 0 || input.StartsAt.IsZero() || !input.EndsAt.After(input.StartsAt) {
		return generator.WorldDefinition{}, ErrInvalidRequest
	}
	state := simulation.NewState()
	definitionEvents := make([]events.Event, 0, 2+len(input.Bootstrap))
	definitionEvents = append(definitionEvents, events.WorldCreated{RunID: "manual-validation", Seed: input.Seed, StartedAt: input.StartsAt.UTC(), EndsAt: input.EndsAt.UTC()})
	definitionEvents = append(definitionEvents, input.Bootstrap...)
	definitionEvents = append(definitionEvents, events.WorldScheduleCreated{Schedule: input.Events, CreatedAt: input.StartsAt.UTC()})
	for _, event := range definitionEvents {
		if err := state.Apply(event); err != nil {
			return generator.WorldDefinition{}, fmt.Errorf("validate manual world: %w", err)
		}
	}
	hash, err := generator.HashWorldEvents(input.Bootstrap, input.Events)
	if err != nil {
		return generator.WorldDefinition{}, err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("manual:%d:%s", input.Seed, hash)))
	world := generator.WorldDefinition{WorldID: "m" + hex.EncodeToString(digest[:16]),
		Key:            generator.WorldKey{Seed: input.Seed, ProfileHash: "manual", GeneratorVersion: "manual.v1"},
		ProfileVersion: "manual.v1", ScheduleHash: hash, Source: "manual", StartsAt: input.StartsAt.UTC(),
		EndsAt: input.EndsAt.UTC(), CreatedAt: time.Now().UTC(), Evaluation: input.Evaluation,
		Bootstrap: append([]events.Event(nil), input.Bootstrap...), Events: append(events.EventSchedule(nil), input.Events...)}
	if err := catalog.CreateWorld(ctx, world); err != nil {
		return generator.WorldDefinition{}, err
	}
	return world, nil
}
