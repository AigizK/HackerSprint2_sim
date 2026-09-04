package events

import "time"

type VisitorArrived struct {
	VisitorID  VisitorID
	SourceIP   string
	UserAgent  string
	RegionCode RegionCode
	ArrivedAt  time.Time
}

func (VisitorArrived) EventType() string { return "VisitorArrived" }

type ProductSelected struct {
	VisitorID  VisitorID
	ProductID  ProductID
	SelectedAt time.Time
}

func (ProductSelected) EventType() string { return "ProductSelected" }

type VisitorJourneyCompleted struct {
	VisitorID   VisitorID
	Outcome     VisitorOutcome
	CompletedAt time.Time
}

func (VisitorJourneyCompleted) EventType() string { return "VisitorJourneyCompleted" }
