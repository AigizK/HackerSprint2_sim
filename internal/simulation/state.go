package simulation

import (
	"errors"
	"fmt"
	"time"

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

	case events.PageConfigured:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if !validPage(event.Page) || event.LoadUnits <= 0 || event.HoldDuration <= 0 ||
			!event.ConfiguredAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid page configuration", ErrInvalidEvent)
		}
		if s.Pages == nil {
			s.Pages = make(map[model.PageType]PageConfigState)
		}
		if _, exists := s.Pages[event.Page]; exists {
			return fmt.Errorf("%w: page %q is already configured", ErrInvalidEvent, event.Page)
		}
		s.Pages[event.Page] = PageConfigState{
			Page:         event.Page,
			LoadUnits:    event.LoadUnits,
			HoldDuration: event.HoldDuration,
			ConfiguredAt: event.ConfiguredAt,
		}
		return nil

	case events.InfrastructureConfigured:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.ServerProvisioningDuration <= 0 || !event.ConfiguredAt.Equal(s.Clock.CurrentTime) ||
			s.Infrastructure.ServerProvisioningDuration > 0 {
			return fmt.Errorf("%w: invalid infrastructure configuration", ErrInvalidEvent)
		}
		s.Infrastructure = InfrastructureConfigState{
			ServerProvisioningDuration: event.ServerProvisioningDuration,
			ConfiguredAt:               event.ConfiguredAt,
		}
		return nil

	case events.BackendScaleRequested:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.CommandID == "" || event.OperationID == "" || event.DesiredInstances < 0 ||
			!event.RequestedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid backend scale request", ErrInvalidEvent)
		}
		if s.Commands == nil {
			s.Commands = make(map[model.CommandID]model.OperationID)
		}
		if _, exists := s.Commands[event.CommandID]; exists {
			return fmt.Errorf("%w: command %q already exists", ErrInvalidEvent, event.CommandID)
		}
		s.Commands[event.CommandID] = event.OperationID
		return nil

	case events.OperationQueued:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.OperationID == "" || event.Kind == "" || !event.QueuedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid queued operation", ErrInvalidEvent)
		}
		if s.Operations == nil {
			s.Operations = make(map[model.OperationID]OperationState)
		}
		if _, exists := s.Operations[event.OperationID]; exists {
			return fmt.Errorf("%w: operation %q already exists", ErrInvalidEvent, event.OperationID)
		}
		s.Operations[event.OperationID] = OperationState{
			ID:       event.OperationID,
			Kind:     event.Kind,
			Status:   model.OperationStatusQueued,
			QueuedAt: event.QueuedAt,
		}
		return nil

	case events.OperationStarted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		operation, exists := s.Operations[event.OperationID]
		if !exists || operation.Status != model.OperationStatusQueued || !event.StartedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: operation %q cannot be started", ErrInvalidEvent, event.OperationID)
		}
		operation.Status = model.OperationStatusRunning
		operation.StartedAt = event.StartedAt
		s.Operations[event.OperationID] = operation
		return nil

	case events.OperationSucceeded:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		operation, exists := s.Operations[event.OperationID]
		if !exists || operation.Status != model.OperationStatusRunning || !event.CompletedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: operation %q cannot succeed", ErrInvalidEvent, event.OperationID)
		}
		operation.Status = model.OperationStatusSucceeded
		operation.CompletedAt = event.CompletedAt
		s.Operations[event.OperationID] = operation
		return nil

	case events.ServerProvisioningStarted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.OperationID == "" || event.ServerID == "" || event.CapacityUnits <= 0 ||
			event.CostPerHourMinor < 0 || !event.StartedAt.Equal(s.Clock.CurrentTime) || event.ReadyAt.Before(event.StartedAt) {
			return fmt.Errorf("%w: invalid server provisioning event", ErrInvalidEvent)
		}
		if s.Servers == nil {
			s.Servers = make(map[model.ServerID]ServerState)
		}
		if _, exists := s.Servers[event.ServerID]; exists {
			return fmt.Errorf("%w: server %q already exists", ErrInvalidEvent, event.ServerID)
		}
		s.Servers[event.ServerID] = ServerState{
			ID:               event.ServerID,
			OperationID:      event.OperationID,
			Status:           model.ServerProvisioning,
			CapacityUnits:    event.CapacityUnits,
			CostPerHourMinor: event.CostPerHourMinor,
			StartedAt:        event.StartedAt,
			ReadyAt:          event.ReadyAt,
		}
		return nil

	case events.ServerActivated:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		server, exists := s.Servers[event.ServerID]
		if !exists || server.Status != model.ServerProvisioning || server.OperationID != event.OperationID ||
			!event.ActivatedAt.Equal(s.Clock.CurrentTime) || event.ActivatedAt.Before(server.ReadyAt) {
			return fmt.Errorf("%w: server %q cannot be activated", ErrInvalidEvent, event.ServerID)
		}
		server.Status = model.ServerActive
		server.ActivatedAt = event.ActivatedAt
		s.Servers[event.ServerID] = server
		return nil

	case events.ServerDrainingStarted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		server, exists := s.Servers[event.ServerID]
		operation, operationExists := s.Operations[event.OperationID]
		if !exists || server.Status != model.ServerActive || !operationExists ||
			operation.Status != model.OperationStatusRunning || !event.StartedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: server %q cannot start draining", ErrInvalidEvent, event.ServerID)
		}
		server.Status = model.ServerDraining
		server.OperationID = event.OperationID
		s.Servers[event.ServerID] = server
		return nil

	case events.ServerRemoved:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		server, exists := s.Servers[event.ServerID]
		if !exists || server.Status != model.ServerDraining || server.OperationID != event.OperationID ||
			!event.RemovedAt.Equal(s.Clock.CurrentTime) || serverHasLoad(*s, event.ServerID, event.RemovedAt) {
			return fmt.Errorf("%w: server %q cannot be removed", ErrInvalidEvent, event.ServerID)
		}
		delete(s.Servers, event.ServerID)
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
			Status:                model.BugActive,
			ActivatedAt:           event.ActivatedAt,
		}
		return nil

	case events.PageRequestStarted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.RequestID == "" || event.VisitorID == "" || event.LoadUnits < 0 || !event.StartedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid page request event", ErrInvalidEvent)
		}
		if page, configured := s.Pages[event.Page]; configured && event.LoadUnits != page.LoadUnits {
			return fmt.Errorf("%w: request load does not match page configuration", ErrInvalidEvent)
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

	case events.PageRequestAccepted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		request, exists := s.Requests[event.RequestID]
		if !exists || request.Status != model.PageRequestInProgress || request.LoadUnits <= 0 {
			return fmt.Errorf("%w: request %q cannot be accepted", ErrInvalidEvent, event.RequestID)
		}
		server, exists := s.Servers[event.ServerID]
		if !exists || server.Status != model.ServerActive || !event.AcceptedAt.Equal(s.Clock.CurrentTime) ||
			!event.ReleasesAt.After(event.AcceptedAt) {
			return fmt.Errorf("%w: server %q cannot accept request", ErrInvalidEvent, event.ServerID)
		}
		page := s.Pages[request.Page]
		if event.ReleasesAt != event.AcceptedAt.Add(page.HoldDuration) ||
			serverAvailableCapacity(*s, server.ID, event.AcceptedAt) < request.LoadUnits {
			return fmt.Errorf("%w: invalid capacity allocation for request %q", ErrInvalidEvent, event.RequestID)
		}
		if s.Capacity == nil {
			s.Capacity = make(map[model.RequestID]CapacityAllocationState)
		}
		s.Capacity[event.RequestID] = CapacityAllocationState{
			RequestID:  event.RequestID,
			ServerID:   event.ServerID,
			LoadUnits:  request.LoadUnits,
			AcceptedAt: event.AcceptedAt,
			ReleasesAt: event.ReleasesAt,
		}
		request.ServerID = event.ServerID
		request.ReleasesAt = event.ReleasesAt
		s.Requests[event.RequestID] = request
		return nil

	case events.PageBugTriggered:
		bug, exists := s.Bugs[event.BugID]
		if !exists {
			return fmt.Errorf("%w: bug %q does not exist", ErrInvalidEvent, event.BugID)
		}
		if bug.Status != model.BugActive {
			return fmt.Errorf("%w: bug %q is not active", ErrInvalidEvent, event.BugID)
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
		if request.Status != model.PageRequestInProgress || !event.CompletedAt.Equal(s.Clock.CurrentTime) ||
			(request.LoadUnits > 0 && event.ServerID != request.ServerID) {
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

	case events.PageRequestRejected:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		request, exists := s.Requests[event.RequestID]
		if !exists || request.Status != model.PageRequestInProgress || event.StatusCode < 400 ||
			event.ErrorCode == "" || !event.RejectedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: request %q cannot be rejected", ErrInvalidEvent, event.RequestID)
		}
		request.Status = model.PageRequestFailed
		request.StatusCode = event.StatusCode
		request.ErrorCode = event.ErrorCode
		request.Message = event.Message
		request.CompletedAt = event.RejectedAt
		s.Requests[event.RequestID] = request
		return nil

	case events.BugFixSubmitted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.CommandID == "" || event.Message == "" || !event.SubmittedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid fix submission", ErrInvalidEvent)
		}
		if s.Fixes == nil {
			s.Fixes = make(map[model.CommandID]FixSubmissionState)
		}
		if _, exists := s.Fixes[event.CommandID]; exists {
			return fmt.Errorf("%w: fix command %q already exists", ErrInvalidEvent, event.CommandID)
		}
		s.Fixes[event.CommandID] = FixSubmissionState{
			CommandID:   event.CommandID,
			Message:     event.Message,
			Status:      model.FixSubmitted,
			SubmittedAt: event.SubmittedAt,
		}
		return nil

	case events.PageBugFixed:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		bug, exists := s.Bugs[event.BugID]
		if !exists || bug.Status != model.BugActive {
			return fmt.Errorf("%w: active bug %q does not exist", ErrInvalidEvent, event.BugID)
		}
		fix, exists := s.Fixes[event.CommandID]
		if !exists || fix.Status != model.FixSubmitted || !event.FixedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: fix command %q cannot be accepted", ErrInvalidEvent, event.CommandID)
		}
		bug.Status = model.BugFixed
		bug.FixedAt = event.FixedAt
		s.Bugs[event.BugID] = bug
		fix.Status = model.FixAccepted
		fix.BugID = event.BugID
		fix.CompletedAt = event.FixedAt
		s.Fixes[event.CommandID] = fix
		return nil

	case events.BugFixRejected:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		fix, exists := s.Fixes[event.CommandID]
		if !exists || fix.Status != model.FixSubmitted || event.Reason == "" || !event.RejectedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: fix command %q cannot be rejected", ErrInvalidEvent, event.CommandID)
		}
		fix.Status = model.FixRejected
		fix.CompletedAt = event.RejectedAt
		s.Fixes[event.CommandID] = fix
		return nil

	default:
		return fmt.Errorf("%w: unsupported event %T", ErrInvalidEvent, event)
	}
}

func serverAvailableCapacity(state State, serverID model.ServerID, at time.Time) int64 {
	server, exists := state.Servers[serverID]
	if !exists || server.Status != model.ServerActive {
		return 0
	}
	used := int64(0)
	for _, allocation := range state.Capacity {
		if allocation.ServerID == serverID && allocation.ReleasesAt.After(at) {
			used += allocation.LoadUnits
		}
	}
	return server.CapacityUnits - used
}

func serverHasLoad(state State, serverID model.ServerID, at time.Time) bool {
	for _, allocation := range state.Capacity {
		if allocation.ServerID == serverID && allocation.ReleasesAt.After(at) {
			return true
		}
	}
	return false
}

func ensureRunExists(state State) error {
	if state.Status == RunNotCreated {
		return ErrWorldNotCreated
	}
	return nil
}
