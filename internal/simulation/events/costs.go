package events

import "time"

type CostsConfigured struct {
	Currency     string
	ConfiguredAt time.Time
}

func (CostsConfigured) EventType() string { return "CostsConfigured" }

type InfrastructureCostAccrued struct {
	ServerID    ServerID
	From        time.Time
	To          time.Time
	BilledHours int64
	// TotalAmountMinor is used by monthly tariffs. It is the cumulative
	// prorated charge since activation and makes event replay exact.
	TotalAmountMinor int64
	AmountMinor      int64
}

func (InfrastructureCostAccrued) EventType() string { return "InfrastructureCostAccrued" }
