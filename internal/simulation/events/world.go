package events

import "time"

// PageConfigured is normally generated together with the world before a run starts.
type PageConfigured struct {
	Page         PageType
	LoadUnits    int64
	HoldDuration time.Duration
	ConfiguredAt time.Time
}

func (PageConfigured) EventType() string { return "PageConfigured" }

type InfrastructureConfigured struct {
	ServerProvisioningDuration time.Duration
	ConfiguredAt               time.Time
}

func (InfrastructureConfigured) EventType() string { return "InfrastructureConfigured" }

type RunEnded struct {
	CompletedAt time.Time
	Reason      string
}

func (RunEnded) EventType() string { return "RunEnded" }
