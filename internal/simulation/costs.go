package simulation

import "github.com/aigizk/hackersprint2-sim/internal/simulation/model"

type CostsView struct {
	Currency                string
	ServerCostMinor         int64
	BackupStorageCostMinor  int64
	TotalCostMinor          int64
	CurrentCostPerHourMinor int64
}

func (p Projection) Costs() CostsView {
	return CostsView{Currency: p.state.Costs.Currency, ServerCostMinor: p.state.Costs.ServerCostMinor,
		BackupStorageCostMinor:  p.state.Costs.BackupStorageCostMinor,
		TotalCostMinor:          p.state.Costs.ServerCostMinor + p.state.Costs.BackupStorageCostMinor,
		CurrentCostPerHourMinor: p.Resources().TotalCostPerHourMinor + currentBackupStorageCost(p.state)}
}

func currentBackupStorageCost(state State) int64 {
	var result int64
	for _, backup := range state.Backups {
		if backup.Status == model.BackupReady {
			result += backup.StorageCostPerHourMinor
		}
	}
	return result
}
