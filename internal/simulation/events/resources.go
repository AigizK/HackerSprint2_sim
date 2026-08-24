package events

import "time"

type BackendScaleRequested struct {
	CommandID        CommandID
	OperationID      OperationID
	DesiredInstances int
	RequestedAt      time.Time
}

func (BackendScaleRequested) EventType() string { return "BackendScaleRequested" }

type ServerProvisioningStarted struct {
	OperationID      OperationID
	ServerID         ServerID
	CapacityUnits    int64
	CostPerHourMinor int64
	StartedAt        time.Time
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
