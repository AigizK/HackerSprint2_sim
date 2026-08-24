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

// OpenPage is intentionally not handled yet. The executable specifications
// define its expected events and site-log projection before implementation.
type OpenPage struct {
	RequestID model.RequestID
	VisitorID model.VisitorID
	Page      model.PageType
	ProductID model.ProductID
}

func (OpenPage) commandType() string { return "OpenPage" }
