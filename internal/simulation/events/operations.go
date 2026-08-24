package events

import "time"

type OperationQueued struct {
	OperationID OperationID
	Kind        OperationKind
	QueuedAt    time.Time
}

func (OperationQueued) EventType() string { return "OperationQueued" }

type OperationStarted struct {
	OperationID OperationID
	StartedAt   time.Time
}

func (OperationStarted) EventType() string { return "OperationStarted" }

type OperationProgressed struct {
	OperationID OperationID
	ProgressPPM uint32
	UpdatedAt   time.Time
}

func (OperationProgressed) EventType() string { return "OperationProgressed" }

type OperationSucceeded struct {
	OperationID OperationID
	CompletedAt time.Time
}

func (OperationSucceeded) EventType() string { return "OperationSucceeded" }

type OperationFailed struct {
	OperationID OperationID
	ErrorCode   string
	Message     string
	FailedAt    time.Time
}

func (OperationFailed) EventType() string { return "OperationFailed" }
