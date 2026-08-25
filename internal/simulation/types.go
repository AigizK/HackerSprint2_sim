package simulation

import (
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const (
	ProbabilityScale   uint32        = 1_000_000
	MinExplicitAdvance time.Duration = 5 * time.Minute
)

type RunStatus string

const (
	RunNotCreated RunStatus = "not_created"
	RunRunning    RunStatus = "running"
	RunCompleted  RunStatus = "completed"
)

type ProductID = model.ProductID
type PurchaseID = model.PurchaseID

type ClockState struct {
	StartedAt   time.Time
	CurrentTime time.Time
	EndsAt      time.Time
}

type ProductState struct {
	ID                     ProductID
	Name                   string
	PriceMinor             int64
	ViewProbabilityPPM     uint32
	PurchaseProbabilityPPM uint32
	AddedAt                time.Time
}

type EconomyState struct {
	Currency            string
	InitialBalanceMinor int64
	StopRunOnNegative   bool
	ServerBillingPeriod time.Duration
	RevenueMinor        int64
	SuccessfulPurchases uint64
	LostPurchases       uint64
	LostRevenueMinor    int64
	ServerCostMinor     int64
	DeploymentCostMinor int64
}

type PurchaseState struct {
	ID          PurchaseID
	ProductID   ProductID
	PriceMinor  int64
	PurchasedAt time.Time
}

type BugState struct {
	ID                    model.BugID
	Page                  model.PageType
	ProductID             ProductID
	FailureProbabilityPPM uint32
	FixMessage            string
	FixMessageHash        string
	Status                model.BugStatus
	ActivatedAt           time.Time
	FixedAt               time.Time
}

type FixSubmissionState struct {
	CommandID   model.CommandID
	Message     string
	Status      model.FixSubmissionStatus
	BugID       model.BugID
	AttackID    model.AttackID
	SubmittedAt time.Time
	CompletedAt time.Time
}

type PageConfigState struct {
	Page         model.PageType
	LoadUnits    int64
	HoldDuration time.Duration
	BaseLatency  time.Duration
	ConfiguredAt time.Time
}

type InfrastructureConfigState struct {
	ServerProvisioningDuration time.Duration
	ConfiguredAt               time.Time
}

type ServerState struct {
	ID               model.ServerID
	OperationID      model.OperationID
	Status           model.ServerLifecycleStatus
	CapacityUnits    int64
	CostPerHourMinor int64
	StartedAt        time.Time
	ReadyAt          time.Time
	ActivatedAt      time.Time
	BilledHours      int64
}

type CapacityAllocationState struct {
	RequestID  model.RequestID
	ServerID   model.ServerID
	LoadUnits  int64
	AcceptedAt time.Time
	ReleasesAt time.Time
}

type OperationState struct {
	ID          model.OperationID
	Kind        model.OperationKind
	Status      model.OperationLifecycleStatus
	QueuedAt    time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	ErrorCode   string
	Message     string
	ProgressPPM uint32
}

type ProviderState struct {
	ID                    model.ProviderID
	FailureProbabilityPPM uint32
	AdditionalLatency     time.Duration
	DegradedAt            time.Time
}

type VisitorState struct {
	ID          model.VisitorID
	ProductID   ProductID
	Outcome     model.VisitorOutcome
	ArrivedAt   time.Time
	CompletedAt time.Time
}

type AttackState struct {
	ID                  model.AttackID
	Kind                model.AttackKind
	TargetPage          model.PageType
	RequestsPerMinute   int64
	LoadUnitsPerRequest int64
	Resolution          model.AttackResolution
	ExpectedEndAt       time.Time
	FixMessage          string
	FixMessageHash      string
	StartedAt           time.Time
}

type DeploymentState struct {
	ID                    model.DeploymentID
	Sequence              int
	Name                  string
	Description           string
	CostMinor             int64
	Duration              time.Duration
	FailureProbabilityPPM uint32
	Status                model.DeploymentLifecycleStatus
	OperationID           model.OperationID
	DefinedAt             time.Time
	StartedAt             time.Time
	ExpectedCompletionAt  time.Time
	CompletedAt           time.Time
}

type DeploymentPageLoadEffectState struct {
	Page            model.PageType
	NewLoadUnits    int64
	NewHoldDuration time.Duration
}

type DeploymentBugProbabilityEffectState struct {
	BugID             model.BugID
	NewProbabilityPPM uint32
}

type DeploymentFutureDurationEffectState struct {
	ReductionPPM    uint32
	MinimumDuration time.Duration
}

type DeploymentNewBugEffectState struct {
	BugID                 model.BugID
	Page                  model.PageType
	ProductID             ProductID
	FailureProbabilityPPM uint32
	FixMessage            string
	FixMessageHash        string
}

type PageRequestState struct {
	ID          model.RequestID
	Source      model.RequestSource
	VisitorID   model.VisitorID
	Page        model.PageType
	ProductID   ProductID
	LoadUnits   int64
	Status      model.PageRequestStatus
	StatusCode  int
	ErrorCode   model.RequestFailureCode
	Message     string
	Latency     time.Duration
	ServerID    model.ServerID
	ReleasesAt  time.Time
	StartedAt   time.Time
	CompletedAt time.Time
}

type State struct {
	RunID                     string
	Seed                      int64
	Status                    RunStatus
	EndReason                 string
	Version                   uint64
	Clock                     ClockState
	Infrastructure            InfrastructureConfigState
	Products                  map[ProductID]ProductState
	Purchases                 map[PurchaseID]PurchaseState
	Bugs                      map[model.BugID]BugState
	Fixes                     map[model.CommandID]FixSubmissionState
	Pages                     map[model.PageType]PageConfigState
	Servers                   map[model.ServerID]ServerState
	Capacity                  map[model.RequestID]CapacityAllocationState
	UsedCapacityByServer      map[model.ServerID]int64
	CapacityIndexedAt         time.Time
	Operations                map[model.OperationID]OperationState
	Commands                  map[model.CommandID]model.OperationID
	CommandPayloads           map[model.CommandID]string
	Deployments               map[model.DeploymentID]DeploymentState
	DeploymentPageLoadEffects map[model.DeploymentID][]DeploymentPageLoadEffectState
	DeploymentBugEffects      map[model.DeploymentID][]DeploymentBugProbabilityEffectState
	DeploymentDurationEffects map[model.DeploymentID][]DeploymentFutureDurationEffectState
	DeploymentNewBugEffects   map[model.DeploymentID][]DeploymentNewBugEffectState
	ActiveDeployment          model.DeploymentID
	Requests                  map[model.RequestID]PageRequestState
	SeenRequests              map[model.RequestID]struct{}
	Visitors                  map[model.VisitorID]VisitorState
	SeenVisitors              map[model.VisitorID]struct{}
	ActiveAttacks             map[model.AttackID]AttackState
	ResolvedAttacks           map[model.AttackID]struct{}
	DegradedProviders         map[model.ProviderID]ProviderState
	Schedule                  events.EventSchedule
	ScheduleCursor            int
	DesiredInstances          int
	Economy                   EconomyState
}

func NewState() State {
	return State{
		Status:                    RunNotCreated,
		Products:                  make(map[ProductID]ProductState),
		Purchases:                 make(map[PurchaseID]PurchaseState),
		Bugs:                      make(map[model.BugID]BugState),
		Fixes:                     make(map[model.CommandID]FixSubmissionState),
		Pages:                     make(map[model.PageType]PageConfigState),
		Servers:                   make(map[model.ServerID]ServerState),
		Capacity:                  make(map[model.RequestID]CapacityAllocationState),
		UsedCapacityByServer:      make(map[model.ServerID]int64),
		Operations:                make(map[model.OperationID]OperationState),
		Commands:                  make(map[model.CommandID]model.OperationID),
		CommandPayloads:           make(map[model.CommandID]string),
		Deployments:               make(map[model.DeploymentID]DeploymentState),
		DeploymentPageLoadEffects: make(map[model.DeploymentID][]DeploymentPageLoadEffectState),
		DeploymentBugEffects:      make(map[model.DeploymentID][]DeploymentBugProbabilityEffectState),
		DeploymentDurationEffects: make(map[model.DeploymentID][]DeploymentFutureDurationEffectState),
		DeploymentNewBugEffects:   make(map[model.DeploymentID][]DeploymentNewBugEffectState),
		Requests:                  make(map[model.RequestID]PageRequestState),
		SeenRequests:              make(map[model.RequestID]struct{}),
		Visitors:                  make(map[model.VisitorID]VisitorState),
		SeenVisitors:              make(map[model.VisitorID]struct{}),
		ActiveAttacks:             make(map[model.AttackID]AttackState),
		ResolvedAttacks:           make(map[model.AttackID]struct{}),
		DegradedProviders:         make(map[model.ProviderID]ProviderState),
	}
}
