package httpapi

import "github.com/aigizk/hackersprint2-sim/internal/simulation"

type costsResponse struct {
	Currency                string `json:"currency"`
	ServerCostMinor         int64  `json:"server_cost_minor"`
	BackupStorageCostMinor  int64  `json:"backup_storage_cost_minor"`
	TotalCostMinor          int64  `json:"total_cost_minor"`
	CurrentCostPerHourMinor int64  `json:"current_cost_per_hour_minor"`
}

func costsResponseFrom(value simulation.CostsView) costsResponse {
	return costsResponse{Currency: value.Currency, ServerCostMinor: value.ServerCostMinor,
		BackupStorageCostMinor: value.BackupStorageCostMinor,
		TotalCostMinor:         value.TotalCostMinor, CurrentCostPerHourMinor: value.CurrentCostPerHourMinor}
}
