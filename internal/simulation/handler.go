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
	events, err := DecideWithRunPolicies(runID, state, command)
	if err != nil {
		return nil, err
	}
	if _, err := h.store.Append(ctx, runID, state.Version, events); err != nil {
		return nil, err
	}
	return events, nil
}

// DecideWithRunPolicies executes the current aggregate rules.
func DecideWithRunPolicies(runID string, state State, command Command) ([]events.Event, error) {
	return DecideWithRunPoliciesContext(context.Background(), runID, state, command)
}

func DecideWithRunPoliciesContext(ctx context.Context, runID string, state State, command Command) ([]events.Event, error) {
	decided, err := decideContext(ctx, runID, state, command)
	if err != nil {
		return nil, err
	}
	return decided, nil
}

func (h *Handler) State(ctx context.Context, runID string) (State, error) {
	records, err := h.store.Load(ctx, runID)
	if err != nil {
		return State{}, err
	}
	return Rehydrate(records)
}
