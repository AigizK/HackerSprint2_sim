package simulation

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
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
	ErrDeploymentLocked      = errors.New("deployment is locked")
	ErrDeploymentInProgress  = errors.New("deployment is already in progress")
	ErrDeploymentApplied     = errors.New("deployment is already applied")
	ErrIdempotencyConflict   = errors.New("idempotency key was reused with another payload")
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
		if event.PurchaseID == "" || event.PriceMinor != product.PriceMinor || !validFactTime(*s, event.PurchasedAt) {
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

	case events.WorldScheduleCreated:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if !event.CreatedAt.Equal(s.Clock.StartedAt) || !s.Clock.CurrentTime.Equal(s.Clock.StartedAt) {
			return fmt.Errorf("%w: invalid world schedule", ErrInvalidEvent)
		}
		schedule := append(events.EventSchedule(nil), event.Schedule...)
		sort.SliceStable(schedule, func(i, j int) bool {
			if schedule[i].OccursAt.Equal(schedule[j].OccursAt) {
				return schedule[i].Sequence < schedule[j].Sequence
			}
			return schedule[i].OccursAt.Before(schedule[j].OccursAt)
		})
		expectedSequence := uint64(len(s.Schedule) + 1)
		for index, item := range schedule {
			if item.Sequence != expectedSequence+uint64(index) || item.Event == nil || item.OccursAt.Before(s.Clock.StartedAt) || item.OccursAt.After(s.Clock.EndsAt) {
				return fmt.Errorf("%w: invalid scheduled event", ErrInvalidEvent)
			}
			if len(s.Schedule) > 0 && index == 0 && item.OccursAt.Before(s.Schedule[len(s.Schedule)-1].OccursAt) {
				return fmt.Errorf("%w: schedule chunk is not chronological", ErrInvalidEvent)
			}
		}
		// Schedule is immutable after bootstrap. Allocate a new backing array so
		// decision clones can safely share it without copying the whole world.
		combined := make(events.EventSchedule, 0, len(s.Schedule)+len(schedule))
		combined = append(combined, s.Schedule...)
		s.Schedule = append(combined, schedule...)
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
		if event.CommandID != "" {
			if err := s.recordCommand(event.CommandID, fmt.Sprintf("time:%d:%d", event.RealElapsed, event.RequestedDuration)); err != nil {
				return err
			}
		}
		// Everything completed before the command started is no longer needed by
		// aggregate decisions. IDs remain in compact seen-sets for idempotency.
		s.pruneDerivedState(event.From)
		s.Clock.CurrentTime = event.To
		return nil

	case events.RunEnded:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.Reason == "" || !event.CompletedAt.Equal(s.Clock.CurrentTime) || event.CompletedAt.After(s.Clock.EndsAt) {
			return fmt.Errorf("%w: invalid run completion", ErrInvalidEvent)
		}
		s.Status = RunCompleted
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

	case events.DeploymentDefined:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.DeploymentID == "" || event.Sequence <= 0 || event.Name == "" || event.Description == "" ||
			event.CostMinor < 0 || event.Duration <= 0 || event.FailureProbabilityPPM > ProbabilityScale ||
			!event.DefinedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid deployment definition", ErrInvalidEvent)
		}
		if s.Deployments == nil {
			s.Deployments = make(map[model.DeploymentID]DeploymentState)
		}
		if _, exists := s.Deployments[event.DeploymentID]; exists {
			return fmt.Errorf("%w: deployment %q already exists", ErrInvalidEvent, event.DeploymentID)
		}
		for _, deployment := range s.Deployments {
			if deployment.Sequence == event.Sequence {
				return fmt.Errorf("%w: deployment sequence %d already exists", ErrInvalidEvent, event.Sequence)
			}
		}
		s.Deployments[event.DeploymentID] = DeploymentState{
			ID:                    event.DeploymentID,
			Sequence:              event.Sequence,
			Name:                  event.Name,
			Description:           event.Description,
			CostMinor:             event.CostMinor,
			Duration:              event.Duration,
			FailureProbabilityPPM: event.FailureProbabilityPPM,
			Status:                model.DeploymentStatusLocked,
			DefinedAt:             event.DefinedAt,
		}
		return nil

	case events.DeploymentUnlocked:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		if !exists || deployment.Status != model.DeploymentStatusLocked || !deploymentCanUnlock(*s, deployment) ||
			!event.UnlockedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: deployment %q cannot be unlocked", ErrInvalidEvent, event.DeploymentID)
		}
		deployment.Status = model.DeploymentStatusAvailable
		s.Deployments[event.DeploymentID] = deployment
		return nil

	case events.DeploymentPageLoadEffectDefined:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		_, pageExists := s.Pages[event.Page]
		if !exists || (deployment.Status != model.DeploymentStatusLocked && deployment.Status != model.DeploymentStatusAvailable) || !pageExists ||
			!validPage(event.Page) || event.NewLoadUnits <= 0 || event.NewHoldDuration <= 0 ||
			!event.DefinedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid deployment page-load effect", ErrInvalidEvent)
		}
		if s.DeploymentPageLoadEffects == nil {
			s.DeploymentPageLoadEffects = make(map[model.DeploymentID][]DeploymentPageLoadEffectState)
		}
		s.DeploymentPageLoadEffects[event.DeploymentID] = append(
			s.DeploymentPageLoadEffects[event.DeploymentID],
			DeploymentPageLoadEffectState{
				Page:            event.Page,
				NewLoadUnits:    event.NewLoadUnits,
				NewHoldDuration: event.NewHoldDuration,
			},
		)
		return nil

	case events.DeploymentBugProbabilityEffectDefined:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		if !exists || (deployment.Status != model.DeploymentStatusLocked && deployment.Status != model.DeploymentStatusAvailable) ||
			event.BugID == "" || event.NewProbabilityPPM > ProbabilityScale || !event.DefinedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid deployment bug effect", ErrInvalidEvent)
		}
		if _, exists := s.Bugs[event.BugID]; !exists {
			return fmt.Errorf("%w: bug %q does not exist", ErrInvalidEvent, event.BugID)
		}
		if s.DeploymentBugEffects == nil {
			s.DeploymentBugEffects = make(map[model.DeploymentID][]DeploymentBugProbabilityEffectState)
		}
		s.DeploymentBugEffects[event.DeploymentID] = append(
			s.DeploymentBugEffects[event.DeploymentID],
			DeploymentBugProbabilityEffectState{BugID: event.BugID, NewProbabilityPPM: event.NewProbabilityPPM},
		)
		return nil

	case events.DeploymentFutureDurationEffectDefined:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		if !exists || (deployment.Status != model.DeploymentStatusLocked && deployment.Status != model.DeploymentStatusAvailable) ||
			event.ReductionPPM == 0 || event.ReductionPPM >= ProbabilityScale || event.MinimumDuration <= 0 ||
			!event.DefinedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid future deployment-duration effect", ErrInvalidEvent)
		}
		s.DeploymentDurationEffects[event.DeploymentID] = append(s.DeploymentDurationEffects[event.DeploymentID], DeploymentFutureDurationEffectState{
			ReductionPPM: event.ReductionPPM, MinimumDuration: event.MinimumDuration,
		})
		return nil

	case events.DeploymentNewBugEffectDefined:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		if !exists || (deployment.Status != model.DeploymentStatusLocked && deployment.Status != model.DeploymentStatusAvailable) ||
			event.BugID == "" || !validPage(event.Page) || event.FailureProbabilityPPM > ProbabilityScale ||
			event.FixMessage == "" || event.FixMessageHash == "" || !event.DefinedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid deployment new-bug effect", ErrInvalidEvent)
		}
		if event.ProductID != "" {
			if _, exists := s.Products[event.ProductID]; !exists {
				return ErrProductNotFound
			}
		}
		if _, exists := s.Bugs[event.BugID]; exists {
			return fmt.Errorf("%w: bug %q already exists", ErrInvalidEvent, event.BugID)
		}
		for _, effects := range s.DeploymentNewBugEffects {
			for _, effect := range effects {
				if effect.BugID == event.BugID {
					return fmt.Errorf("%w: future bug %q already exists", ErrInvalidEvent, event.BugID)
				}
			}
		}
		s.DeploymentNewBugEffects[event.DeploymentID] = append(s.DeploymentNewBugEffects[event.DeploymentID], DeploymentNewBugEffectState{
			BugID: event.BugID, Page: event.Page, ProductID: event.ProductID,
			FailureProbabilityPPM: event.FailureProbabilityPPM, FixMessage: event.FixMessage, FixMessageHash: event.FixMessageHash,
		})
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
		if err := s.recordCommand(event.CommandID, fmt.Sprintf("scale:%s:%d", event.OperationID, event.DesiredInstances)); err != nil {
			return err
		}
		s.DesiredInstances = event.DesiredInstances
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

	case events.DeploymentStarted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		operation, operationExists := s.Operations[event.OperationID]
		if !exists || deployment.Status != model.DeploymentStatusAvailable || s.ActiveDeployment != "" ||
			!operationExists || operation.Kind != model.OperationDeployment || operation.Status != model.OperationStatusRunning ||
			event.CommandID == "" || !event.StartedAt.Equal(s.Clock.CurrentTime) ||
			event.ExpectedCompletionAt != event.StartedAt.Add(deployment.Duration) {
			return fmt.Errorf("%w: deployment %q cannot be started", ErrInvalidEvent, event.DeploymentID)
		}
		if _, exists := s.Commands[event.CommandID]; exists {
			return fmt.Errorf("%w: command %q already exists", ErrInvalidEvent, event.CommandID)
		}
		s.Commands[event.CommandID] = event.OperationID
		if err := s.recordCommand(event.CommandID, fmt.Sprintf("deployment:%s:%s", event.DeploymentID, event.OperationID)); err != nil {
			return err
		}
		deployment.Status = model.DeploymentStatusRunning
		deployment.OperationID = event.OperationID
		deployment.StartedAt = event.StartedAt
		deployment.ExpectedCompletionAt = event.ExpectedCompletionAt
		s.Deployments[event.DeploymentID] = deployment
		s.ActiveDeployment = event.DeploymentID
		return nil

	case events.DeploymentCompleted:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		if !exists || deployment.Status != model.DeploymentStatusRunning || deployment.OperationID != event.OperationID ||
			s.ActiveDeployment != event.DeploymentID || event.CompletedAt.Before(deployment.ExpectedCompletionAt) ||
			!event.CompletedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: deployment %q cannot complete", ErrInvalidEvent, event.DeploymentID)
		}
		deployment.Status = model.DeploymentStatusApplied
		deployment.CompletedAt = event.CompletedAt
		s.Deployments[event.DeploymentID] = deployment
		s.ActiveDeployment = ""
		return nil

	case events.DeploymentFailed:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		if !exists || deployment.Status != model.DeploymentStatusRunning || deployment.OperationID != event.OperationID ||
			s.ActiveDeployment != event.DeploymentID || event.ErrorCode == "" || event.Message == "" ||
			event.FailedAt.Before(deployment.ExpectedCompletionAt) || !event.FailedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: deployment %q cannot fail", ErrInvalidEvent, event.DeploymentID)
		}
		deployment.Status = model.DeploymentStatusFailed
		deployment.CompletedAt = event.FailedAt
		s.Deployments[event.DeploymentID] = deployment
		s.ActiveDeployment = ""
		return nil

	case events.PageLoadChanged:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		page, pageExists := s.Pages[event.Page]
		if !exists || deployment.Status != model.DeploymentStatusApplied || !pageExists ||
			page.LoadUnits != event.OldLoadUnits || page.HoldDuration != event.OldHoldDuration ||
			event.NewLoadUnits <= 0 || event.NewHoldDuration <= 0 || !event.ChangedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid page-load change", ErrInvalidEvent)
		}
		page.LoadUnits = event.NewLoadUnits
		page.HoldDuration = event.NewHoldDuration
		s.Pages[event.Page] = page
		return nil

	case events.PageBugProbabilityChanged:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		bug, bugExists := s.Bugs[event.BugID]
		if !exists || deployment.Status != model.DeploymentStatusApplied || !bugExists ||
			bug.FailureProbabilityPPM != event.OldProbabilityPPM || event.NewProbabilityPPM > ProbabilityScale ||
			!event.ChangedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid page-bug probability change", ErrInvalidEvent)
		}
		bug.FailureProbabilityPPM = event.NewProbabilityPPM
		s.Bugs[event.BugID] = bug
		return nil

	case events.DeploymentDurationChanged:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		source, sourceExists := s.Deployments[event.SourceDeploymentID]
		deployment, exists := s.Deployments[event.DeploymentID]
		if !sourceExists || source.Status != model.DeploymentStatusApplied || !exists || deployment.Status != model.DeploymentStatusLocked ||
			deployment.Duration != event.OldDuration || event.NewDuration <= 0 || event.NewDuration > event.OldDuration ||
			!event.ChangedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid deployment-duration change", ErrInvalidEvent)
		}
		deployment.Duration = event.NewDuration
		s.Deployments[event.DeploymentID] = deployment
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

	case events.OperationFailed:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		operation, exists := s.Operations[event.OperationID]
		if !exists || operation.Status != model.OperationStatusRunning || event.ErrorCode == "" || event.Message == "" ||
			!event.FailedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: operation %q cannot fail", ErrInvalidEvent, event.OperationID)
		}
		operation.Status = model.OperationStatusFailed
		operation.CompletedAt = event.FailedAt
		operation.ErrorCode = event.ErrorCode
		operation.Message = event.Message
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
		if s.DesiredInstances < len(s.Servers) {
			s.DesiredInstances = len(s.Servers)
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
		if event.RequestID == "" || (event.Source == model.RequestSourceVisitor && event.VisitorID == "") ||
			(event.Source != model.RequestSourceVisitor && event.Source != model.RequestSourceProbe) ||
			event.LoadUnits < 0 || !validFactTime(*s, event.StartedAt) {
			return fmt.Errorf("%w: invalid page request event", ErrInvalidEvent)
		}
		if page, configured := s.Pages[event.Page]; configured && event.LoadUnits != page.LoadUnits {
			return fmt.Errorf("%w: request load does not match page configuration", ErrInvalidEvent)
		}
		if s.Requests == nil {
			s.Requests = make(map[model.RequestID]PageRequestState)
		}
		s.pruneExpiredCapacity(event.StartedAt)
		if _, exists := s.SeenRequests[event.RequestID]; exists {
			return fmt.Errorf("%w: request %q already exists", ErrInvalidEvent, event.RequestID)
		}
		if s.SeenRequests == nil {
			s.SeenRequests = make(map[model.RequestID]struct{})
		}
		s.SeenRequests[event.RequestID] = struct{}{}
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
		if !exists || server.Status != model.ServerActive || !validFactTime(*s, event.AcceptedAt) ||
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
		if s.UsedCapacityByServer == nil {
			s.UsedCapacityByServer = make(map[model.ServerID]int64)
		}
		s.UsedCapacityByServer[event.ServerID] += request.LoadUnits
		request.ServerID = event.ServerID
		request.ReleasesAt = event.ReleasesAt
		s.Requests[event.RequestID] = request
		return nil

	case events.CapacityAllocationReleased:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		allocation, exists := s.Capacity[event.RequestID]
		if !exists || s.ActiveDeployment != event.DeploymentID || allocation.ServerID != event.ServerID ||
			!allocation.ReleasesAt.After(event.ReleasedAt) || !event.ReleasedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: capacity allocation %q cannot be released", ErrInvalidEvent, event.RequestID)
		}
		delete(s.Capacity, event.RequestID)
		s.UsedCapacityByServer[event.ServerID] -= allocation.LoadUnits
		request := s.Requests[event.RequestID]
		request.ReleasesAt = event.ReleasedAt
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
		if request.Status != model.PageRequestInProgress || !validFactTime(*s, event.CompletedAt) ||
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
			event.ErrorCode == "" || !validFactTime(*s, event.RejectedAt) {
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
		if err := s.recordCommand(event.CommandID, "fix:"+event.Message); err != nil {
			return err
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

	case events.InfrastructureCostAccrued:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		server, exists := s.Servers[event.ServerID]
		if !exists || event.AmountMinor < 0 || event.BilledHours <= server.BilledHours || !event.To.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid infrastructure cost", ErrInvalidEvent)
		}
		server.BilledHours = event.BilledHours
		s.Servers[event.ServerID] = server
		s.Economy.ServerCostMinor += event.AmountMinor
		return nil

	case events.EconomyConfigured:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.InitialBalanceMinor <= 0 || !event.StopRunOnNegativeBalance || event.ServerBillingPeriod <= 0 ||
			!event.ConfiguredAt.Equal(s.Clock.CurrentTime) || s.Economy.InitialBalanceMinor != 0 {
			return fmt.Errorf("%w: invalid economy configuration", ErrInvalidEvent)
		}
		s.Economy.InitialBalanceMinor = event.InitialBalanceMinor
		s.Economy.StopRunOnNegative = event.StopRunOnNegativeBalance
		s.Economy.ServerBillingPeriod = event.ServerBillingPeriod
		return nil

	case events.DeploymentCostAccrued:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		deployment, exists := s.Deployments[event.DeploymentID]
		if !exists || event.AmountMinor != deployment.CostMinor || !event.AccruedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid deployment cost", ErrInvalidEvent)
		}
		s.Economy.DeploymentCostMinor += event.AmountMinor
		return nil

	case events.RevenueLost:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		if event.VisitorID == "" || event.AmountMinor <= 0 || !validFactTime(*s, event.LostAt) {
			return fmt.Errorf("%w: invalid lost revenue", ErrInvalidEvent)
		}
		s.Economy.LostPurchases++
		s.Economy.LostRevenueMinor += event.AmountMinor
		return nil

	case events.VisitorArrived:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		if event.VisitorID == "" || !validFactTime(*s, event.ArrivedAt) {
			return fmt.Errorf("%w: invalid visitor arrival", ErrInvalidEvent)
		}
		s.pruneExpiredCapacity(event.ArrivedAt)
		if _, exists := s.SeenVisitors[event.VisitorID]; exists {
			return fmt.Errorf("%w: visitor already exists", ErrInvalidEvent)
		}
		if s.SeenVisitors == nil {
			s.SeenVisitors = make(map[model.VisitorID]struct{})
		}
		s.SeenVisitors[event.VisitorID] = struct{}{}
		s.Visitors[event.VisitorID] = VisitorState{ID: event.VisitorID, ArrivedAt: event.ArrivedAt}
		s.markScheduled(event, event.ArrivedAt)
		return nil

	case events.TrafficAttackStarted:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		if event.AttackID == "" || event.Kind != model.AttackDDoS || !validPage(event.TargetPage) ||
			event.RequestsPerMinute <= 0 || event.LoadUnitsPerRequest <= 0 ||
			(event.Resolution != model.AttackScaleOrExpiry && event.Resolution != model.AttackFixOrExpiry && event.Resolution != model.AttackExpiryOnly) ||
			!event.ExpectedEndAt.After(event.StartedAt) || !validFactTime(*s, event.StartedAt) {
			return fmt.Errorf("%w: invalid traffic attack", ErrInvalidEvent)
		}
		if _, exists := s.ActiveAttacks[event.AttackID]; exists {
			return fmt.Errorf("%w: attack already active", ErrInvalidEvent)
		}
		s.ActiveAttacks[event.AttackID] = AttackState{ID: event.AttackID, Kind: event.Kind, TargetPage: event.TargetPage,
			RequestsPerMinute: event.RequestsPerMinute, LoadUnitsPerRequest: event.LoadUnitsPerRequest,
			Resolution: event.Resolution, ExpectedEndAt: event.ExpectedEndAt, FixMessage: event.FixMessage,
			FixMessageHash: event.FixMessageHash, StartedAt: event.StartedAt}
		s.markScheduled(event, event.StartedAt)
		return nil

	case events.TrafficAttackEnded:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		attack, exists := s.ActiveAttacks[event.AttackID]
		if !exists || event.EndedAt.Before(attack.ExpectedEndAt) || !validFactTime(*s, event.EndedAt) {
			return fmt.Errorf("%w: traffic attack cannot end", ErrInvalidEvent)
		}
		delete(s.ActiveAttacks, event.AttackID)
		s.markScheduled(event, event.EndedAt)
		return nil

	case events.ProductSelected:
		visitor, exists := s.Visitors[event.VisitorID]
		if !exists || s.Products[event.ProductID].ID == "" || !validFactTime(*s, event.SelectedAt) {
			return fmt.Errorf("%w: invalid product selection", ErrInvalidEvent)
		}
		visitor.ProductID = event.ProductID
		s.Visitors[event.VisitorID] = visitor
		return nil

	case events.PurchaseIntentCreated:
		visitor, exists := s.Visitors[event.VisitorID]
		if !exists || visitor.ProductID != event.ProductID || event.PurchaseID == "" || !validFactTime(*s, event.CreatedAt) {
			return fmt.Errorf("%w: invalid purchase intent", ErrInvalidEvent)
		}
		return nil

	case events.VisitorJourneyCompleted:
		visitor, exists := s.Visitors[event.VisitorID]
		if !exists || event.Outcome == "" || !validFactTime(*s, event.CompletedAt) {
			return fmt.Errorf("%w: invalid visitor completion", ErrInvalidEvent)
		}
		visitor.Outcome, visitor.CompletedAt = event.Outcome, event.CompletedAt
		s.Visitors[event.VisitorID] = visitor
		return nil

	default:
		return fmt.Errorf("%w: unsupported event %T", ErrInvalidEvent, event)
	}
}

func validFactTime(state State, at time.Time) bool {
	return !at.Before(state.Clock.StartedAt) && !at.After(state.Clock.CurrentTime)
}

func (s *State) recordCommand(commandID model.CommandID, payload string) error {
	if s.CommandPayloads == nil {
		s.CommandPayloads = make(map[model.CommandID]string)
	}
	if previous, exists := s.CommandPayloads[commandID]; exists && previous != payload {
		return ErrIdempotencyConflict
	}
	s.CommandPayloads[commandID] = payload
	return nil
}

func (s *State) markScheduled(event events.Event, occursAt time.Time) {
	if s.ScheduleCursor >= len(s.Schedule) {
		return
	}
	next := s.Schedule[s.ScheduleCursor]
	if next.OccursAt.Equal(occursAt) && reflect.DeepEqual(next.Event, event) {
		s.ScheduleCursor++
	}
}

func serverAvailableCapacity(state State, serverID model.ServerID, at time.Time) int64 {
	server, exists := state.Servers[serverID]
	if !exists || server.Status != model.ServerActive {
		return 0
	}
	if state.CapacityIndexedAt.Equal(at) {
		return server.CapacityUnits - state.UsedCapacityByServer[serverID]
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
	if state.CapacityIndexedAt.Equal(at) {
		return state.UsedCapacityByServer[serverID] > 0
	}
	for _, allocation := range state.Capacity {
		if allocation.ServerID == serverID && allocation.ReleasesAt.After(at) {
			return true
		}
	}
	return false
}

// pruneExpiredCapacity maintains a derived O(1) capacity index. It does not
// discard domain history: the append-only event journal remains authoritative.
func (s *State) pruneExpiredCapacity(at time.Time) {
	if at.Before(s.CapacityIndexedAt) {
		return
	}
	if s.UsedCapacityByServer == nil {
		s.UsedCapacityByServer = make(map[model.ServerID]int64)
	}
	for requestID, allocation := range s.Capacity {
		if allocation.ReleasesAt.After(at) {
			continue
		}
		delete(s.Capacity, requestID)
		s.UsedCapacityByServer[allocation.ServerID] -= allocation.LoadUnits
	}
	s.CapacityIndexedAt = at
}

func (s *State) pruneDerivedState(at time.Time) {
	s.pruneExpiredCapacity(at)
	for requestID, request := range s.Requests {
		if request.Status != model.PageRequestInProgress {
			if _, active := s.Capacity[requestID]; !active {
				delete(s.Requests, requestID)
			}
		}
	}
	for visitorID, visitor := range s.Visitors {
		if !visitor.CompletedAt.IsZero() && !visitor.CompletedAt.After(at) {
			delete(s.Visitors, visitorID)
		}
	}
}

func ensureRunExists(state State) error {
	if state.Status == RunNotCreated {
		return ErrWorldNotCreated
	}
	return nil
}

func deploymentCanUnlock(state State, candidate DeploymentState) bool {
	for _, deployment := range state.Deployments {
		if deployment.Sequence >= candidate.Sequence {
			continue
		}
		if deployment.Status != model.DeploymentStatusApplied && deployment.Status != model.DeploymentStatusFailed &&
			deployment.Status != model.DeploymentStatusSucceeded {
			return false
		}
	}
	return true
}
