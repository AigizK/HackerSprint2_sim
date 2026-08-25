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

// DecideWithRunPolicies applies aggregate decisions and run-wide policies,
// including immediate termination on a negative balance.
func DecideWithRunPolicies(runID string, state State, command Command) ([]events.Event, error) {
	decided, err := Decide(runID, state, command)
	if err != nil {
		return nil, err
	}
	return endRunOnNegativeBalance(state, decided)
}

func endRunOnNegativeBalance(state State, decided []events.Event) ([]events.Event, error) {
	working := cloneState(state)
	for _, event := range decided {
		if err := working.Apply(event); err != nil {
			return nil, err
		}
	}
	if !working.Economy.StopRunOnNegative {
		return decided, nil
	}
	balance := working.Economy.InitialBalanceMinor + working.Economy.RevenueMinor -
		working.Economy.ServerCostMinor - working.Economy.DeploymentCostMinor
	if balance >= 0 {
		return decided, nil
	}
	if working.Status == RunCompleted && len(decided) > 0 {
		if ended, ok := decided[len(decided)-1].(events.RunEnded); ok && ended.Reason == "world_completed" {
			decided[len(decided)-1] = events.RunEnded{CompletedAt: ended.CompletedAt, Reason: "negative_balance"}
		}
		return decided, nil
	}
	if working.Status != RunRunning {
		return decided, nil
	}
	return append(decided, events.RunEnded{CompletedAt: working.Clock.CurrentTime, Reason: "negative_balance"}), nil
}

func (h *Handler) State(ctx context.Context, runID string) (State, error) {
	records, err := h.store.Load(ctx, runID)
	if err != nil {
		return State{}, err
	}
	return Rehydrate(records)
}
