package simulation

import (
	"context"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

type Handler struct {
	store EventStore
}

func NewHandler(store EventStore) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Execute(ctx context.Context, runID string, command Command) ([]events.Event, error) {
	records, err := h.store.Load(ctx, runID)
	if err != nil {
		return nil, err
	}
	state, err := Rehydrate(records)
	if err != nil {
		return nil, err
	}
	events, err := Decide(runID, state, command)
	if err != nil {
		return nil, err
	}
	if _, err := h.store.Append(ctx, runID, state.Version, events); err != nil {
		return nil, err
	}
	return events, nil
}

func (h *Handler) State(ctx context.Context, runID string) (State, error) {
	records, err := h.store.Load(ctx, runID)
	if err != nil {
		return State{}, err
	}
	return Rehydrate(records)
}
