package simulation

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func firewallCommandReceipt(command Command) (model.CommandID, string) {
	switch command := command.(type) {
	case UpsertFirewallRule:
		encoded, _ := json.Marshal(command.Rule)
		return command.CommandID, stableFirewallPayload("upsert", string(encoded))
	case DeleteFirewallRule:
		return command.CommandID, stableFirewallPayload("delete", string(command.RuleID))
	default:
		return "", ""
	}
}

func decideUpsertFirewallRule(state State, command UpsertFirewallRule) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.CommandID == "" {
		return nil, fmt.Errorf("%w: request_id is required", ErrInvalidCommand)
	}
	rule, err := validateAndNormalizeFirewallRule(command.Rule, state.Clock.CurrentTime)
	if err != nil {
		return nil, err
	}
	current, replaced := state.FirewallRules[rule.ID]
	revision := uint64(1)
	if replaced {
		revision = current.Revision + 1
	}
	result := []events.Event{
		events.ServerCommandAccepted{CommandID: command.CommandID, OperationID: model.OperationID(command.CommandID),
			Payload: mustFirewallCommandPayload(command), AcceptedAt: state.Clock.CurrentTime},
		events.FirewallRuleUpserted{Rule: rule, Revision: revision, Replaced: replaced, UpsertedAt: state.Clock.CurrentTime},
	}
	result, err = appendFirewallAvailabilityChange(state, result)
	if err != nil {
		return nil, err
	}
	return appendBackendAvailabilityChange(state, result, state.Clock.CurrentTime)
}

func decideDeleteFirewallRule(state State, command DeleteFirewallRule) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	if command.CommandID == "" || !resourceIDPattern.MatchString(string(command.RuleID)) {
		return nil, fmt.Errorf("%w: invalid firewall delete", ErrInvalidCommand)
	}
	current, exists := state.FirewallRules[command.RuleID]
	if !exists {
		return nil, fmt.Errorf("%w: firewall rule %q", ErrResourceNotFound, command.RuleID)
	}
	result := []events.Event{
		events.ServerCommandAccepted{CommandID: command.CommandID, OperationID: model.OperationID(command.CommandID),
			Payload: mustFirewallCommandPayload(command), AcceptedAt: state.Clock.CurrentTime},
		events.FirewallRuleDeleted{RuleID: command.RuleID, Revision: current.Revision, DeletedAt: state.Clock.CurrentTime},
	}
	result, err := appendFirewallAvailabilityChange(state, result)
	if err != nil {
		return nil, err
	}
	return appendBackendAvailabilityChange(state, result, state.Clock.CurrentTime)
}

func mustFirewallCommandPayload(command Command) string {
	_, payload := firewallCommandReceipt(command)
	return payload
}

func appendFirewallAvailabilityChange(state State, items []events.Event) ([]events.Event, error) {
	working := cloneState(state)
	for _, item := range items {
		if err := working.Apply(item); err != nil {
			return nil, err
		}
	}
	blocking := firewallBlockingRuleIDs(working, state.Clock.CurrentTime)
	available := len(blocking) == 0
	if available == !state.FirewallUnavailable {
		return items, nil
	}
	return append(items, events.FirewallAvailabilityChanged{Available: available, BlockingRuleIDs: blocking, ChangedAt: state.Clock.CurrentTime}), nil
}

func firewallExpiryEvents(state State, from, to time.Time) []events.Event {
	boundaries := firewallExpiryBoundaries(state, from, to)
	result := make([]events.Event, 0)
	unavailable := state.FirewallUnavailable
	for _, at := range boundaries {
		blocking := firewallBlockingRuleIDs(state, at)
		available := len(blocking) == 0
		if available == !unavailable {
			continue
		}
		result = append(result, events.FirewallAvailabilityChanged{Available: available, BlockingRuleIDs: blocking, ChangedAt: at})
		unavailable = !available
	}
	return result
}

func firewallExpiryBoundaries(state State, from, to time.Time) []time.Time {
	boundaries := make([]time.Time, 0)
	seen := make(map[time.Time]struct{})
	for _, stored := range state.FirewallRules {
		expiresAt := stored.Rule.ExpiresAt
		if expiresAt.IsZero() || !expiresAt.After(from) || expiresAt.After(to) {
			continue
		}
		if _, exists := seen[expiresAt]; !exists {
			seen[expiresAt] = struct{}{}
			boundaries = append(boundaries, expiresAt)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	return boundaries
}
