package events

import "time"

type Event interface {
	EventType() string
}

type WorldCreated struct {
	RunID     string
	Seed      int64
	StartedAt time.Time
	EndsAt    time.Time
}

func (WorldCreated) EventType() string { return "WorldCreated" }

type ProductAdded struct {
	ProductID              ProductID
	Name                   string
	PriceMinor             int64
	ViewProbabilityPPM     uint32
	PurchaseProbabilityPPM uint32
	AddedAt                time.Time
}

func (ProductAdded) EventType() string { return "ProductAdded" }

type ProductPurchased struct {
	PurchaseID  PurchaseID
	ProductID   ProductID
	PriceMinor  int64
	PurchasedAt time.Time
}

func (ProductPurchased) EventType() string { return "ProductPurchased" }

type TimeAdvanced struct {
	CommandID         CommandID
	From              time.Time
	To                time.Time
	RealElapsed       time.Duration
	RequestedDuration time.Duration
	AppliedDuration   time.Duration
}

func (TimeAdvanced) EventType() string { return "TimeAdvanced" }
