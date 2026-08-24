package events

import "time"

type PageRequestStarted struct {
	RequestID RequestID
	Source    RequestSource
	VisitorID VisitorID
	Page      PageType
	ProductID ProductID
	LoadUnits int64
	StartedAt time.Time
}

func (PageRequestStarted) EventType() string { return "PageRequestStarted" }

type PageRequestAccepted struct {
	RequestID  RequestID
	ServerID   ServerID
	AcceptedAt time.Time
	ReleasesAt time.Time
}

func (PageRequestAccepted) EventType() string { return "PageRequestAccepted" }

type CapacityAllocationReleased struct {
	DeploymentID DeploymentID
	RequestID    RequestID
	ServerID     ServerID
	ReleasedAt   time.Time
}

func (CapacityAllocationReleased) EventType() string { return "CapacityAllocationReleased" }

type PageRequestCompleted struct {
	RequestID   RequestID
	ServerID    ServerID
	StatusCode  int
	ErrorCode   RequestFailureCode
	Message     string
	Latency     time.Duration
	CompletedAt time.Time
}

func (PageRequestCompleted) EventType() string { return "PageRequestCompleted" }

type PageRequestRejected struct {
	RequestID  RequestID
	StatusCode int
	ErrorCode  RequestFailureCode
	Message    string
	RejectedAt time.Time
}

func (PageRequestRejected) EventType() string { return "PageRequestRejected" }
