package events

import "time"

type ServerCommandAccepted struct {
	CommandID   CommandID
	OperationID OperationID
	Payload     string
	AcceptedAt  time.Time
}

func (ServerCommandAccepted) EventType() string { return "ServerCommandAccepted" }

type ServerProvisioningStarted struct {
	OperationID       OperationID
	ServerID          ServerID
	Name              string
	CredentialID      CredentialID
	InstanceType      InstanceType
	Role              ServerRole
	CapacityUnits     int64
	DiskBytes         int64
	ConnectionLimit   int
	ConnectionHold    time.Duration
	CostPerHourMinor  int64
	CostPerMonthMinor int64
	StartedAt         time.Time
	ReadyAt           time.Time
}

func (ServerProvisioningStarted) EventType() string { return "ServerProvisioningStarted" }

type ServerActivated struct {
	OperationID OperationID
	ServerID    ServerID
	ActivatedAt time.Time
}

func (ServerActivated) EventType() string { return "ServerActivated" }

type ServerDrainingStarted struct {
	OperationID OperationID
	ServerID    ServerID
	StartedAt   time.Time
}

func (ServerDrainingStarted) EventType() string { return "ServerDrainingStarted" }

type ServerRemoved struct {
	OperationID OperationID
	ServerID    ServerID
	RemovedAt   time.Time
}

func (ServerRemoved) EventType() string { return "ServerRemoved" }

type ServerProvisioningFailed struct {
	OperationID OperationID
	ServerID    ServerID
	ErrorCode   string
	Message     string
	FailedAt    time.Time
}

func (ServerProvisioningFailed) EventType() string { return "ServerProvisioningFailed" }

// BackendAvailabilityChanged is a replayable uptime boundary. Available is
// false when at least one configured read-only page cannot fit on any active
// backend after current allocations, DDoS load and firewall rules are applied.
type BackendAvailabilityChanged struct {
	Available        bool
	UnavailablePages []PageType
	ChangedAt        time.Time
}

func (BackendAvailabilityChanged) EventType() string { return "BackendAvailabilityChanged" }
