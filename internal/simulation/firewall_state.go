package simulation

import (
	"fmt"
	"reflect"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func (s *State) applyFirewallEvent(event events.Event) (bool, error) {
	switch event := event.(type) {
	case events.FirewallRuleUpserted:
		if err := ensureRunning(*s); err != nil {
			return true, err
		}
		rule, err := validateAndNormalizeFirewallRule(event.Rule, event.UpsertedAt)
		// The command validator requires future expiry relative to UpsertedAt.
		if err != nil || !reflect.DeepEqual(rule, event.Rule) || !event.UpsertedAt.Equal(s.Clock.CurrentTime) {
			return true, fmt.Errorf("%w: invalid firewall rule upsert", ErrInvalidEvent)
		}
		current, exists := s.FirewallRules[event.Rule.ID]
		expectedRevision := uint64(1)
		if exists {
			expectedRevision = current.Revision + 1
		}
		if event.Replaced != exists || event.Revision != expectedRevision {
			return true, fmt.Errorf("%w: invalid firewall revision", ErrInvalidEvent)
		}
		if s.FirewallRules == nil {
			s.FirewallRules = make(map[model.FirewallRuleID]FirewallRuleState)
		}
		s.FirewallRules[event.Rule.ID] = FirewallRuleState{Rule: rule, Revision: event.Revision}
		return true, nil

	case events.FirewallRuleDeleted:
		if err := ensureRunning(*s); err != nil {
			return true, err
		}
		current, exists := s.FirewallRules[event.RuleID]
		if !exists || current.Revision != event.Revision || !event.DeletedAt.Equal(s.Clock.CurrentTime) {
			return true, fmt.Errorf("%w: invalid firewall rule deletion", ErrInvalidEvent)
		}
		delete(s.FirewallRules, event.RuleID)
		return true, nil

	case events.FirewallRequestEvaluated:
		if err := ensureRunning(*s); err != nil {
			return true, err
		}
		request, exists := s.Requests[event.RequestID]
		profile := model.ClientProfile{SourceIP: event.SourceIP, UserAgent: event.UserAgent, RegionCode: event.RegionCode}
		if !exists || request.Status != model.PageRequestInProgress || !validFactTime(*s, event.EvaluatedAt) ||
			request.SourceIP != event.SourceIP || request.UserAgent != event.UserAgent || request.RegionCode != event.RegionCode ||
			validateClientProfile(profile) != nil {
			return true, fmt.Errorf("%w: invalid firewall request evaluation", ErrInvalidEvent)
		}
		decision := evaluateFirewall(*s, profile, event.EvaluatedAt)
		if event.Action != decision.Action || event.MatchedRuleID != decision.RuleID || event.MatchedRuleRevision != decision.Revision {
			return true, fmt.Errorf("%w: firewall decision does not match rule state", ErrInvalidEvent)
		}
		request.FirewallAction = event.Action
		request.FirewallRuleID = event.MatchedRuleID
		s.Requests[event.RequestID] = request
		return true, nil

	case events.FirewallAvailabilityChanged:
		if err := ensureRunExists(*s); err != nil {
			return true, err
		}
		if !validFactTime(*s, event.ChangedAt) {
			return true, fmt.Errorf("%w: invalid firewall availability time", ErrInvalidEvent)
		}
		blocking := firewallBlockingRuleIDs(*s, event.ChangedAt)
		if event.Available != (len(blocking) == 0) || !reflect.DeepEqual(event.BlockingRuleIDs, blocking) || event.Available == !s.FirewallUnavailable {
			return true, fmt.Errorf("%w: invalid firewall availability transition", ErrInvalidEvent)
		}
		s.FirewallUnavailable = !event.Available
		if s.Site.Status == model.SiteRunning {
			if !event.Available && s.Site.UnavailableSince.IsZero() {
				s.Site.UnavailableSince = event.ChangedAt
			}
			if event.Available && siteDependenciesAvailable(*s) && !s.Site.UnavailableSince.IsZero() {
				s.Site.DowntimeDuration += event.ChangedAt.Sub(s.Site.UnavailableSince)
				s.Site.UnavailableSince = time.Time{}
			}
		}
		return true, nil
	default:
		return false, nil
	}
}

func siteDependenciesAvailable(state State) bool {
	if state.FirewallUnavailable || state.BackendUnavailable {
		return false
	}
	if state.Site.DatabaseID == "" {
		return true
	}
	database, exists := state.Databases[state.Site.DatabaseID]
	return exists && database.Available()
}
