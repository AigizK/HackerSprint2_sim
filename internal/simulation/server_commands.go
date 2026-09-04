package simulation

import (
	"fmt"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// Each server operation has its own receipt; there is no desired replica count.
func serverCommandPayload(command Command) string {
	switch command := command.(type) {
	case AddServer:
		return fmt.Sprintf("server.create:%s:%s:%d:%d", command.OperationID, command.ServerID, command.CapacityUnits, command.CostPerHourMinor)
	case RemoveServer:
		return fmt.Sprintf("server.delete:%s:%s", command.OperationID, command.ServerID)
	default:
		return ""
	}
}

func serverCommandReceipt(command Command) (model.CommandID, string) {
	switch command := command.(type) {
	case AddServer:
		return command.CommandID, serverCommandPayload(command)
	case RemoveServer:
		return command.CommandID, serverCommandPayload(command)
	default:
		return "", ""
	}
}
