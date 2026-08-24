package simulation

import (
	"time"

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
	RevenueMinor        int64
	SuccessfulPurchases uint64
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
	ActivatedAt           time.Time
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
	StartedAt   time.Time
	CompletedAt time.Time
}

type State struct {
	RunID     string
	Seed      int64
	Status    RunStatus
	Version   uint64
	Clock     ClockState
	Products  map[ProductID]ProductState
	Purchases map[PurchaseID]PurchaseState
	Bugs      map[model.BugID]BugState
	Requests  map[model.RequestID]PageRequestState
	Economy   EconomyState
}

func NewState() State {
	return State{
		Status:    RunNotCreated,
		Products:  make(map[ProductID]ProductState),
		Purchases: make(map[PurchaseID]PurchaseState),
		Bugs:      make(map[model.BugID]BugState),
		Requests:  make(map[model.RequestID]PageRequestState),
	}
}
