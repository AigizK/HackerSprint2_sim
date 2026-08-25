package events

import "time"

type EconomyConfigured struct {
	Currency                 string
	InitialBalanceMinor      int64
	StopRunOnNegativeBalance bool
	ServerBillingPeriod      time.Duration
	ConfiguredAt             time.Time
}

func (EconomyConfigured) EventType() string { return "EconomyConfigured" }

type InfrastructureCostAccrued struct {
	ServerID    ServerID
	From        time.Time
	To          time.Time
	BilledHours int64
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
