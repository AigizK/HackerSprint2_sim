package simulation

import (
	"fmt"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func databaseCommandPayload(command Command) string {
	switch command := command.(type) {
	case AddTypedServer:
		return fmt.Sprintf("server.create:%s:%s:%s:%s", command.OperationID, command.ServerID, command.InstanceType, command.Name)
	case CreateDatabase:
		return fmt.Sprintf("database.create:%s:%s:%s", command.DatabaseID, command.ServerID, command.Name)
	case GrowDatabase:
		return fmt.Sprintf("database.grow:%s:%d:%d", command.GrowthID, command.DataDeltaBytes, command.LogsDeltaBytes)
	case CleanupDatabaseLogs:
		return fmt.Sprintf("disk.cleanup:%s", command.ServerID)
	case BackupDatabase:
		return fmt.Sprintf("database.backup:%s:%s:%s:%t", command.OperationID, command.BackupID, command.DatabaseID, command.Fail)
	case RestoreDatabase:
		return fmt.Sprintf("database.restore:%s:%s:%s:%t", command.OperationID, command.BackupID, command.DatabaseID, command.Fail)
	case StopSite:
		return fmt.Sprintf("site.stop:%s", command.OperationID)
	case StartSite:
		return "site.start"
	case SetSiteDatabase:
		return fmt.Sprintf("site.database.set:%s:%s", command.DatabaseID, command.ExpectedCurrentDatabaseID)
	default:
		return ""
	}
}

func databaseCommandReceipt(command Command) (model.CommandID, string) {
	switch command := command.(type) {
	case AddTypedServer:
		return command.CommandID, databaseCommandPayload(command)
	case CreateDatabase:
		return command.CommandID, databaseCommandPayload(command)
	case GrowDatabase:
		return command.CommandID, databaseCommandPayload(command)
	case CleanupDatabaseLogs:
		return command.CommandID, databaseCommandPayload(command)
	case BackupDatabase:
		return command.CommandID, databaseCommandPayload(command)
	case RestoreDatabase:
		return command.CommandID, databaseCommandPayload(command)
	case StopSite:
		return command.CommandID, databaseCommandPayload(command)
	case StartSite:
		return command.CommandID, databaseCommandPayload(command)
	case SetSiteDatabase:
		return command.CommandID, databaseCommandPayload(command)
	default:
		return "", ""
	}
}
