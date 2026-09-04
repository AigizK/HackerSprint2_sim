package simulation

import (
	"encoding/json"
	"fmt"
	"reflect"
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
	value := reflect.ValueOf(event)
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		if dereferenced, ok := value.Elem().Interface().(events.Event); ok {
			return dereferenced
		}
	}
	return event
}

var eventFactories = map[string]func() events.Event{
	"CostsConfigured":                   func() events.Event { return &events.CostsConfigured{} },
	"ServerCommandAccepted":             func() events.Event { return &events.ServerCommandAccepted{} },
	"ControlCommandAccepted":            func() events.Event { return &events.ControlCommandAccepted{} },
	"ControlCommandResponseRecorded":    func() events.Event { return &events.ControlCommandResponseRecorded{} },
	"ControlOperationResultRecorded":    func() events.Event { return &events.ControlOperationResultRecorded{} },
	"ServerCredentialIssued":            func() events.Event { return &events.ServerCredentialIssued{} },
	"ServerCredentialRotationRequested": func() events.Event { return &events.ServerCredentialRotationRequested{} },
	"WorldCreated":                      func() events.Event { return &events.WorldCreated{} },
	"ProductAdded":                      func() events.Event { return &events.ProductAdded{} },
	"InboxMessageDelivered":             func() events.Event { return &events.InboxMessageDelivered{} },
	"TimeAdvanced":                      func() events.Event { return &events.TimeAdvanced{} },
	"PageConfigured":                    func() events.Event { return &events.PageConfigured{} },
	"InfrastructureConfigured":          func() events.Event { return &events.InfrastructureConfigured{} },
	"RunEnded":                          func() events.Event { return &events.RunEnded{} },
	"VisitorArrived":                    func() events.Event { return &events.VisitorArrived{} },
	"ProductSelected":                   func() events.Event { return &events.ProductSelected{} },
	"VisitorJourneyCompleted":           func() events.Event { return &events.VisitorJourneyCompleted{} },
	"PageRequestStarted":                func() events.Event { return &events.PageRequestStarted{} },
	"PageRequestAccepted":               func() events.Event { return &events.PageRequestAccepted{} },
	"PageRequestCompleted":              func() events.Event { return &events.PageRequestCompleted{} },
	"PageRequestRejected":               func() events.Event { return &events.PageRequestRejected{} },
	"ServerProvisioningStarted":         func() events.Event { return &events.ServerProvisioningStarted{} },
	"ServerActivated":                   func() events.Event { return &events.ServerActivated{} },
	"ServerDrainingStarted":             func() events.Event { return &events.ServerDrainingStarted{} },
	"ServerRemoved":                     func() events.Event { return &events.ServerRemoved{} },
	"ServerProvisioningFailed":          func() events.Event { return &events.ServerProvisioningFailed{} },
	"BackendAvailabilityChanged":        func() events.Event { return &events.BackendAvailabilityChanged{} },
	"OperationQueued":                   func() events.Event { return &events.OperationQueued{} },
	"OperationStarted":                  func() events.Event { return &events.OperationStarted{} },
	"OperationProgressed":               func() events.Event { return &events.OperationProgressed{} },
	"OperationSucceeded":                func() events.Event { return &events.OperationSucceeded{} },
	"OperationFailed":                   func() events.Event { return &events.OperationFailed{} },
	"InfrastructureCostAccrued":         func() events.Event { return &events.InfrastructureCostAccrued{} },
	"TrafficAttackStarted":              func() events.Event { return &events.TrafficAttackStarted{} },
	"TrafficAttackEnded":                func() events.Event { return &events.TrafficAttackEnded{} },
	"FirewallRuleUpserted":              func() events.Event { return &events.FirewallRuleUpserted{} },
	"FirewallRuleDeleted":               func() events.Event { return &events.FirewallRuleDeleted{} },
	"FirewallRequestEvaluated":          func() events.Event { return &events.FirewallRequestEvaluated{} },
	"FirewallAvailabilityChanged":       func() events.Event { return &events.FirewallAvailabilityChanged{} },
	"ServerTypeDefined":                 func() events.Event { return &events.ServerTypeDefined{} },
	"DatabaseCreated":                   func() events.Event { return &events.DatabaseCreated{} },
	"DatabaseDeleted":                   func() events.Event { return &events.DatabaseDeleted{} },
	"DatabaseConnectionOpened":          func() events.Event { return &events.DatabaseConnectionOpened{} },
	"DatabaseConnectionReleased":        func() events.Event { return &events.DatabaseConnectionReleased{} },
	"DatabaseConnectionRejected":        func() events.Event { return &events.DatabaseConnectionRejected{} },
	"DatabaseAvailabilityChanged":       func() events.Event { return &events.DatabaseAvailabilityChanged{} },
	"DatabaseGrowthRequested":           func() events.Event { return &events.DatabaseGrowthRequested{} },
	"DatabaseStorageIncreased":          func() events.Event { return &events.DatabaseStorageIncreased{} },
	"DatabaseGrowthBlocked":             func() events.Event { return &events.DatabaseGrowthBlocked{} },
	"DiskLogsCleaned":                   func() events.Event { return &events.DiskLogsCleaned{} },
	"DiskLogsGrowthRequested":           func() events.Event { return &events.DiskLogsGrowthRequested{} },
	"DiskLogsIncreased":                 func() events.Event { return &events.DiskLogsIncreased{} },
	"DiskLogsGrowthBlocked":             func() events.Event { return &events.DiskLogsGrowthBlocked{} },
	"DatabaseBackupStarted":             func() events.Event { return &events.DatabaseBackupStarted{} },
	"DatabaseBackupCompleted":           func() events.Event { return &events.DatabaseBackupCompleted{} },
	"DatabaseBackupFailed":              func() events.Event { return &events.DatabaseBackupFailed{} },
	"BackupStorageCostAccrued":          func() events.Event { return &events.BackupStorageCostAccrued{} },
	"DatabaseRestoreStarted":            func() events.Event { return &events.DatabaseRestoreStarted{} },
	"DatabaseRestoreCompleted":          func() events.Event { return &events.DatabaseRestoreCompleted{} },
	"DatabaseRestoreFailed":             func() events.Event { return &events.DatabaseRestoreFailed{} },
	"SiteStopStarted":                   func() events.Event { return &events.SiteStopStarted{} },
	"SiteStopped":                       func() events.Event { return &events.SiteStopped{} },
	"SiteDatabaseChanged":               func() events.Event { return &events.SiteDatabaseChanged{} },
	"SiteStarted":                       func() events.Event { return &events.SiteStarted{} },
}
