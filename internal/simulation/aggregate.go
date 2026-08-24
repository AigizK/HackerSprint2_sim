package simulation

import (
	"fmt"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

func Rehydrate(records []StoredEvent) (State, error) {
	state := NewState()
	for _, record := range records {
		if record.Version != state.Version+1 {
			return State{}, fmt.Errorf("%w: expected version %d, got %d", ErrInvalidEvent, state.Version+1, record.Version)
		}
		if err := state.Apply(record.Event); err != nil {
			return State{}, err
		}
		state.Version = record.Version
	}
	return state, nil
}

func Decide(runID string, state State, command Command) ([]events.Event, error) {
	switch command := command.(type) {
	case CreateWorld:
		if state.Status != RunNotCreated {
			return nil, ErrWorldAlreadyCreated
		}
		if runID == "" || command.Seed == 0 || command.StartedAt.IsZero() || !command.EndsAt.After(command.StartedAt) {
			return nil, fmt.Errorf("%w: invalid world parameters", ErrInvalidCommand)
		}
		return []events.Event{events.WorldCreated{
			RunID:     runID,
			Seed:      command.Seed,
			StartedAt: command.StartedAt,
			EndsAt:    command.EndsAt,
		}}, nil

	case AddProduct:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.ProductID == "" || strings.TrimSpace(command.Name) == "" || command.PriceMinor <= 0 {
			return nil, fmt.Errorf("%w: invalid product", ErrInvalidCommand)
		}
		if command.ViewProbabilityPPM > ProbabilityScale || command.PurchaseProbabilityPPM > ProbabilityScale {
			return nil, fmt.Errorf("%w: probability is outside [0, %d]", ErrInvalidCommand, ProbabilityScale)
		}
		var totalViewProbability uint64
		for _, product := range state.Products {
			totalViewProbability += uint64(product.ViewProbabilityPPM)
		}
		totalViewProbability += uint64(command.ViewProbabilityPPM)
		if totalViewProbability > uint64(ProbabilityScale) {
			return nil, fmt.Errorf("%w: total product view probability exceeds %d", ErrInvalidCommand, ProbabilityScale)
		}
		if _, exists := state.Products[command.ProductID]; exists {
			return nil, ErrProductAlreadyExists
		}
		return []events.Event{events.ProductAdded{
			ProductID:              command.ProductID,
			Name:                   command.Name,
			PriceMinor:             command.PriceMinor,
			ViewProbabilityPPM:     command.ViewProbabilityPPM,
			PurchaseProbabilityPPM: command.PurchaseProbabilityPPM,
			AddedAt:                state.Clock.CurrentTime,
		}}, nil

	case PurchaseProduct:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.PurchaseID == "" {
			return nil, fmt.Errorf("%w: purchase id is required", ErrInvalidCommand)
		}
		if _, exists := state.Purchases[command.PurchaseID]; exists {
			return nil, ErrPurchaseAlreadyExists
		}
		product, exists := state.Products[command.ProductID]
		if !exists {
			return nil, ErrProductNotFound
		}
		return []events.Event{events.ProductPurchased{
			PurchaseID:  command.PurchaseID,
			ProductID:   product.ID,
			PriceMinor:  product.PriceMinor,
			PurchasedAt: state.Clock.CurrentTime,
		}}, nil

	case AdvanceTime:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.RealElapsed < 0 || command.RequestedDuration < 0 ||
			(command.RequestedDuration > 0 && command.RequestedDuration < MinExplicitAdvance) {
			return nil, fmt.Errorf("%w: explicit duration must be zero or at least %s; durations cannot be negative", ErrInvalidCommand, MinExplicitAdvance)
		}
		applied := max(command.RequestedDuration, command.RealElapsed)
		if applied == 0 {
			return nil, nil
		}
		remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
		if applied > remaining {
			applied = remaining
		}
		return []events.Event{events.TimeAdvanced{
			From:              state.Clock.CurrentTime,
			To:                state.Clock.CurrentTime.Add(applied),
			RealElapsed:       command.RealElapsed,
			RequestedDuration: command.RequestedDuration,
			AppliedDuration:   applied,
		}}, nil

	default:
		return nil, fmt.Errorf("%w: unsupported command %T", ErrInvalidCommand, command)
	}
}

func ensureRunning(state State) error {
	switch state.Status {
	case RunNotCreated:
		return ErrWorldNotCreated
	case RunCompleted:
		return ErrRunCompleted
	case RunRunning:
		return nil
	default:
		return fmt.Errorf("%w: unknown run status %q", ErrInvalidCommand, state.Status)
	}
}

func max(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
