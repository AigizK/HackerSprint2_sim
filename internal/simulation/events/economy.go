package events

import "time"

type InfrastructureCostAccrued struct {
	From        time.Time
	To          time.Time
	AmountMinor int64
}

func (InfrastructureCostAccrued) EventType() string { return "InfrastructureCostAccrued" }

type DeploymentCostAccrued struct {
	DeploymentID DeploymentID
	AmountMinor  int64
	AccruedAt    time.Time
}

func (DeploymentCostAccrued) EventType() string { return "DeploymentCostAccrued" }

type RevenueLost struct {
	VisitorID   VisitorID
	ProductID   ProductID
	RequestID   RequestID
	AmountMinor int64
	Reason      RevenueLossReason
	LostAt      time.Time
}

func (RevenueLost) EventType() string { return "RevenueLost" }
