package simulation

import "github.com/aigizk/hackersprint2-sim/internal/simulation/events"

type StoredEvent struct {
	Version uint64
	Event   events.Event
}
