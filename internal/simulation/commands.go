package simulation

import (
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type Command interface {
	commandType() string
}

type CreateWorld struct {
	Seed      int64
	StartedAt time.Time
	EndsAt    time.Time
}

func (CreateWorld) commandType() string { return "CreateWorld" }

type AddProduct struct {
	ProductID              ProductID
	Name                   string
	PriceMinor             int64
	ViewProbabilityPPM     uint32
	PurchaseProbabilityPPM uint32
}

func (AddProduct) commandType() string { return "AddProduct" }

type PurchaseProduct struct {
	PurchaseID PurchaseID
	ProductID  ProductID
}

func (PurchaseProduct) commandType() string { return "PurchaseProduct" }

type AdvanceTime struct {
	RealElapsed       time.Duration
	RequestedDuration time.Duration
}

func (AdvanceTime) commandType() string { return "AdvanceTime" }

// OpenPage executes a visitor request against the current page, bug and capacity state.
type OpenPage struct {
	RequestID model.RequestID
	VisitorID model.VisitorID
	Page      model.PageType
	ProductID model.ProductID
}

func (OpenPage) commandType() string { return "OpenPage" }

// ApplyFix submits a diagnostic message that may resolve an active page bug.
type ApplyFix struct {
	CommandID model.CommandID
	Message   string
}

func (ApplyFix) commandType() string { return "ApplyFix" }

type AddServer struct {
	CommandID        model.CommandID
	OperationID      model.OperationID
	ServerID         model.ServerID
	CapacityUnits    int64
	CostPerHourMinor int64
}

func (AddServer) commandType() string { return "AddServer" }

type RemoveServer struct {
	CommandID   model.CommandID
	OperationID model.OperationID
	ServerID    model.ServerID
}

func (RemoveServer) commandType() string { return "RemoveServer" }
