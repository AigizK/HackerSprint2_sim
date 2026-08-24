package simulation

import (
	"errors"
	"fmt"
)

var (
	ErrWorldAlreadyCreated   = errors.New("world already created")
	ErrWorldNotCreated       = errors.New("world not created")
	ErrRunCompleted          = errors.New("run completed")
	ErrProductAlreadyExists  = errors.New("product already exists")
	ErrProductNotFound       = errors.New("product not found")
	ErrPurchaseAlreadyExists = errors.New("purchase already exists")
	ErrInvalidCommand        = errors.New("invalid command")
	ErrInvalidEvent          = errors.New("invalid event")
)

func (s *State) Apply(event Event) error {
	switch event := event.(type) {
	case WorldCreated:
		if s.Status != RunNotCreated {
			return ErrWorldAlreadyCreated
		}
		if event.RunID == "" || event.Seed == 0 || event.StartedAt.IsZero() || !event.EndsAt.After(event.StartedAt) {
			return fmt.Errorf("%w: invalid world creation event", ErrInvalidEvent)
		}
		s.RunID = event.RunID
		s.Seed = event.Seed
		s.Status = RunRunning
		s.Clock = ClockState{
			StartedAt:   event.StartedAt,
			CurrentTime: event.StartedAt,
			EndsAt:      event.EndsAt,
		}
		if s.Products == nil {
			s.Products = make(map[ProductID]ProductState)
		}
		if s.Purchases == nil {
			s.Purchases = make(map[PurchaseID]PurchaseState)
		}
		return nil

	case ProductAdded:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.ProductID == "" || event.Name == "" || event.PriceMinor <= 0 ||
			event.ViewProbabilityPPM > ProbabilityScale || event.PurchaseProbabilityPPM > ProbabilityScale ||
			!event.AddedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid product event", ErrInvalidEvent)
		}
		if _, exists := s.Products[event.ProductID]; exists {
			return ErrProductAlreadyExists
		}
		var totalViewProbability uint64
		for _, product := range s.Products {
			totalViewProbability += uint64(product.ViewProbabilityPPM)
		}
		totalViewProbability += uint64(event.ViewProbabilityPPM)
		if totalViewProbability > uint64(ProbabilityScale) {
			return fmt.Errorf("%w: total product view probability exceeds %d", ErrInvalidEvent, ProbabilityScale)
		}
		s.Products[event.ProductID] = ProductState{
			ID:                     event.ProductID,
			Name:                   event.Name,
			PriceMinor:             event.PriceMinor,
			ViewProbabilityPPM:     event.ViewProbabilityPPM,
			PurchaseProbabilityPPM: event.PurchaseProbabilityPPM,
			AddedAt:                event.AddedAt,
		}
		return nil

	case ProductPurchased:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		product, exists := s.Products[event.ProductID]
		if !exists {
			return ErrProductNotFound
		}
		if _, exists := s.Purchases[event.PurchaseID]; exists {
			return ErrPurchaseAlreadyExists
		}
		if event.PurchaseID == "" || event.PriceMinor != product.PriceMinor || !event.PurchasedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: purchase price does not match product price", ErrInvalidEvent)
		}
		s.Purchases[event.PurchaseID] = PurchaseState{
			ID:          event.PurchaseID,
			ProductID:   event.ProductID,
			PriceMinor:  event.PriceMinor,
			PurchasedAt: event.PurchasedAt,
		}
		s.Economy.RevenueMinor += event.PriceMinor
		s.Economy.SuccessfulPurchases++
		return nil

	case TimeAdvanced:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if !event.From.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: time advance starts at %s, current time is %s", ErrInvalidEvent, event.From, s.Clock.CurrentTime)
		}
		if event.To.Before(event.From) || event.To.After(s.Clock.EndsAt) {
			return fmt.Errorf("%w: target time %s is outside the run", ErrInvalidEvent, event.To)
		}
		if event.AppliedDuration != event.To.Sub(event.From) {
			return fmt.Errorf("%w: applied duration does not match event interval", ErrInvalidEvent)
		}
		if event.RealElapsed < 0 || event.RequestedDuration < 0 ||
			(event.RequestedDuration > 0 && event.RequestedDuration < MinExplicitAdvance) {
			return fmt.Errorf("%w: invalid time durations", ErrInvalidEvent)
		}
		expectedApplied := max(event.RealElapsed, event.RequestedDuration)
		if remaining := s.Clock.EndsAt.Sub(event.From); expectedApplied > remaining {
			expectedApplied = remaining
		}
		if event.AppliedDuration != expectedApplied {
			return fmt.Errorf("%w: applied duration does not follow max(real, requested)", ErrInvalidEvent)
		}
		s.Clock.CurrentTime = event.To
		if event.To.Equal(s.Clock.EndsAt) {
			s.Status = RunCompleted
		}
		return nil

	default:
		return fmt.Errorf("%w: unsupported event %T", ErrInvalidEvent, event)
	}
}
