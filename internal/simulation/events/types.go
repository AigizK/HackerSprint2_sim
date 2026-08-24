package events

import (
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type ProductID = model.ProductID
type PurchaseID = model.PurchaseID
type VisitorID = model.VisitorID
type RequestID = model.RequestID
type ServerID = model.ServerID
type BugID = model.BugID
type DeploymentID = model.DeploymentID
type OperationID = model.OperationID
type ProviderID = model.ProviderID
type AttackID = model.AttackID
type CommandID = model.CommandID
type PageType = model.PageType
type RequestSource = model.RequestSource
type RequestFailureCode = model.RequestFailureCode
type RevenueLossReason = model.RevenueLossReason
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
