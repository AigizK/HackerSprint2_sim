package events

import "time"

type DeploymentDefined struct {
	DeploymentID          DeploymentID
	Sequence              int
	Name                  string
	Description           string
	CostMinor             int64
	Duration              time.Duration
	FailureProbabilityPPM uint32
	DefinedAt             time.Time
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

type DeploymentPageLoadEffectDefined struct {
	DeploymentID    DeploymentID
	Page            PageType
	NewLoadUnits    int64
	NewHoldDuration time.Duration
	DefinedAt       time.Time
}

func (DeploymentPageLoadEffectDefined) EventType() string {
	return "DeploymentPageLoadEffectDefined"
}

type DeploymentBugProbabilityEffectDefined struct {
	DeploymentID      DeploymentID
	BugID             BugID
	NewProbabilityPPM uint32
	DefinedAt         time.Time
}

func (DeploymentBugProbabilityEffectDefined) EventType() string {
	return "DeploymentBugProbabilityEffectDefined"
}

type DeploymentFutureDurationEffectDefined struct {
	DeploymentID    DeploymentID
	ReductionPPM    uint32
	MinimumDuration time.Duration
	DefinedAt       time.Time
}

func (DeploymentFutureDurationEffectDefined) EventType() string {
	return "DeploymentFutureDurationEffectDefined"
}

type DeploymentNewBugEffectDefined struct {
	DeploymentID          DeploymentID
	BugID                 BugID
	Page                  PageType
	ProductID             ProductID
	FailureProbabilityPPM uint32
	FixMessage            string
	FixMessageHash        string
	DefinedAt             time.Time
}

func (DeploymentNewBugEffectDefined) EventType() string { return "DeploymentNewBugEffectDefined" }

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

type PageBugProbabilityChanged struct {
	DeploymentID      DeploymentID
	BugID             BugID
	OldProbabilityPPM uint32
	NewProbabilityPPM uint32
	ChangedAt         time.Time
}

func (PageBugProbabilityChanged) EventType() string { return "PageBugProbabilityChanged" }

type DeploymentDurationChanged struct {
	SourceDeploymentID DeploymentID
	DeploymentID       DeploymentID
	OldDuration        time.Duration
	NewDuration        time.Duration
	ChangedAt          time.Time
}

func (DeploymentDurationChanged) EventType() string { return "DeploymentDurationChanged" }
