package simulation

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
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
	state.pruneDerivedState(state.Clock.CurrentTime)
	return state, nil
}

func Decide(runID string, state State, command Command) ([]events.Event, error) {
	if commandID, payload := commandReceipt(command); commandID != "" {
		if previous, exists := state.CommandPayloads[commandID]; exists {
			if previous != payload {
				return nil, ErrIdempotencyConflict
			}
			return nil, nil
		}
	}
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
		to := state.Clock.CurrentTime.Add(applied)
		working := cloneState(state)
		result := make([]events.Event, 0)
		appendEvents := func(items ...events.Event) error {
			for _, event := range items {
				if err := working.Apply(event); err != nil {
					return err
				}
				result = append(result, event)
			}
			return nil
		}
		if err := appendEvents(events.TimeAdvanced{
			CommandID:         command.CommandID,
			From:              state.Clock.CurrentTime,
			To:                to,
			RealElapsed:       command.RealElapsed,
			RequestedDuration: command.RequestedDuration,
			AppliedDuration:   applied,
		}); err != nil {
			return nil, err
		}
		if err := appendEvents(infrastructureCostEvents(working, state.Clock.CurrentTime, to)...); err != nil {
			return nil, err
		}
		// Events inside the interval are evaluated against infrastructure and
		// deployments that were active before the advance. Lifecycle changes
		// becoming ready inside the interval are committed at its end.
		for _, scheduled := range pendingScheduledEvents(state, to) {
			if err := appendEvents(scheduled.Event); err != nil {
				return nil, err
			}
			if arrival, ok := scheduled.Event.(events.VisitorArrived); ok && len(working.Products) > 0 {
				journey, err := decideVisitorMutable(&working, arrival.VisitorID, arrival.ArrivedAt, false)
				if err != nil {
					return nil, err
				}
				result = append(result, journey...)
			}
		}
		if deployment, exists := working.Deployments[working.ActiveDeployment]; exists &&
			!deployment.ExpectedCompletionAt.After(to) {
			if err := appendEvents(deploymentCompletionEvents(working, deployment, to)...); err != nil {
				return nil, err
			}
		}
		completedOperations := make(map[model.OperationID]bool)
		for _, server := range sortedProvisioningServers(working) {
			if server.ReadyAt.After(to) {
				continue
			}
			if err := appendEvents(events.ServerActivated{
				OperationID: server.OperationID,
				ServerID:    server.ID,
				ActivatedAt: to,
			}); err != nil {
				return nil, err
			}
			completedOperations[server.OperationID] = true
		}
		for operationID := range completedOperations {
			if err := appendEvents(events.OperationSucceeded{OperationID: operationID, CompletedAt: to}); err != nil {
				return nil, err
			}
		}
		removedOperations := make(map[model.OperationID]bool)
		for _, server := range sortedDrainingServers(working) {
			if serverHasLoad(working, server.ID, to) {
				continue
			}
			if err := appendEvents(events.ServerRemoved{
				OperationID: server.OperationID,
				ServerID:    server.ID,
				RemovedAt:   to,
			}); err != nil {
				return nil, err
			}
			removedOperations[server.OperationID] = true
		}
		for operationID := range removedOperations {
			allRemoved := true
			for _, server := range working.Servers {
				if server.OperationID == operationID && server.Status == model.ServerDraining && serverHasLoad(working, server.ID, to) {
					allRemoved = false
				}
			}
			if allRemoved {
				if err := appendEvents(events.OperationSucceeded{OperationID: operationID, CompletedAt: to}); err != nil {
					return nil, err
				}
			}
		}
		if to.Equal(state.Clock.EndsAt) {
			if err := appendEvents(events.RunEnded{CompletedAt: to, Reason: "world_completed"}); err != nil {
				return nil, err
			}
		}
		return result, nil

	case SynchronizeRealTime:
		return Decide(runID, state, AdvanceTime{CommandID: command.CommandID, RealElapsed: command.RealElapsed})

	case OpenPage:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.RequestID == "" || command.VisitorID == "" || !validPage(command.Page) {
			return nil, fmt.Errorf("%w: invalid page request", ErrInvalidCommand)
		}
		if _, exists := state.SeenRequests[command.RequestID]; exists {
			return nil, fmt.Errorf("%w: request %q already exists", ErrInvalidCommand, command.RequestID)
		}
		if command.Page != model.PageProductList {
			if _, exists := state.Products[command.ProductID]; !exists {
				return nil, ErrProductNotFound
			}
		}
		pageConfig := state.Pages[command.Page]

		result := []events.Event{events.PageRequestStarted{
			RequestID: command.RequestID,
			Source:    model.RequestSourceVisitor,
			VisitorID: command.VisitorID,
			Page:      command.Page,
			ProductID: command.ProductID,
			LoadUnits: pageConfig.LoadUnits,
			StartedAt: state.Clock.CurrentTime,
		}}
		if attack, blocked := blockingAttack(state, command.Page); blocked {
			message := fmt.Sprintf("%s attack blocks %s", attack.Kind, command.Page)
			if attack.Resolution == model.AttackFixOrExpiry {
				message = "чтоб traffic attack прекратилась, надо сделать фикс с текстом " + attack.FixMessage
			}
			errorCode := model.FailureDDoSMitigationRequired
			if attack.Kind == model.AttackBruteForce {
				errorCode = model.FailureBruteForceMitigationRequired
			}
			return append(result, events.PageRequestRejected{
				RequestID: command.RequestID, StatusCode: 500, ErrorCode: errorCode,
				Message: message, RejectedAt: state.Clock.CurrentTime,
			}), nil
		}
		if state.ActiveDeployment != "" {
			message := fmt.Sprintf("deployment in progress: %s", state.ActiveDeployment)
			return append(result, events.PageRequestRejected{
				RequestID:  command.RequestID,
				StatusCode: 500,
				ErrorCode:  model.FailureDeployment,
				Message:    message,
				RejectedAt: state.Clock.CurrentTime,
			}), nil
		}
		serverID := model.ServerID("")
		if pageConfig.LoadUnits > 0 {
			server, available, accepted := selectServer(state, command.Page, pageConfig.LoadUnits, state.Clock.CurrentTime)
			if !accepted {
				message := fmt.Sprintf("server capacity exceeded: required=%d available=%d", pageConfig.LoadUnits, available)
				return append(result, events.PageRequestRejected{
					RequestID:  command.RequestID,
					StatusCode: 500,
					ErrorCode:  model.FailureServerCapacityExceeded,
					Message:    message,
					RejectedAt: state.Clock.CurrentTime,
				}), nil
			}
			serverID = server.ID
			result = append(result, events.PageRequestAccepted{
				RequestID:  command.RequestID,
				ServerID:   server.ID,
				AcceptedAt: state.Clock.CurrentTime,
				ReleasesAt: state.Clock.CurrentTime.Add(pageConfig.HoldDuration),
			})
		}

		if bug, triggered := triggeredBug(state, command); triggered {
			message := "чтоб этот баг пропал полностью, надо сделать фикс с текстом " + bug.FixMessage
			result = append(result,
				events.PageBugTriggered{
					BugID:       bug.ID,
					RequestID:   command.RequestID,
					LogMessage:  message,
					TriggeredAt: state.Clock.CurrentTime,
				},
				events.PageRequestCompleted{
					RequestID:   command.RequestID,
					ServerID:    serverID,
					StatusCode:  500,
					ErrorCode:   model.FailurePageBug,
					Message:     message,
					Latency:     pageConfig.BaseLatency,
					CompletedAt: state.Clock.CurrentTime,
				},
			)
			return result, nil
		}
		if provider, failed := failedProvider(state, command); failed {
			message := fmt.Sprintf("external provider degraded: %s", provider.ID)
			return append(result, events.PageRequestCompleted{
				RequestID: command.RequestID, ServerID: serverID, StatusCode: 500,
				ErrorCode: model.FailureExternalProvider, Message: message,
				Latency: pageConfig.BaseLatency + provider.AdditionalLatency, CompletedAt: state.Clock.CurrentTime,
			}), nil
		}

		result = append(result, events.PageRequestCompleted{
			RequestID:   command.RequestID,
			ServerID:    serverID,
			StatusCode:  200,
			Latency:     pageConfig.BaseLatency,
			CompletedAt: state.Clock.CurrentTime,
		})
		if command.Page == model.PagePurchase {
			product := state.Products[command.ProductID]
			result = append(result, events.ProductPurchased{
				PurchaseID:  model.PurchaseID(command.RequestID),
				ProductID:   product.ID,
				PriceMinor:  product.PriceMinor,
				PurchasedAt: state.Clock.CurrentTime,
			})
		}
		return result, nil

	case ApplyFix:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.CommandID == "" || strings.TrimSpace(command.Message) == "" {
			return nil, fmt.Errorf("%w: invalid fix command", ErrInvalidCommand)
		}
		if previous, exists := state.Fixes[command.CommandID]; exists {
			if previous.Message == command.Message {
				return nil, nil
			}
			return nil, ErrIdempotencyConflict
		}

		result := []events.Event{events.BugFixSubmitted{
			CommandID:   command.CommandID,
			Message:     command.Message,
			SubmittedAt: state.Clock.CurrentTime,
		}}
		messageHash := sha256.Sum256([]byte(command.Message))
		encodedHash := fmt.Sprintf("%x", messageHash)
		for _, bug := range sortedActiveBugs(state) {
			if bug.FixMessageHash == encodedHash {
				return append(result, events.PageBugFixed{
					CommandID: command.CommandID,
					BugID:     bug.ID,
					FixedAt:   state.Clock.CurrentTime,
				}), nil
			}
		}
		for _, attack := range sortedActiveAttacks(state) {
			if attack.Resolution == model.AttackFixOrExpiry && attack.FixMessageHash == encodedHash {
				return append(result, events.TrafficAttackMitigated{
					AttackID: attack.ID, CommandID: command.CommandID, MitigatedAt: state.Clock.CurrentTime,
				}), nil
			}
		}
		return append(result, events.BugFixRejected{
			CommandID:  command.CommandID,
			Reason:     "FIX_MESSAGE_DOES_NOT_MATCH",
			RejectedAt: state.Clock.CurrentTime,
		}), nil

	case AddServer:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.CommandID == "" || command.OperationID == "" || command.ServerID == "" ||
			command.CapacityUnits <= 0 || command.CostPerHourMinor < 0 {
			return nil, fmt.Errorf("%w: invalid add server command", ErrInvalidCommand)
		}
		if err := ensureNewScaleIdentifiers(state, command.CommandID, command.OperationID); err != nil {
			return nil, err
		}
		if _, exists := state.Servers[command.ServerID]; exists {
			return nil, fmt.Errorf("%w: server %q already exists", ErrInvalidCommand, command.ServerID)
		}
		provisioningDuration := state.Infrastructure.ServerProvisioningDuration
		if provisioningDuration <= 0 {
			return nil, fmt.Errorf("%w: server provisioning duration is not configured", ErrInvalidCommand)
		}
		return []events.Event{
			events.BackendScaleRequested{
				CommandID:        command.CommandID,
				OperationID:      command.OperationID,
				DesiredInstances: len(state.Servers) + 1,
				RequestedAt:      state.Clock.CurrentTime,
			},
			events.OperationQueued{
				OperationID: command.OperationID,
				Kind:        model.OperationScaleBackend,
				QueuedAt:    state.Clock.CurrentTime,
			},
			events.OperationStarted{
				OperationID: command.OperationID,
				StartedAt:   state.Clock.CurrentTime,
			},
			events.ServerProvisioningStarted{
				OperationID:      command.OperationID,
				ServerID:         command.ServerID,
				CapacityUnits:    command.CapacityUnits,
				CostPerHourMinor: command.CostPerHourMinor,
				StartedAt:        state.Clock.CurrentTime,
				ReadyAt:          state.Clock.CurrentTime.Add(provisioningDuration),
			},
		}, nil

	case RemoveServer:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.CommandID == "" || command.OperationID == "" || command.ServerID == "" {
			return nil, fmt.Errorf("%w: invalid remove server command", ErrInvalidCommand)
		}
		if err := ensureNewScaleIdentifiers(state, command.CommandID, command.OperationID); err != nil {
			return nil, err
		}
		server, exists := state.Servers[command.ServerID]
		if !exists || server.Status != model.ServerActive {
			return nil, fmt.Errorf("%w: active server %q does not exist", ErrInvalidCommand, command.ServerID)
		}
		managedServers := 0
		for _, candidate := range state.Servers {
			if candidate.Status == model.ServerProvisioning || candidate.Status == model.ServerActive || candidate.Status == model.ServerDraining {
				managedServers++
			}
		}
		if managedServers <= MinimumBackendInstances {
			return nil, fmt.Errorf("%w: at least %d backend server must remain", ErrInvalidCommand, MinimumBackendInstances)
		}
		result := []events.Event{
			events.BackendScaleRequested{
				CommandID:        command.CommandID,
				OperationID:      command.OperationID,
				DesiredInstances: len(state.Servers) - 1,
				RequestedAt:      state.Clock.CurrentTime,
			},
			events.OperationQueued{
				OperationID: command.OperationID,
				Kind:        model.OperationScaleBackend,
				QueuedAt:    state.Clock.CurrentTime,
			},
			events.OperationStarted{
				OperationID: command.OperationID,
				StartedAt:   state.Clock.CurrentTime,
			},
			events.ServerDrainingStarted{
				OperationID: command.OperationID,
				ServerID:    command.ServerID,
				StartedAt:   state.Clock.CurrentTime,
			},
		}
		if serverHasLoad(state, command.ServerID, state.Clock.CurrentTime) {
			return result, nil
		}
		return append(result,
			events.ServerRemoved{
				OperationID: command.OperationID,
				ServerID:    command.ServerID,
				RemovedAt:   state.Clock.CurrentTime,
			},
			events.OperationSucceeded{
				OperationID: command.OperationID,
				CompletedAt: state.Clock.CurrentTime,
			},
		), nil

	case StartDeployment:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.CommandID == "" || command.DeploymentID == "" || command.OperationID == "" {
			return nil, fmt.Errorf("%w: invalid start deployment command", ErrInvalidCommand)
		}
		if state.ActiveDeployment != "" {
			return nil, ErrDeploymentInProgress
		}
		deployment, exists := state.Deployments[command.DeploymentID]
		if !exists {
			return nil, fmt.Errorf("%w: deployment %q", ErrDeploymentNotFound, command.DeploymentID)
		}
		if deployment.Status == model.DeploymentStatusApplied || deployment.Status == model.DeploymentStatusSucceeded {
			return nil, ErrDeploymentApplied
		}
		if deployment.Status != model.DeploymentStatusAvailable {
			return nil, ErrDeploymentLocked
		}
		if err := ensureNewScaleIdentifiers(state, command.CommandID, command.OperationID); err != nil {
			return nil, err
		}
		result := []events.Event{
			events.OperationQueued{
				OperationID: command.OperationID,
				Kind:        model.OperationDeployment,
				QueuedAt:    state.Clock.CurrentTime,
			},
			events.OperationStarted{
				OperationID: command.OperationID,
				StartedAt:   state.Clock.CurrentTime,
			},
			events.DeploymentStarted{
				CommandID:            command.CommandID,
				DeploymentID:         command.DeploymentID,
				OperationID:          command.OperationID,
				StartedAt:            state.Clock.CurrentTime,
				ExpectedCompletionAt: state.Clock.CurrentTime.Add(deployment.Duration),
			},
		}
		if deployment.CostMinor > 0 {
			result = append(result, events.DeploymentCostAccrued{
				DeploymentID: deployment.ID, AmountMinor: deployment.CostMinor, AccruedAt: state.Clock.CurrentTime,
			})
		}
		requestIDs := make([]string, 0, len(state.Capacity))
		for requestID, allocation := range state.Capacity {
			if allocation.ReleasesAt.After(state.Clock.CurrentTime) {
				requestIDs = append(requestIDs, string(requestID))
			}
		}
		sort.Strings(requestIDs)
		for _, rawID := range requestIDs {
			allocation := state.Capacity[model.RequestID(rawID)]
			result = append(result, events.CapacityAllocationReleased{
				DeploymentID: command.DeploymentID,
				RequestID:    allocation.RequestID,
				ServerID:     allocation.ServerID,
				ReleasedAt:   state.Clock.CurrentTime,
			})
		}
		return result, nil

	case ProbePage:
		generated, err := Decide(runID, state, OpenPage{
			RequestID: command.RequestID, VisitorID: "__probe__", Page: command.Page, ProductID: command.ProductID,
		})
		if err != nil {
			return nil, err
		}
		result := make([]events.Event, 0, len(generated))
		for _, event := range generated {
			switch event := event.(type) {
			case events.PageRequestStarted:
				event.Source = model.RequestSourceProbe
				event.VisitorID = ""
				result = append(result, event)
			case events.ProductPurchased:
				// Probes test availability but never create business revenue.
			default:
				result = append(result, event)
			}
		}
		return result, nil

	case SimulateVisitor:
		return decideVisitor(state, command.VisitorID, state.Clock.CurrentTime, true)

	case SetBackendDesiredInstances:
		return decideDesiredInstances(state, command)

	default:
		return nil, fmt.Errorf("%w: unsupported command %T", ErrInvalidCommand, command)
	}
}

func validPage(page model.PageType) bool {
	return page == model.PageProductList || page == model.PageProduct || page == model.PagePurchase
}

func commandReceipt(command Command) (model.CommandID, string) {
	switch command := command.(type) {
	case AdvanceTime:
		return command.CommandID, timeCommandPayload(command.RealElapsed, command.RequestedDuration)
	case SynchronizeRealTime:
		return command.CommandID, fmt.Sprintf("time:%d:0", command.RealElapsed)
	case ApplyFix:
		return command.CommandID, "fix:" + command.Message
	case StartDeployment:
		return command.CommandID, fmt.Sprintf("deployment:%s:%s", command.DeploymentID, command.OperationID)
	case SetBackendDesiredInstances:
		return command.CommandID, fmt.Sprintf("scale:%s:%d", command.OperationID, command.DesiredInstances)
	case ProbePage:
		return model.CommandID(command.RequestID), fmt.Sprintf("probe:%s:%s", command.Page, command.ProductID)
	default:
		return "", ""
	}
}

func timeCommandPayload(realElapsed, requested time.Duration) string {
	if requested > 0 {
		return fmt.Sprintf("advance:%d", requested)
	}
	return fmt.Sprintf("time:%d:0", realElapsed)
}

func triggeredBug(state State, command OpenPage) (BugState, bool) {
	for _, bug := range sortedActiveBugs(state) {
		if bug.Page != command.Page || (bug.ProductID != "" && bug.ProductID != command.ProductID) {
			continue
		}
		if probabilityHit(state.Seed, bug.ID, command.RequestID, bug.FailureProbabilityPPM) {
			return bug, true
		}
	}
	return BugState{}, false
}

func sortedActiveBugs(state State) []BugState {
	bugIDs := make([]string, 0, len(state.Bugs))
	for id, bug := range state.Bugs {
		if bug.Status == model.BugActive {
			bugIDs = append(bugIDs, string(id))
		}
	}
	sort.Strings(bugIDs)
	bugs := make([]BugState, 0, len(bugIDs))
	for _, rawID := range bugIDs {
		bugs = append(bugs, state.Bugs[model.BugID(rawID)])
	}
	return bugs
}

func failedProvider(state State, command OpenPage) (ProviderState, bool) {
	if command.Page != model.PagePurchase {
		return ProviderState{}, false
	}
	ids := make([]string, 0, len(state.DegradedProviders))
	for id := range state.DegradedProviders {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, rawID := range ids {
		provider := state.DegradedProviders[model.ProviderID(rawID)]
		if probabilityHit(state.Seed, model.BugID("provider:"+rawID), command.RequestID, provider.FailureProbabilityPPM) {
			return provider, true
		}
	}
	return ProviderState{}, false
}

func selectServer(state State, page model.PageType, required int64, at time.Time) (ServerState, int64, bool) {
	serverIDs := make([]string, 0, len(state.Servers))
	for id, server := range state.Servers {
		if server.Status == model.ServerActive {
			serverIDs = append(serverIDs, string(id))
		}
	}
	sort.Strings(serverIDs)
	maxAvailable := int64(0)
	for _, rawID := range serverIDs {
		server := state.Servers[model.ServerID(rawID)]
		available := serverAvailableCapacity(state, server.ID, page, at)
		if available > maxAvailable {
			maxAvailable = available
		}
		if available >= required {
			return server, available, true
		}
	}
	return ServerState{}, maxAvailable, false
}

func blockingAttack(state State, page model.PageType) (AttackState, bool) {
	for _, attack := range sortedActiveAttacks(state) {
		if attack.TargetPage == page && (attack.Resolution == model.AttackFixOrExpiry || attack.Resolution == model.AttackExpiryOnly) {
			return attack, true
		}
	}
	return AttackState{}, false
}

func sortedActiveAttacks(state State) []AttackState {
	ids := make([]string, 0, len(state.ActiveAttacks))
	for id := range state.ActiveAttacks {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	result := make([]AttackState, 0, len(ids))
	for _, id := range ids {
		result = append(result, state.ActiveAttacks[model.AttackID(id)])
	}
	return result
}

func sortedDrainingServers(state State) []ServerState {
	serverIDs := make([]string, 0, len(state.Servers))
	for id, server := range state.Servers {
		if server.Status == model.ServerDraining {
			serverIDs = append(serverIDs, string(id))
		}
	}
	sort.Strings(serverIDs)
	servers := make([]ServerState, 0, len(serverIDs))
	for _, rawID := range serverIDs {
		servers = append(servers, state.Servers[model.ServerID(rawID)])
	}
	return servers
}

func sortedProvisioningServers(state State) []ServerState {
	serverIDs := make([]string, 0, len(state.Servers))
	for id, server := range state.Servers {
		if server.Status == model.ServerProvisioning {
			serverIDs = append(serverIDs, string(id))
		}
	}
	sort.Strings(serverIDs)
	servers := make([]ServerState, 0, len(serverIDs))
	for _, rawID := range serverIDs {
		servers = append(servers, state.Servers[model.ServerID(rawID)])
	}
	return servers
}

func ensureNewScaleIdentifiers(state State, commandID model.CommandID, operationID model.OperationID) error {
	if _, exists := state.Commands[commandID]; exists {
		return fmt.Errorf("%w: command %q already exists", ErrInvalidCommand, commandID)
	}
	if _, exists := state.Operations[operationID]; exists {
		return fmt.Errorf("%w: operation %q already exists", ErrInvalidCommand, operationID)
	}
	return nil
}

func deploymentCompletionEvents(state State, deployment DeploymentState, completedAt time.Time) []events.Event {
	if deploymentFailureHit(state.Seed, deployment.ID, deployment.OperationID, deployment.FailureProbabilityPPM) {
		result := []events.Event{
			events.DeploymentFailed{
				DeploymentID: deployment.ID,
				OperationID:  deployment.OperationID,
				ErrorCode:    "DEPLOYMENT_FAILED",
				Message:      "deployment failed",
				FailedAt:     completedAt,
			},
			events.OperationFailed{
				OperationID: deployment.OperationID,
				ErrorCode:   "DEPLOYMENT_FAILED",
				Message:     "deployment failed",
				FailedAt:    completedAt,
			},
		}
		return appendNextDeploymentUnlocked(result, state, deployment.Sequence, completedAt)
	}

	result := []events.Event{events.DeploymentCompleted{
		DeploymentID: deployment.ID,
		OperationID:  deployment.OperationID,
		CompletedAt:  completedAt,
	}}
	pages := make(map[model.PageType]PageConfigState, len(state.Pages))
	for pageType, page := range state.Pages {
		pages[pageType] = page
	}
	for _, effect := range state.DeploymentPageLoadEffects[deployment.ID] {
		page := pages[effect.Page]
		result = append(result, events.PageLoadChanged{
			DeploymentID:    deployment.ID,
			Page:            effect.Page,
			OldLoadUnits:    page.LoadUnits,
			NewLoadUnits:    effect.NewLoadUnits,
			OldHoldDuration: page.HoldDuration,
			NewHoldDuration: effect.NewHoldDuration,
			ChangedAt:       completedAt,
		})
		page.LoadUnits = effect.NewLoadUnits
		page.HoldDuration = effect.NewHoldDuration
		pages[effect.Page] = page
	}
	bugs := make(map[model.BugID]BugState, len(state.Bugs))
	for bugID, bug := range state.Bugs {
		bugs[bugID] = bug
	}
	for _, effect := range state.DeploymentBugEffects[deployment.ID] {
		bug := bugs[effect.BugID]
		result = append(result, events.PageBugProbabilityChanged{
			DeploymentID:      deployment.ID,
			BugID:             effect.BugID,
			OldProbabilityPPM: bug.FailureProbabilityPPM,
			NewProbabilityPPM: effect.NewProbabilityPPM,
			ChangedAt:         completedAt,
		})
		bug.FailureProbabilityPPM = effect.NewProbabilityPPM
		bugs[effect.BugID] = bug
	}
	durations := make(map[model.DeploymentID]time.Duration, len(state.Deployments))
	for deploymentID, candidate := range state.Deployments {
		durations[deploymentID] = candidate.Duration
	}
	for _, effect := range state.DeploymentDurationEffects[deployment.ID] {
		candidates := make([]DeploymentState, 0)
		for _, candidate := range state.Deployments {
			if candidate.Sequence > deployment.Sequence && candidate.Status == model.DeploymentStatusLocked {
				candidates = append(candidates, candidate)
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Sequence < candidates[j].Sequence })
		for _, candidate := range candidates {
			oldDuration := durations[candidate.ID]
			newDuration := time.Duration(int64(oldDuration) * int64(ProbabilityScale-effect.ReductionPPM) / int64(ProbabilityScale))
			if newDuration < effect.MinimumDuration {
				newDuration = effect.MinimumDuration
			}
			if newDuration > oldDuration {
				newDuration = oldDuration
			}
			result = append(result, events.DeploymentDurationChanged{
				SourceDeploymentID: deployment.ID, DeploymentID: candidate.ID,
				OldDuration: oldDuration, NewDuration: newDuration, ChangedAt: completedAt,
			})
			durations[candidate.ID] = newDuration
		}
	}
	for _, effect := range state.DeploymentNewBugEffects[deployment.ID] {
		result = append(result, events.PageBugActivated{
			BugID: effect.BugID, Page: effect.Page, ProductID: effect.ProductID,
			FailureProbabilityPPM: effect.FailureProbabilityPPM, FixMessage: effect.FixMessage,
			FixMessageHash: effect.FixMessageHash, ActivatedAt: completedAt,
		})
	}
	result = append(result, events.OperationSucceeded{
		OperationID: deployment.OperationID,
		CompletedAt: completedAt,
	})
	return appendNextDeploymentUnlocked(result, state, deployment.Sequence, completedAt)
}

func appendNextDeploymentUnlocked(
	result []events.Event,
	state State,
	completedSequence int,
	at time.Time,
) []events.Event {
	var next DeploymentState
	found := false
	for _, candidate := range state.Deployments {
		if candidate.Status != model.DeploymentStatusLocked || candidate.Sequence <= completedSequence {
			continue
		}
		if !found || candidate.Sequence < next.Sequence {
			next = candidate
			found = true
		}
	}
	if found {
		result = append(result, events.DeploymentUnlocked{DeploymentID: next.ID, UnlockedAt: at})
	}
	return result
}

func deploymentFailureHit(
	seed int64,
	deploymentID model.DeploymentID,
	operationID model.OperationID,
	probability uint32,
) bool {
	if probability == 0 {
		return false
	}
	if probability >= ProbabilityScale {
		return true
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", seed, deploymentID, operationID)))
	roll := binary.BigEndian.Uint64(digest[:8]) % uint64(ProbabilityScale)
	return roll < uint64(probability)
}

func probabilityHit(seed int64, bugID model.BugID, requestID model.RequestID, probability uint32) bool {
	if probability == 0 {
		return false
	}
	if probability >= ProbabilityScale {
		return true
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", seed, bugID, requestID)))
	roll := binary.BigEndian.Uint64(digest[:8]) % uint64(ProbabilityScale)
	return roll < uint64(probability)
}

func deterministicRoll(seed int64, parts ...string) uint32 {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, strings.Join(parts, ":"))))
	return uint32(binary.BigEndian.Uint64(digest[:8]) % uint64(ProbabilityScale))
}

func decideVisitor(state State, visitorID model.VisitorID, at time.Time, includeArrival bool) ([]events.Event, error) {
	working := cloneState(state)
	return decideVisitorMutable(&working, visitorID, at, includeArrival)
}

func decideVisitorMutable(working *State, visitorID model.VisitorID, at time.Time, includeArrival bool) ([]events.Event, error) {
	if err := ensureRunning(*working); err != nil {
		return nil, err
	}
	if visitorID == "" {
		return nil, fmt.Errorf("%w: visitor id is required", ErrInvalidCommand)
	}
	currentTime := working.Clock.CurrentTime
	working.Clock.CurrentTime = at
	defer func() { working.Clock.CurrentTime = currentTime }()
	result := make([]events.Event, 0, 16)
	appendEvents := func(items ...events.Event) error {
		for _, event := range items {
			if err := working.Apply(event); err != nil {
				return err
			}
			result = append(result, event)
		}
		return nil
	}
	if includeArrival {
		if err := appendEvents(events.VisitorArrived{VisitorID: visitorID, ArrivedAt: at}); err != nil {
			return nil, err
		}
	}
	listEvents, err := Decide("", *working, OpenPage{
		RequestID: model.RequestID(string(visitorID) + ":product_list"), VisitorID: visitorID, Page: model.PageProductList,
	})
	if err != nil {
		return nil, err
	}
	if err := appendEvents(listEvents...); err != nil {
		return nil, err
	}
	if requestFailed(listEvents) {
		failure := appendJourneyFailure(nil, *working, visitorID, ProductState{}, model.RequestID(string(visitorID)+":product_list"), at)
		if err := appendEvents(failure...); err != nil {
			return nil, err
		}
		return result, nil
	}

	products := make([]ProductState, 0, len(working.Products))
	for _, product := range working.Products {
		products = append(products, product)
	}
	sort.Slice(products, func(i, j int) bool { return products[i].ID < products[j].ID })
	roll := deterministicRoll(working.Seed, string(visitorID), "product")
	var selected ProductState
	var cumulative uint64
	for _, product := range products {
		cumulative += uint64(product.ViewProbabilityPPM)
		if uint64(roll) < cumulative {
			selected = product
			break
		}
	}
	if selected.ID == "" {
		completion := events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorLeftAfterProductList, CompletedAt: at}
		_ = appendEvents(completion)
		return result, nil
	}
	if err := appendEvents(events.ProductSelected{VisitorID: visitorID, ProductID: selected.ID, SelectedAt: at}); err != nil {
		return nil, err
	}
	productRequestID := model.RequestID(string(visitorID) + ":product_page")
	productEvents, err := Decide("", *working, OpenPage{RequestID: productRequestID, VisitorID: visitorID, Page: model.PageProduct, ProductID: selected.ID})
	if err != nil {
		return nil, err
	}
	if err := appendEvents(productEvents...); err != nil {
		return nil, err
	}
	if requestFailed(productEvents) {
		failure := appendJourneyFailure(nil, *working, visitorID, selected, productRequestID, at)
		if err := appendEvents(failure...); err != nil {
			return nil, err
		}
		return result, nil
	}
	if deterministicRoll(working.Seed, string(visitorID), "purchase") >= selected.PurchaseProbabilityPPM {
		lost := events.RevenueLost{VisitorID: visitorID, ProductID: selected.ID, AmountMinor: selected.PriceMinor, Reason: model.RevenueLostToAbandonment, LostAt: at}
		completed := events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorLeftAfterProductPage, CompletedAt: at}
		_ = appendEvents(lost, completed)
		return result, nil
	}
	purchaseID := model.PurchaseID(string(visitorID) + ":purchase")
	if err := appendEvents(events.PurchaseIntentCreated{PurchaseID: purchaseID, VisitorID: visitorID, ProductID: selected.ID, CreatedAt: at}); err != nil {
		return nil, err
	}
	purchaseEvents, err := Decide("", *working, OpenPage{RequestID: model.RequestID(purchaseID), VisitorID: visitorID, Page: model.PagePurchase, ProductID: selected.ID})
	if err != nil {
		return nil, err
	}
	if err := appendEvents(purchaseEvents...); err != nil {
		return nil, err
	}
	if requestFailed(purchaseEvents) {
		failure := appendJourneyFailure(nil, *working, visitorID, selected, model.RequestID(purchaseID), at)
		if err := appendEvents(failure...); err != nil {
			return nil, err
		}
		return result, nil
	}
	_ = appendEvents(events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorPurchased, CompletedAt: at})
	return result, nil
}

func cloneState(state State) State {
	clone := state
	clone.Products = cloneMap(state.Products)
	clone.Purchases = cloneMap(state.Purchases)
	clone.Bugs = cloneMap(state.Bugs)
	clone.Fixes = cloneMap(state.Fixes)
	clone.Pages = cloneMap(state.Pages)
	clone.Servers = cloneMap(state.Servers)
	clone.Capacity = cloneMap(state.Capacity)
	clone.UsedCapacityByServer = cloneMap(state.UsedCapacityByServer)
	clone.Operations = cloneMap(state.Operations)
	clone.Commands = cloneMap(state.Commands)
	clone.CommandPayloads = cloneMap(state.CommandPayloads)
	clone.Deployments = cloneMap(state.Deployments)
	clone.DeploymentPageLoadEffects = cloneSliceMap(state.DeploymentPageLoadEffects)
	clone.DeploymentBugEffects = cloneSliceMap(state.DeploymentBugEffects)
	clone.DeploymentDurationEffects = cloneSliceMap(state.DeploymentDurationEffects)
	clone.DeploymentNewBugEffects = cloneSliceMap(state.DeploymentNewBugEffects)
	clone.Requests = cloneMap(state.Requests)
	clone.SeenRequests = cloneMap(state.SeenRequests)
	clone.Visitors = cloneMap(state.Visitors)
	clone.SeenVisitors = cloneMap(state.SeenVisitors)
	clone.ActiveAttacks = cloneMap(state.ActiveAttacks)
	clone.ResolvedAttacks = cloneMap(state.ResolvedAttacks)
	clone.DegradedProviders = cloneMap(state.DegradedProviders)
	// World schedule is immutable after bootstrap and therefore safe to share.
	clone.Schedule = state.Schedule
	return clone
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	clone := make(map[K]V, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneSliceMap[K comparable, V any](source map[K][]V) map[K][]V {
	clone := make(map[K][]V, len(source))
	for key, value := range source {
		clone[key] = append([]V(nil), value...)
	}
	return clone
}

func requestFailed(items []events.Event) bool {
	for _, item := range items {
		switch event := item.(type) {
		case events.PageRequestRejected:
			return event.StatusCode >= 400
		case events.PageRequestCompleted:
			return event.StatusCode >= 400
		}
	}
	return false
}

func appendJourneyFailure(result []events.Event, state State, visitorID model.VisitorID, product ProductState, requestID model.RequestID, at time.Time) []events.Event {
	reason := model.RevenueLostToCapacity
	if request, exists := state.Requests[requestID]; exists {
		switch request.ErrorCode {
		case model.FailurePageBug:
			reason = model.RevenueLostToPageBug
		case model.FailureDeployment:
			reason = model.RevenueLostToDeployment
		case model.FailureExternalProvider:
			reason = model.RevenueLostToProvider
		}
	}
	if product.ID != "" {
		result = append(result, events.RevenueLost{VisitorID: visitorID, ProductID: product.ID, RequestID: requestID, AmountMinor: product.PriceMinor, Reason: reason, LostAt: at})
	}
	return append(result, events.VisitorJourneyCompleted{VisitorID: visitorID, Outcome: model.VisitorLeftAfterPageError, CompletedAt: at})
}

func pendingScheduledEvents(state State, to time.Time) events.EventSchedule {
	start := state.ScheduleCursor
	start += sort.Search(len(state.Schedule)-start, func(i int) bool {
		return state.Schedule[start+i].OccursAt.After(state.Clock.CurrentTime)
	})
	endOffset := sort.Search(len(state.Schedule)-start, func(i int) bool {
		return state.Schedule[start+i].OccursAt.After(to)
	})
	return state.Schedule[start : start+endOffset]
}

func infrastructureCostEvents(state State, from, to time.Time) []events.Event {
	result := make([]events.Event, 0)
	for _, server := range state.Servers {
		if server.Status != model.ServerActive && server.Status != model.ServerDraining {
			continue
		}
		activeFrom := server.ActivatedAt
		if activeFrom.IsZero() || !activeFrom.Before(to) {
			continue
		}
		duration := to.Sub(activeFrom)
		hours := int64((duration + time.Hour - 1) / time.Hour)
		if hours <= server.BilledHours {
			continue
		}
		amount := (hours - server.BilledHours) * server.CostPerHourMinor
		result = append(result, events.InfrastructureCostAccrued{
			ServerID: server.ID, From: from, To: to, BilledHours: hours, AmountMinor: amount,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(events.InfrastructureCostAccrued).ServerID < result[j].(events.InfrastructureCostAccrued).ServerID
	})
	return result
}

func decideDesiredInstances(state State, command SetBackendDesiredInstances) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.CommandID == "" || command.OperationID == "" || command.DesiredInstances < MinimumBackendInstances {
		return nil, fmt.Errorf("%w: invalid desired instance command", ErrInvalidCommand)
	}
	if err := ensureNewScaleIdentifiers(state, command.CommandID, command.OperationID); err != nil {
		return nil, err
	}
	current := 0
	for _, server := range state.Servers {
		if server.Status == model.ServerProvisioning || server.Status == model.ServerActive || server.Status == model.ServerDraining {
			current++
		}
	}
	result := []events.Event{
		events.BackendScaleRequested{CommandID: command.CommandID, OperationID: command.OperationID, DesiredInstances: command.DesiredInstances, RequestedAt: state.Clock.CurrentTime},
		events.OperationQueued{OperationID: command.OperationID, Kind: model.OperationScaleBackend, QueuedAt: state.Clock.CurrentTime},
		events.OperationStarted{OperationID: command.OperationID, StartedAt: state.Clock.CurrentTime},
	}
	if current == command.DesiredInstances {
		return append(result, events.OperationSucceeded{OperationID: command.OperationID, CompletedAt: state.Clock.CurrentTime}), nil
	}
	serverIDs := make([]string, 0, len(state.Servers))
	for id := range state.Servers {
		serverIDs = append(serverIDs, string(id))
	}
	sort.Strings(serverIDs)
	if command.DesiredInstances > current {
		if state.Infrastructure.ServerProvisioningDuration <= 0 {
			return nil, fmt.Errorf("%w: server provisioning duration is not configured", ErrInvalidCommand)
		}
		capacity, cost := int64(100), int64(0)
		if len(serverIDs) > 0 {
			template := state.Servers[model.ServerID(serverIDs[0])]
			capacity, cost = template.CapacityUnits, template.CostPerHourMinor
		}
		for i := 1; i <= command.DesiredInstances-current; i++ {
			serverID := model.ServerID(fmt.Sprintf("server-%s-%d", command.CommandID, i))
			result = append(result, events.ServerProvisioningStarted{OperationID: command.OperationID, ServerID: serverID, CapacityUnits: capacity, CostPerHourMinor: cost, StartedAt: state.Clock.CurrentTime, ReadyAt: state.Clock.CurrentTime.Add(state.Infrastructure.ServerProvisioningDuration)})
		}
		return result, nil
	}
	removeCount := current - command.DesiredInstances
	allImmediate := true
	for i := len(serverIDs) - 1; i >= 0 && removeCount > 0; i-- {
		server := state.Servers[model.ServerID(serverIDs[i])]
		if server.Status != model.ServerActive {
			continue
		}
		result = append(result, events.ServerDrainingStarted{OperationID: command.OperationID, ServerID: server.ID, StartedAt: state.Clock.CurrentTime})
		if !serverHasLoad(state, server.ID, state.Clock.CurrentTime) {
			result = append(result, events.ServerRemoved{OperationID: command.OperationID, ServerID: server.ID, RemovedAt: state.Clock.CurrentTime})
		} else {
			allImmediate = false
		}
		removeCount--
	}
	if removeCount != 0 {
		return nil, fmt.Errorf("%w: not enough active servers", ErrInvalidCommand)
	}
	if allImmediate {
		result = append(result, events.OperationSucceeded{OperationID: command.OperationID, CompletedAt: state.Clock.CurrentTime})
	}
	return result, nil
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
