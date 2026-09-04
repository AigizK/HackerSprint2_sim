package simulation

import (
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

var (
	ErrWorldAlreadyCreated  = errors.New("world already created")
	ErrWorldNotCreated      = errors.New("world not created")
	ErrRunCompleted         = errors.New("run completed")
	ErrProductAlreadyExists = errors.New("product already exists")
	ErrProductNotFound      = errors.New("product not found")
	ErrInvalidCommand       = errors.New("invalid command")
	ErrInvalidEvent         = errors.New("invalid event")
	ErrIdempotencyConflict  = errors.New("idempotency key was reused with another payload")
	ErrResourceNotFound     = errors.New("resource not found")
	ErrSiteNotStopped       = errors.New("SITE_NOT_STOPPED")
	ErrSiteNotRunning       = errors.New("SITE_NOT_RUNNING")
	ErrDatabaseNotEmpty     = errors.New("DB_NOT_EMPTY")
	ErrBackupNotReady       = errors.New("BACKUP_NOT_READY")
	ErrInsufficientDisk     = errors.New("INSUFFICIENT_DISK_SPACE")
	ErrDatabaseNotReady     = errors.New("DB_NOT_READY")
	ErrDatabaseBackupStale  = errors.New("DB_BACKUP_STALE")
	ErrSiteConfigConflict   = errors.New("SITE_CONFIG_CONFLICT")
	ErrLastBackendRequired  = fmt.Errorf("%w: LAST_BACKEND_REQUIRED", ErrInvalidCommand)
	ErrServerInUse          = fmt.Errorf("%w: SERVER_IN_USE", ErrInvalidCommand)
	ErrBackendUnavailable   = errors.New("BACKEND_UNAVAILABLE")
	ErrOperationInProgress  = errors.New("OPERATION_IN_PROGRESS")
)

func (s *State) Apply(event events.Event) error {
	if handled, err := s.applyControlEvent(event); handled {
		return err
	}
	if handled, err := s.applyFirewallEvent(event); handled {
		return err
	}
	if handled, err := s.applyDatabaseEvent(event); handled {
		return err
	}
	switch event := event.(type) {
	case events.CostsConfigured:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if len(event.Currency) != 3 || event.Currency != strings.ToUpper(event.Currency) || s.Costs.Currency != "" || !event.ConfiguredAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid costs configuration", ErrInvalidEvent)
		}
		s.Costs.Currency = event.Currency
		return nil
	case events.ServerCommandAccepted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.CommandID == "" || event.OperationID == "" || event.Payload == "" || !event.AcceptedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid server command receipt", ErrInvalidEvent)
		}
		if _, exists := s.Commands[event.CommandID]; exists {
			return ErrIdempotencyConflict
		}
		if err := s.recordCommand(event.CommandID, event.Payload); err != nil {
			return err
		}
		s.Commands[event.CommandID] = event.OperationID
		return nil
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
		if s.InboxMessages == nil {
			s.InboxMessages = make(map[MessageID]InboxMessageState)
		}
		return nil

	case events.ProductAdded:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.ProductID == "" || event.Name == "" || len(event.Description) > 65536 || len(event.Manufacturer) > 1000 ||
			event.PriceMinor <= 0 || event.Version > 1 ||
			event.ViewProbabilityPPM > ProbabilityScale ||
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
		manufacturer := event.Manufacturer
		if manufacturer == "" {
			manufacturer = "Unknown"
		}
		s.Products[event.ProductID] = ProductState{
			ID:                 event.ProductID,
			Name:               event.Name,
			Description:        event.Description,
			Manufacturer:       manufacturer,
			PriceMinor:         event.PriceMinor,
			Available:          event.Available,
			Version:            1,
			ViewProbabilityPPM: event.ViewProbabilityPPM,
			AddedAt:            event.AddedAt,
			UpdatedAt:          event.AddedAt,
		}
		return nil

	case events.InboxMessageDelivered:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if event.MessageID == "" || len(event.MessageID) > 128 || event.SenderEmail == "" || len(event.SenderEmail) > 320 ||
			!strings.Contains(event.SenderEmail, "@") || len(event.Subject) > 1000 || len(event.Description) > 65536 ||
			event.SentAt.Before(s.Clock.StartedAt) || event.SentAt.After(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid inbox message", ErrInvalidEvent)
		}
		if _, exists := s.InboxMessages[event.MessageID]; exists {
			return fmt.Errorf("%w: inbox message %q already exists", ErrInvalidEvent, event.MessageID)
		}
		s.InboxMessages[event.MessageID] = InboxMessageState{ID: event.MessageID, SenderEmail: event.SenderEmail,
			SentAt: event.SentAt, Subject: event.Subject, Description: event.Description}
		s.markScheduled(event, event.SentAt)
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
			(event.RequestedDuration > 0 && event.RequestedDuration < MinExplicitAdvance) ||
			(event.StopOnLogError && event.RequestedDuration < MinExplicitAdvance) {
			return fmt.Errorf("%w: invalid time durations", ErrInvalidEvent)
		}
		normalizedCodes, validCodes := normalizeLogErrorCodes(event.LogErrorCodes)
		if !validCodes || len(event.LogErrorCodes) > 0 && !event.StopOnLogError || !reflect.DeepEqual(normalizedCodes, event.LogErrorCodes) {
			return fmt.Errorf("%w: invalid log error filter", ErrInvalidEvent)
		}
		expectedApplied := max(event.RealElapsed, event.RequestedDuration)
		if remaining := s.Clock.EndsAt.Sub(event.From); expectedApplied > remaining {
			expectedApplied = remaining
		}
		if (event.StopOnLogError && event.AppliedDuration > expectedApplied) ||
			(!event.StopOnLogError && event.AppliedDuration != expectedApplied) {
			return fmt.Errorf("%w: applied duration does not follow max(real, requested)", ErrInvalidEvent)
		}
		if event.CommandID != "" {
			if err := s.recordCommand(event.CommandID, timeCommandPayload(event.RealElapsed, event.RequestedDuration, event.StopOnLogError, event.LogErrorCodes)); err != nil {
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
		s.EndReason = event.Reason
		return nil

	case events.PageConfigured:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		if !validPage(event.Page) || event.LoadUnits <= 0 || event.HoldDuration <= 0 || event.BaseLatency < 0 ||
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
			BaseLatency:  event.BaseLatency,
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

	case events.OperationProgressed:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		operation, exists := s.Operations[event.OperationID]
		if !exists || operation.Status != model.OperationStatusRunning || event.ProgressPPM > ProbabilityScale ||
			event.ProgressPPM < operation.ProgressPPM || !event.UpdatedAt.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: operation %q cannot progress", ErrInvalidEvent, event.OperationID)
		}
		operation.ProgressPPM = event.ProgressPPM
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
		role := event.Role
		if role == "" {
			role = model.ServerRoleBackend
		}
		validResources := role == model.ServerRoleBackend && event.CapacityUnits > 0 ||
			role == model.ServerRoleDatabase && event.DiskBytes > 0 && event.ConnectionLimit > 0 && event.ConnectionHold > 0
		if event.OperationID == "" || event.ServerID == "" || !validResources ||
			event.CostPerHourMinor < 0 || event.CostPerMonthMinor < 0 || !event.StartedAt.Equal(s.Clock.CurrentTime) || event.ReadyAt.Before(event.StartedAt) {
			return fmt.Errorf("%w: invalid server provisioning event", ErrInvalidEvent)
		}
		if s.Servers == nil {
			s.Servers = make(map[model.ServerID]ServerState)
		}
		if _, exists := s.Servers[event.ServerID]; exists {
			return fmt.Errorf("%w: server %q already exists", ErrInvalidEvent, event.ServerID)
		}
		s.Servers[event.ServerID] = ServerState{
			ID:                event.ServerID,
			Name:              event.Name,
			CredentialID:      event.CredentialID,
			OperationID:       event.OperationID,
			Status:            model.ServerProvisioning,
			InstanceType:      event.InstanceType,
			Role:              role,
			CapacityUnits:     event.CapacityUnits,
			DiskBytes:         event.DiskBytes,
			ConnectionLimit:   event.ConnectionLimit,
			ConnectionHold:    event.ConnectionHold,
			CostPerHourMinor:  event.CostPerHourMinor,
			CostPerMonthMinor: event.CostPerMonthMinor,
			StartedAt:         event.StartedAt,
			ReadyAt:           event.ReadyAt,
		}
		server := s.Servers[event.ServerID]
		if server.Name == "" {
			server.Name = string(server.ID)
			s.Servers[event.ServerID] = server
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
		if database, hosted := databaseOnServer(*s, event.ServerID); hosted {
			delete(s.Databases, database.ID)
			for growthID, growth := range s.PendingDatabaseGrowth {
				if growth.DatabaseID == database.ID {
					delete(s.PendingDatabaseGrowth, growthID)
				}
			}
		}
		return nil

	case events.BackendAvailabilityChanged:
		if !validFactTime(*s, event.ChangedAt) || !validBackendAvailabilityEvent(*s, event) {
			return fmt.Errorf("%w: invalid backend availability transition", ErrInvalidEvent)
		}
		s.BackendUnavailable = !event.Available
		if s.Site.Status == model.SiteRunning {
			if !event.Available && s.Site.UnavailableSince.IsZero() {
				s.Site.UnavailableSince = event.ChangedAt
			}
			if event.Available && siteDependenciesAvailable(*s) && !s.Site.UnavailableSince.IsZero() {
				s.Site.DowntimeDuration += event.ChangedAt.Sub(s.Site.UnavailableSince)
				s.Site.UnavailableSince = time.Time{}
			}
		}
		return nil

	case events.PageRequestStarted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		profile := model.ClientProfile{SourceIP: event.SourceIP, UserAgent: event.UserAgent, RegionCode: event.RegionCode}
		profilePresent := event.SourceIP != "" || event.UserAgent != "" || event.RegionCode != ""
		if event.RequestID == "" || (event.Source == model.RequestSourceVisitor && event.VisitorID == "") ||
			(event.Source != model.RequestSourceVisitor && event.Source != model.RequestSourceProbe) ||
			event.LoadUnits < 0 || !validFactTime(*s, event.StartedAt) || (profilePresent && validateClientProfile(profile) != nil) {
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
		if event.Source == model.RequestSourceProbe {
			if err := s.recordCommand(model.CommandID(event.RequestID), fmt.Sprintf("probe:%s:%s", event.Page, event.ProductID)); err != nil {
				return err
			}
		}
		s.Requests[event.RequestID] = PageRequestState{
			ID: event.RequestID, Source: event.Source, VisitorID: event.VisitorID, Page: event.Page,
			ProductID: event.ProductID, SourceIP: event.SourceIP, UserAgent: event.UserAgent, RegionCode: event.RegionCode,
			LoadUnits: event.LoadUnits, Status: model.PageRequestInProgress, StartedAt: event.StartedAt,
		}
		return nil

	case events.PageRequestAccepted:
		if err := ensureRunning(*s); err != nil {
			return err
		}
		request, exists := s.Requests[event.RequestID]
		if !exists || request.Status != model.PageRequestInProgress || request.LoadUnits <= 0 || event.FirewallRuleID != request.FirewallRuleID {
			return fmt.Errorf("%w: request %q cannot be accepted", ErrInvalidEvent, event.RequestID)
		}
		server, exists := s.Servers[event.ServerID]
		if !exists || server.Status != model.ServerActive || !validFactTime(*s, event.AcceptedAt) ||
			!event.ReleasesAt.After(event.AcceptedAt) {
			return fmt.Errorf("%w: server %q cannot accept request", ErrInvalidEvent, event.ServerID)
		}
		page := s.Pages[request.Page]
		if event.ReleasesAt != event.AcceptedAt.Add(page.HoldDuration) ||
			serverAvailableCapacity(*s, server.ID, request.Page, event.AcceptedAt) < request.LoadUnits {
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
		request.DatabaseID = event.DatabaseID
		request.FirewallRuleID = event.FirewallRuleID
		request.ReleasesAt = event.ReleasesAt
		s.Requests[event.RequestID] = request
		return nil

	case events.PageRequestCompleted:
		request, exists := s.Requests[event.RequestID]
		if !exists {
			return fmt.Errorf("%w: request %q does not exist", ErrInvalidEvent, event.RequestID)
		}
		if request.Status != model.PageRequestInProgress || !validFactTime(*s, event.CompletedAt) ||
			(request.LoadUnits > 0 && event.ServerID != request.ServerID) || event.DatabaseID != request.DatabaseID ||
			event.FirewallRuleID != request.FirewallRuleID || event.Latency < 0 ||
			(event.StatusCode != 200 && event.StatusCode != 500 && event.StatusCode != 503) ||
			(event.StatusCode == 200 && event.ErrorCode != "") {
			return fmt.Errorf("%w: request %q cannot be completed", ErrInvalidEvent, event.RequestID)
		}
		request.StatusCode = event.StatusCode
		request.ErrorCode = event.ErrorCode
		request.Message = event.Message
		request.Latency = event.Latency
		request.CompletedAt = event.CompletedAt
		request.FirewallRuleID = event.FirewallRuleID
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
		if !exists || request.Status != model.PageRequestInProgress || event.StatusCode < 400 || event.FirewallRuleID != request.FirewallRuleID ||
			event.ErrorCode == "" || !validFactTime(*s, event.RejectedAt) {
			return fmt.Errorf("%w: request %q cannot be rejected", ErrInvalidEvent, event.RequestID)
		}
		request.Status = model.PageRequestFailed
		request.ServerID = event.ServerID
		request.DatabaseID = event.DatabaseID
		request.StatusCode = event.StatusCode
		request.ErrorCode = event.ErrorCode
		request.Message = event.Message
		request.FirewallRuleID = event.FirewallRuleID
		request.CompletedAt = event.RejectedAt
		s.Requests[event.RequestID] = request
		return nil

	case events.InfrastructureCostAccrued:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		server, exists := s.Servers[event.ServerID]
		monthly := server.CostPerMonthMinor > 0
		validBilling := monthly && event.TotalAmountMinor > server.AccruedCostMinor && event.AmountMinor == event.TotalAmountMinor-server.AccruedCostMinor ||
			!monthly && event.BilledHours > server.BilledHours
		if !exists || event.AmountMinor < 0 || !validBilling || !event.To.Equal(s.Clock.CurrentTime) {
			return fmt.Errorf("%w: invalid infrastructure cost", ErrInvalidEvent)
		}
		if monthly {
			server.AccruedCostMinor = event.TotalAmountMinor
		} else {
			server.BilledHours = event.BilledHours
		}
		s.Servers[event.ServerID] = server
		s.Costs.ServerCostMinor += event.AmountMinor
		return nil

	case events.VisitorArrived:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		profile := model.ClientProfile{SourceIP: event.SourceIP, UserAgent: event.UserAgent, RegionCode: event.RegionCode}
		profilePresent := event.SourceIP != "" || event.UserAgent != "" || event.RegionCode != ""
		if event.VisitorID == "" || !validFactTime(*s, event.ArrivedAt) || (profilePresent && validateClientProfile(profile) != nil) {
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
		s.Visitors[event.VisitorID] = VisitorState{ID: event.VisitorID, SourceIP: event.SourceIP, UserAgent: event.UserAgent,
			RegionCode: event.RegionCode, ArrivedAt: event.ArrivedAt}
		s.markScheduled(event, event.ArrivedAt)
		return nil

	case events.TrafficAttackStarted:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		profile := model.ClientProfile{UserAgent: event.UserAgent, RegionCode: event.RegionCode}
		if event.SourceCIDR != "" {
			if prefix, err := netip.ParsePrefix(event.SourceCIDR); err != nil || prefix != prefix.Masked() {
				return fmt.Errorf("%w: invalid traffic attack source", ErrInvalidEvent)
			} else {
				profile.SourceIP = prefix.Addr().String()
			}
		}
		profilePresent := event.SourceCIDR != "" || event.UserAgent != "" || event.RegionCode != ""
		if event.AttackID == "" || (event.Kind != model.AttackDDoS && event.Kind != model.AttackBruteForce) || !validPage(event.TargetPage) ||
			event.RequestsPerMinute <= 0 || event.LoadUnitsPerRequest <= 0 ||
			(event.Resolution != model.AttackScaleOrExpiry) ||
			!event.ExpectedEndAt.After(event.StartedAt) || !validFactTime(*s, event.StartedAt) || (profilePresent && validateClientProfile(profile) != nil) {
			return fmt.Errorf("%w: invalid traffic attack", ErrInvalidEvent)
		}
		if _, exists := s.ActiveAttacks[event.AttackID]; exists {
			return fmt.Errorf("%w: attack already active", ErrInvalidEvent)
		}
		s.ActiveAttacks[event.AttackID] = AttackState{ID: event.AttackID, Kind: event.Kind, TargetPage: event.TargetPage,
			RequestsPerMinute: event.RequestsPerMinute, LoadUnitsPerRequest: event.LoadUnitsPerRequest,
			SourceCIDR: event.SourceCIDR, UserAgent: event.UserAgent, RegionCode: event.RegionCode,
			Resolution: event.Resolution, ExpectedEndAt: event.ExpectedEndAt, StartedAt: event.StartedAt}
		s.markScheduled(event, event.StartedAt)
		return nil

	case events.TrafficAttackEnded:
		if err := ensureRunExists(*s); err != nil {
			return err
		}
		attack, exists := s.ActiveAttacks[event.AttackID]
		if !exists {
			if _, resolved := s.ResolvedAttacks[event.AttackID]; resolved && validFactTime(*s, event.EndedAt) {
				s.markScheduled(event, event.EndedAt)
				return nil
			}
			return fmt.Errorf("%w: traffic attack cannot end", ErrInvalidEvent)
		}
		if event.EndedAt.Before(attack.ExpectedEndAt) || !validFactTime(*s, event.EndedAt) {
			return fmt.Errorf("%w: traffic attack cannot end", ErrInvalidEvent)
		}
		delete(s.ActiveAttacks, event.AttackID)
		s.ResolvedAttacks[event.AttackID] = struct{}{}
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

func serverAvailableCapacity(state State, serverID model.ServerID, page model.PageType, at time.Time) int64 {
	server, exists := state.Servers[serverID]
	if !exists || server.Status != model.ServerActive || (server.Role != "" && server.Role != model.ServerRoleBackend) {
		return 0
	}
	if serverDiskFull(state, server) {
		return 0
	}
	if state.CapacityIndexedAt.Equal(at) {
		return server.CapacityUnits - state.UsedCapacityByServer[serverID] - ddosLoadPerActiveServer(state, page, at)
	}
	used := int64(0)
	for _, allocation := range state.Capacity {
		if allocation.ServerID == serverID && allocation.ReleasesAt.After(at) {
			used += allocation.LoadUnits
		}
	}
	return server.CapacityUnits - used - ddosLoadPerActiveServer(state, page, at)
}

func serverDiskFull(state State, server ServerState) bool {
	return server.DiskBytes > 0 && diskUsage(state, server.ID).FreeBytes == 0
}

func allActiveBackendsDiskFull(state State) bool {
	found := false
	for _, server := range state.Servers {
		if server.Status != model.ServerActive || (server.Role != "" && server.Role != model.ServerRoleBackend) {
			continue
		}
		found = true
		if !serverDiskFull(state, server) {
			return false
		}
	}
	return found
}

func ddosLoadPerActiveServer(state State, page model.PageType, at time.Time) int64 {
	activeServers := int64(0)
	for _, server := range state.Servers {
		if server.Status == model.ServerActive && (server.Role == "" || server.Role == model.ServerRoleBackend) && !serverDiskFull(state, server) {
			activeServers++
		}
	}
	if activeServers == 0 {
		return 0
	}
	hold := state.Pages[page].HoldDuration
	var total int64
	for _, attack := range state.ActiveAttacks {
		if attack.TargetPage != page || attack.Resolution != model.AttackScaleOrExpiry {
			continue
		}
		if profile, present := attackClientProfile(attack); present && evaluateFirewall(state, profile, at).Action == model.FirewallDeny {
			continue
		}
		concurrent := (attack.RequestsPerMinute*int64(hold) + int64(time.Minute) - 1) / int64(time.Minute)
		total += concurrent * attack.LoadUnitsPerRequest
	}
	return (total + activeServers - 1) / activeServers
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
