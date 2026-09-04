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
	ProductID          ProductID
	Name               string
	Description        string
	Manufacturer       string
	PriceMinor         int64
	Available          bool
	Version            uint64
	ViewProbabilityPPM uint32
	AddedAt            time.Time
}

func (ProductAdded) EventType() string { return "ProductAdded" }

type TimeAdvanced struct {
	CommandID         CommandID
	From              time.Time
	To                time.Time
	RealElapsed       time.Duration
	RequestedDuration time.Duration
	AppliedDuration   time.Duration
}

func (TimeAdvanced) EventType() string { return "TimeAdvanced" }
