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
		to := state.Clock.CurrentTime.Add(applied)
		result := []events.Event{events.TimeAdvanced{
			From:              state.Clock.CurrentTime,
			To:                to,
			RealElapsed:       command.RealElapsed,
			RequestedDuration: command.RequestedDuration,
			AppliedDuration:   applied,
		}}
		if deployment, exists := state.Deployments[state.ActiveDeployment]; exists &&
			!deployment.ExpectedCompletionAt.After(to) {
			result = append(result, deploymentCompletionEvents(state, deployment, to)...)
		}
		for _, server := range sortedProvisioningServers(state) {
			if server.ReadyAt.After(to) {
				continue
			}
			result = append(result,
				events.ServerActivated{
					OperationID: server.OperationID,
					ServerID:    server.ID,
					ActivatedAt: to,
				},
				events.OperationSucceeded{
					OperationID: server.OperationID,
					CompletedAt: to,
				},
			)
		}
		for _, server := range sortedDrainingServers(state) {
			if serverHasLoad(state, server.ID, to) {
				continue
			}
			result = append(result,
				events.ServerRemoved{
					OperationID: server.OperationID,
					ServerID:    server.ID,
					RemovedAt:   to,
				},
				events.OperationSucceeded{
					OperationID: server.OperationID,
					CompletedAt: to,
				},
			)
		}
		return result, nil

	case OpenPage:
		if err := ensureRunning(state); err != nil {
			return nil, err
		}
		if command.RequestID == "" || command.VisitorID == "" || !validPage(command.Page) {
			return nil, fmt.Errorf("%w: invalid page request", ErrInvalidCommand)
		}
		if _, exists := state.Requests[command.RequestID]; exists {
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
			server, available, accepted := selectServer(state, pageConfig.LoadUnits, state.Clock.CurrentTime)
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
					CompletedAt: state.Clock.CurrentTime,
				},
			)
			return result, nil
		}

		result = append(result, events.PageRequestCompleted{
			RequestID:   command.RequestID,
			ServerID:    serverID,
			StatusCode:  200,
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
		if _, exists := state.Fixes[command.CommandID]; exists {
			return nil, fmt.Errorf("%w: fix command %q already exists", ErrInvalidCommand, command.CommandID)
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
			return nil, fmt.Errorf("%w: deployment %q does not exist", ErrInvalidCommand, command.DeploymentID)
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

	default:
		return nil, fmt.Errorf("%w: unsupported command %T", ErrInvalidCommand, command)
	}
}

func validPage(page model.PageType) bool {
	return page == model.PageProductList || page == model.PageProduct || page == model.PagePurchase
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

func selectServer(state State, required int64, at time.Time) (ServerState, int64, bool) {
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
		available := serverAvailableCapacity(state, server.ID, at)
		if available > maxAvailable {
			maxAvailable = available
		}
		if available >= required {
			return server, available, true
		}
	}
	return ServerState{}, maxAvailable, false
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
