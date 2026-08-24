package events

import "time"

type DeploymentDefined struct {
	DeploymentID DeploymentID
	Sequence     int
	Name         string
	Description  string
	CostMinor    int64
	DefinedAt    time.Time
}

func (DeploymentDefined) EventType() string { return "DeploymentDefined" }

type DeploymentUnlocked struct {
	DeploymentID DeploymentID
	UnlockedAt   time.Time
}

func (DeploymentUnlocked) EventType() string { return "DeploymentUnlocked" }

type DeploymentStarted struct {
	CommandID            CommandID
	DeploymentID         DeploymentID
	OperationID          OperationID
	StartedAt            time.Time
	ExpectedCompletionAt time.Time
}

func (DeploymentStarted) EventType() string { return "DeploymentStarted" }

type DeploymentCompleted struct {
	DeploymentID DeploymentID
	OperationID  OperationID
	CompletedAt  time.Time
}

func (DeploymentCompleted) EventType() string { return "DeploymentCompleted" }

type DeploymentFailed struct {
	DeploymentID DeploymentID
	OperationID  OperationID
	ErrorCode    string
	Message      string
	FailedAt     time.Time
}

func (DeploymentFailed) EventType() string { return "DeploymentFailed" }

type PageLoadChanged struct {
	DeploymentID    DeploymentID
	Page            PageType
	OldLoadUnits    int64
	NewLoadUnits    int64
	OldHoldDuration time.Duration
	NewHoldDuration time.Duration
	ChangedAt       time.Time
}

func (PageLoadChanged) EventType() string { return "PageLoadChanged" }
