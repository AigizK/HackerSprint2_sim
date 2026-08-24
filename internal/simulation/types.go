package simulation

import "time"

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

type ProductID string
type PurchaseID string

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

type State struct {
	RunID     string
	Seed      int64
	Status    RunStatus
	Version   uint64
	Clock     ClockState
	Products  map[ProductID]ProductState
	Purchases map[PurchaseID]PurchaseState
	Economy   EconomyState
}

func NewState() State {
	return State{
		Status:    RunNotCreated,
		Products:  make(map[ProductID]ProductState),
		Purchases: make(map[PurchaseID]PurchaseState),
	}
}
