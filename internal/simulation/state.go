package simulation

import (
	"errors"
	"fmt"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
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

func (s *State) Apply(event events.Event) error {
	switch event := event.(type) {
	case events.WorldCreated:
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

	case events.ProductAdded:
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

	case events.ProductPurchased:
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

	case events.TimeAdvanced:
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

	case events.PageBugActivated:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.BugID == "" || event.FailureProbabilityPPM > ProbabilityScale ||
			event.FixMessage == "" || event.FixMessageHash == "" || !event.ActivatedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid page bug event", ErrInvalidEvent)
		}
		if s.Bugs == nil {
			s.Bugs = make(map[model.BugID]BugState)
		}
		if _, exists := s.Bugs[event.BugID]; exists {
			return fmt.Errorf("%w: bug %q already exists", ErrInvalidEvent, event.BugID)
		}
		s.Bugs[event.BugID] = BugState{
			ID:                    event.BugID,
			Page:                  event.Page,
			ProductID:             event.ProductID,
			FailureProbabilityPPM: event.FailureProbabilityPPM,
			FixMessage:            event.FixMessage,
			FixMessageHash:        event.FixMessageHash,
			ActivatedAt:           event.ActivatedAt,
		}
		return nil

	case events.PageRequestStarted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.RequestID == "" || event.VisitorID == "" || !event.StartedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid page request event", ErrInvalidEvent)
		}
		if s.Requests == nil {
			s.Requests = make(map[model.RequestID]PageRequestState)
		}
		if _, exists := s.Requests[event.RequestID]; exists {
			return fmt.Errorf("%w: request %q already exists", ErrInvalidEvent, event.RequestID)
		}
		s.Requests[event.RequestID] = PageRequestState{
			ID:        event.RequestID,
			Source:    event.Source,
			VisitorID: event.VisitorID,
			Page:      event.Page,
			ProductID: event.ProductID,
			LoadUnits: event.LoadUnits,
			Status:    model.PageRequestInProgress,
			StartedAt: event.StartedAt,
		}
		return nil

	case events.PageBugTriggered:
		if _, exists := s.Bugs[event.BugID]; !exists {
			return fmt.Errorf("%w: bug %q does not exist", ErrInvalidEvent, event.BugID)
		}
		if _, exists := s.Requests[event.RequestID]; !exists {
			return fmt.Errorf("%w: request %q does not exist", ErrInvalidEvent, event.RequestID)
		}
		return nil

	case events.PageRequestCompleted:
		request, exists := s.Requests[event.RequestID]
		if !exists {
			return fmt.Errorf("%w: request %q does not exist", ErrInvalidEvent, event.RequestID)
		}
		if request.Status != model.PageRequestInProgress || !event.CompletedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: request %q cannot be completed", ErrInvalidEvent, event.RequestID)
		}
		request.StatusCode = event.StatusCode
		request.ErrorCode = event.ErrorCode
		request.Message = event.Message
		request.CompletedAt = event.CompletedAt
		if event.StatusCode >= 200 && event.StatusCode < 300 {
			request.Status = model.PageRequestSucceeded
		} else {
			request.Status = model.PageRequestFailed
		}
		s.Requests[event.RequestID] = request
		return nil

	default:
		return fmt.Errorf("%w: unsupported event %T", ErrInvalidEvent, event)
	}
}
