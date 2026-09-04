package events

import (
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type ProductID = model.ProductID
type MessageID = model.MessageID
type VisitorID = model.VisitorID
type RequestID = model.RequestID
type ServerID = model.ServerID
type OperationID = model.OperationID
type AttackID = model.AttackID
type CommandID = model.CommandID
type CredentialID = model.CredentialID
type DatabaseID = model.DatabaseID
type BackupID = model.BackupID
type GrowthID = model.GrowthID
type ServerRole = model.ServerRole
type InstanceType = model.InstanceType
type DatabaseAvailabilityReason = model.DatabaseAvailabilityReason
type FirewallRuleID = model.FirewallRuleID
type FirewallAction = model.FirewallAction
type RegionCode = model.RegionCode
type FirewallRule = model.FirewallRule
type PageType = model.PageType
type RequestSource = model.RequestSource
type RequestFailureCode = model.RequestFailureCode
type VisitorOutcome = model.VisitorOutcome
type OperationKind = model.OperationKind
type AttackKind = model.AttackKind
type AttackResolution = model.AttackResolution

type ScheduledWorldEvent struct {
	Sequence uint64
	OccursAt time.Time
	Event    Event
}

type EventSchedule []ScheduledWorldEvent
