package simulation

import (
	"reflect"
	"sort"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// backendUnavailablePages evaluates the same no-extra-load health check used
// for time-based uptime. Every configured page must fit on at least one active
// backend after live allocations and unblocked DDoS load are accounted for.
func backendUnavailablePages(state State, at time.Time) []model.PageType {
	pages := make([]model.PageType, 0, len(state.Pages))
	for page := range state.Pages {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	unavailable := make([]model.PageType, 0, len(pages))
	for _, page := range pages {
		required := state.Pages[page].LoadUnits
		available := false
		for _, server := range state.Servers {
			if server.Status != model.ServerActive || (server.Role != "" && server.Role != model.ServerRoleBackend) {
				continue
			}
			if serverAvailableCapacity(state, server.ID, page, at) >= required {
				available = true
				break
			}
		}
		if !available {
			unavailable = append(unavailable, page)
		}
	}
	return unavailable
}

func appendBackendAvailabilityChange(state State, items []events.Event, at time.Time) ([]events.Event, error) {
	working := cloneState(state)
	for _, item := range items {
		if err := working.Apply(item); err != nil {
			return nil, err
		}
	}
	return appendBackendAvailabilityChangeToWorking(items, &working, at)
}

func appendBackendAvailabilityChangeToWorking(items []events.Event, working *State, at time.Time) ([]events.Event, error) {
	pages := backendUnavailablePages(*working, at)
	unavailable := len(pages) > 0
	if unavailable == working.BackendUnavailable {
		return items, nil
	}
	event := events.BackendAvailabilityChanged{Available: !unavailable, UnavailablePages: pages, ChangedAt: at}
	if err := working.Apply(event); err != nil {
		return nil, err
	}
	return append(items, event), nil
}

func validBackendAvailabilityEvent(state State, event events.BackendAvailabilityChanged) bool {
	pages := backendUnavailablePages(state, event.ChangedAt)
	return event.Available == (len(pages) == 0) && reflect.DeepEqual(event.UnavailablePages, pages) &&
		event.Available == state.BackendUnavailable
}
