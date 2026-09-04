package events

import "time"

type FirewallRuleUpserted struct {
	Rule       FirewallRule
	Revision   uint64
	Replaced   bool
	UpsertedAt time.Time
}

func (FirewallRuleUpserted) EventType() string { return "FirewallRuleUpserted" }

type FirewallRuleDeleted struct {
	RuleID    FirewallRuleID
	Revision  uint64
	DeletedAt time.Time
}

func (FirewallRuleDeleted) EventType() string { return "FirewallRuleDeleted" }

// FirewallRequestEvaluated records the deterministic first-match decision.
// MatchedRuleID is empty only for the implicit default-allow decision.
type FirewallRequestEvaluated struct {
	RequestID           RequestID
	SourceIP            string
	UserAgent           string
	RegionCode          RegionCode
	MatchedRuleID       FirewallRuleID
	MatchedRuleRevision uint64
	Action              FirewallAction
	EvaluatedAt         time.Time
}

func (FirewallRequestEvaluated) EventType() string { return "FirewallRequestEvaluated" }

// FirewallAvailabilityChanged tracks whether a rule blocks at least one
// uptime-control client. It makes downtime boundaries replayable, including
// expiry instants crossed by a time advance.
type FirewallAvailabilityChanged struct {
	Available       bool
	BlockingRuleIDs []FirewallRuleID
	ChangedAt       time.Time
}

func (FirewallAvailabilityChanged) EventType() string { return "FirewallAvailabilityChanged" }
