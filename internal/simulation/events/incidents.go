package events

import "time"

type TrafficAttackStarted struct {
	AttackID            AttackID
	Kind                AttackKind
	TargetPage          PageType
	RequestsPerMinute   int64
	LoadUnitsPerRequest int64
	SourceCIDR          string
	UserAgent           string
	RegionCode          RegionCode
	Resolution          AttackResolution
	ExpectedEndAt       time.Time
	StartedAt           time.Time
}

func (TrafficAttackStarted) EventType() string { return "TrafficAttackStarted" }

type TrafficAttackEnded struct {
	AttackID AttackID
	EndedAt  time.Time
}

func (TrafficAttackEnded) EventType() string { return "TrafficAttackEnded" }
