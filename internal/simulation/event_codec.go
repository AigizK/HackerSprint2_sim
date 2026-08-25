package simulation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

type encodedScheduledEvent struct {
	Sequence  uint64          `json:"sequence"`
	OccursAt  time.Time       `json:"occurs_at"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type encodedWorldSchedule struct {
	Schedule  []encodedScheduledEvent `json:"schedule"`
	CreatedAt time.Time               `json:"created_at"`
}

func EncodeEvent(event events.Event) (string, []byte, error) {
	if event == nil {
		return "", nil, fmt.Errorf("%w: event is nil", ErrInvalidEvent)
	}
	if schedule, ok := event.(events.WorldScheduleCreated); ok {
		payload := encodedWorldSchedule{CreatedAt: schedule.CreatedAt, Schedule: make([]encodedScheduledEvent, 0, len(schedule.Schedule))}
		for _, scheduled := range schedule.Schedule {
			eventType, encoded, err := EncodeEvent(scheduled.Event)
			if err != nil {
				return "", nil, err
			}
			payload.Schedule = append(payload.Schedule, encodedScheduledEvent{
				Sequence: scheduled.Sequence, OccursAt: scheduled.OccursAt, EventType: eventType, Payload: encoded,
			})
		}
		encoded, err := json.Marshal(payload)
		return event.EventType(), encoded, err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return "", nil, fmt.Errorf("encode %s: %w", event.EventType(), err)
	}
	return event.EventType(), payload, nil
}

func DecodeEvent(eventType string, payload []byte) (events.Event, error) {
	if eventType == "WorldScheduleCreated" {
		var encoded encodedWorldSchedule
		if err := json.Unmarshal(payload, &encoded); err != nil {
			return nil, fmt.Errorf("decode %s: %w", eventType, err)
		}
		schedule := make(events.EventSchedule, 0, len(encoded.Schedule))
		for _, scheduled := range encoded.Schedule {
			event, err := DecodeEvent(scheduled.EventType, scheduled.Payload)
			if err != nil {
				return nil, err
			}
			schedule = append(schedule, events.ScheduledWorldEvent{Sequence: scheduled.Sequence, OccursAt: scheduled.OccursAt, Event: event})
		}
		return events.WorldScheduleCreated{Schedule: schedule, CreatedAt: encoded.CreatedAt}, nil
	}
	factory, exists := eventFactories[eventType]
	if !exists {
		return nil, fmt.Errorf("%w: unknown event type %q", ErrInvalidEvent, eventType)
	}
	event := factory()
	if err := json.Unmarshal(payload, event); err != nil {
		return nil, fmt.Errorf("decode %s: %w", eventType, err)
	}
	return dereferenceEvent(event), nil
}

func dereferenceEvent(event events.Event) events.Event {
	switch event := event.(type) {
	case *events.WorldCreated:
		return *event
	case *events.ProductAdded:
		return *event
	case *events.ProductPurchased:
		return *event
	case *events.TimeAdvanced:
		return *event
	case *events.PageConfigured:
		return *event
	case *events.InfrastructureConfigured:
		return *event
	case *events.RunEnded:
		return *event
	case *events.VisitorArrived:
		return *event
	case *events.ProductSelected:
		return *event
	case *events.PurchaseIntentCreated:
		return *event
	case *events.VisitorJourneyCompleted:
		return *event
	case *events.PageRequestStarted:
		return *event
	case *events.PageRequestAccepted:
		return *event
	case *events.CapacityAllocationReleased:
		return *event
	case *events.PageRequestCompleted:
		return *event
	case *events.PageRequestRejected:
		return *event
	case *events.BackendScaleRequested:
		return *event
	case *events.ServerProvisioningStarted:
		return *event
	case *events.ServerActivated:
		return *event
	case *events.ServerDrainingStarted:
		return *event
	case *events.ServerRemoved:
		return *event
	case *events.ServerProvisioningFailed:
		return *event
	case *events.PageBugActivated:
		return *event
	case *events.PageBugTriggered:
		return *event
	case *events.BugFixSubmitted:
		return *event
	case *events.PageBugFixed:
		return *event
	case *events.BugFixRejected:
		return *event
	case *events.DeploymentDefined:
		return *event
	case *events.DeploymentUnlocked:
		return *event
	case *events.DeploymentStarted:
		return *event
	case *events.DeploymentCompleted:
		return *event
	case *events.DeploymentFailed:
		return *event
	case *events.DeploymentPageLoadEffectDefined:
		return *event
	case *events.DeploymentBugProbabilityEffectDefined:
		return *event
	case *events.DeploymentFutureDurationEffectDefined:
		return *event
	case *events.DeploymentNewBugEffectDefined:
		return *event
	case *events.PageLoadChanged:
		return *event
	case *events.PageBugProbabilityChanged:
		return *event
	case *events.DeploymentDurationChanged:
		return *event
	case *events.OperationQueued:
		return *event
	case *events.OperationStarted:
		return *event
	case *events.OperationProgressed:
		return *event
	case *events.OperationSucceeded:
		return *event
	case *events.OperationFailed:
		return *event
	case *events.InfrastructureCostAccrued:
		return *event
	case *events.EconomyConfigured:
		return *event
	case *events.DeploymentCostAccrued:
		return *event
	case *events.RevenueLost:
		return *event
	case *events.TrafficAttackStarted:
		return *event
	case *events.TrafficAttackEnded:
		return *event
	case *events.TrafficAttackMitigated:
		return *event
	case *events.ExternalProviderDegraded:
		return *event
	case *events.ExternalProviderRecovered:
		return *event
	default:
		return event
	}
}

var eventFactories = map[string]func() events.Event{
	"WorldCreated":                          func() events.Event { return &events.WorldCreated{} },
	"ProductAdded":                          func() events.Event { return &events.ProductAdded{} },
	"ProductPurchased":                      func() events.Event { return &events.ProductPurchased{} },
	"TimeAdvanced":                          func() events.Event { return &events.TimeAdvanced{} },
	"PageConfigured":                        func() events.Event { return &events.PageConfigured{} },
	"InfrastructureConfigured":              func() events.Event { return &events.InfrastructureConfigured{} },
	"RunEnded":                              func() events.Event { return &events.RunEnded{} },
	"VisitorArrived":                        func() events.Event { return &events.VisitorArrived{} },
	"ProductSelected":                       func() events.Event { return &events.ProductSelected{} },
	"PurchaseIntentCreated":                 func() events.Event { return &events.PurchaseIntentCreated{} },
	"VisitorJourneyCompleted":               func() events.Event { return &events.VisitorJourneyCompleted{} },
	"PageRequestStarted":                    func() events.Event { return &events.PageRequestStarted{} },
	"PageRequestAccepted":                   func() events.Event { return &events.PageRequestAccepted{} },
	"CapacityAllocationReleased":            func() events.Event { return &events.CapacityAllocationReleased{} },
	"PageRequestCompleted":                  func() events.Event { return &events.PageRequestCompleted{} },
	"PageRequestRejected":                   func() events.Event { return &events.PageRequestRejected{} },
	"BackendScaleRequested":                 func() events.Event { return &events.BackendScaleRequested{} },
	"ServerProvisioningStarted":             func() events.Event { return &events.ServerProvisioningStarted{} },
	"ServerActivated":                       func() events.Event { return &events.ServerActivated{} },
	"ServerDrainingStarted":                 func() events.Event { return &events.ServerDrainingStarted{} },
	"ServerRemoved":                         func() events.Event { return &events.ServerRemoved{} },
	"ServerProvisioningFailed":              func() events.Event { return &events.ServerProvisioningFailed{} },
	"PageBugActivated":                      func() events.Event { return &events.PageBugActivated{} },
	"PageBugTriggered":                      func() events.Event { return &events.PageBugTriggered{} },
	"BugFixSubmitted":                       func() events.Event { return &events.BugFixSubmitted{} },
	"PageBugFixed":                          func() events.Event { return &events.PageBugFixed{} },
	"BugFixRejected":                        func() events.Event { return &events.BugFixRejected{} },
	"DeploymentDefined":                     func() events.Event { return &events.DeploymentDefined{} },
	"DeploymentUnlocked":                    func() events.Event { return &events.DeploymentUnlocked{} },
	"DeploymentStarted":                     func() events.Event { return &events.DeploymentStarted{} },
	"DeploymentCompleted":                   func() events.Event { return &events.DeploymentCompleted{} },
	"DeploymentFailed":                      func() events.Event { return &events.DeploymentFailed{} },
	"DeploymentPageLoadEffectDefined":       func() events.Event { return &events.DeploymentPageLoadEffectDefined{} },
	"DeploymentBugProbabilityEffectDefined": func() events.Event { return &events.DeploymentBugProbabilityEffectDefined{} },
	"DeploymentFutureDurationEffectDefined": func() events.Event { return &events.DeploymentFutureDurationEffectDefined{} },
	"DeploymentNewBugEffectDefined":         func() events.Event { return &events.DeploymentNewBugEffectDefined{} },
	"PageLoadChanged":                       func() events.Event { return &events.PageLoadChanged{} },
	"PageBugProbabilityChanged":             func() events.Event { return &events.PageBugProbabilityChanged{} },
	"DeploymentDurationChanged":             func() events.Event { return &events.DeploymentDurationChanged{} },
	"OperationQueued":                       func() events.Event { return &events.OperationQueued{} },
	"OperationStarted":                      func() events.Event { return &events.OperationStarted{} },
	"OperationProgressed":                   func() events.Event { return &events.OperationProgressed{} },
	"OperationSucceeded":                    func() events.Event { return &events.OperationSucceeded{} },
	"OperationFailed":                       func() events.Event { return &events.OperationFailed{} },
	"InfrastructureCostAccrued":             func() events.Event { return &events.InfrastructureCostAccrued{} },
	"EconomyConfigured":                     func() events.Event { return &events.EconomyConfigured{} },
	"DeploymentCostAccrued":                 func() events.Event { return &events.DeploymentCostAccrued{} },
	"RevenueLost":                           func() events.Event { return &events.RevenueLost{} },
	"TrafficAttackStarted":                  func() events.Event { return &events.TrafficAttackStarted{} },
	"TrafficAttackEnded":                    func() events.Event { return &events.TrafficAttackEnded{} },
	"TrafficAttackMitigated":                func() events.Event { return &events.TrafficAttackMitigated{} },
	"ExternalProviderDegraded":              func() events.Event { return &events.ExternalProviderDegraded{} },
	"ExternalProviderRecovered":             func() events.Event { return &events.ExternalProviderRecovered{} },
}
