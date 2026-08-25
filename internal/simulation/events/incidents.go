package events

import "time"

type TrafficAttackStarted struct {
	AttackID            AttackID
	Kind                AttackKind
	TargetPage          PageType
	RequestsPerMinute   int64
	LoadUnitsPerRequest int64
	Resolution          AttackResolution
	ExpectedEndAt       time.Time
	FixMessage          string
	FixMessageHash      string
	StartedAt           time.Time
}

func (TrafficAttackStarted) EventType() string { return "TrafficAttackStarted" }

type TrafficAttackEnded struct {
	AttackID AttackID
	EndedAt  time.Time
}

func (TrafficAttackEnded) EventType() string { return "TrafficAttackEnded" }

type TrafficAttackMitigated struct {
	AttackID    AttackID
	CommandID   CommandID
	MitigatedAt time.Time
}

func (TrafficAttackMitigated) EventType() string { return "TrafficAttackMitigated" }

type ExternalProviderDegraded struct {
	ProviderID            ProviderID
	FailureProbabilityPPM uint32
	AdditionalLatency     time.Duration
	DegradedAt            time.Time
}

func (ExternalProviderDegraded) EventType() string { return "ExternalProviderDegraded" }

type ExternalProviderRecovered struct {
	ProviderID  ProviderID
	RecoveredAt time.Time
}

func (ExternalProviderRecovered) EventType() string { return "ExternalProviderRecovered" }
