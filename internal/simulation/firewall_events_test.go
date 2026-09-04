package simulation

import (
	"reflect"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func TestFirewallEventsRoundTripThroughCodec(t *testing.T) {
	at := time.Date(2032, 2, 3, 4, 5, 6, 7, time.UTC)
	expiresAt := at.Add(time.Hour)
	userAgent := &model.UserAgentMatch{Operator: model.UserAgentContains, Value: "CompromisedCamera/"}
	rule := model.FirewallRule{ID: "deny-cameras", Priority: 100, Action: model.FirewallDeny,
		Match: model.FirewallMatch{SourceCIDR: "203.0.113.0/24", RegionCode: "RU", UserAgent: userAgent}, Enabled: true, ExpiresAt: expiresAt}
	all := []events.Event{
		events.FirewallRuleUpserted{Rule: rule, Revision: 2, Replaced: true, UpsertedAt: at},
		events.FirewallRuleDeleted{RuleID: rule.ID, Revision: 2, DeletedAt: at},
		events.FirewallRequestEvaluated{RequestID: "request-1", SourceIP: "203.0.113.42", UserAgent: "CompromisedCamera/1.0",
			RegionCode: "RU", MatchedRuleID: rule.ID, MatchedRuleRevision: 2, Action: model.FirewallDeny, EvaluatedAt: at},
		events.FirewallAvailabilityChanged{Available: false, BlockingRuleIDs: []model.FirewallRuleID{rule.ID}, ChangedAt: at},
	}
	for _, original := range all {
		t.Run(original.EventType(), func(t *testing.T) {
			eventType, payload, err := EncodeEvent(original)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeEvent(eventType, payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, original) {
				t.Fatalf("decoded = %#v, want %#v", decoded, original)
			}
		})
	}
}

func TestRequestAndTrafficEventsCarryFirewallIdentity(t *testing.T) {
	at := time.Date(2032, 2, 3, 4, 5, 6, 7, time.UTC)
	all := []events.Event{
		events.VisitorArrived{VisitorID: "visitor", SourceIP: "198.51.100.10", UserAgent: "Browser/1", RegionCode: "RU", ArrivedAt: at},
		events.PageRequestStarted{RequestID: "request", Source: model.RequestSourceVisitor, VisitorID: "visitor", Page: model.PageProductList,
			SourceIP: "198.51.100.10", UserAgent: "Browser/1", RegionCode: "RU", LoadUnits: 1, StartedAt: at},
		events.PageRequestRejected{RequestID: "request", FirewallRuleID: "deny-ru", StatusCode: 403,
			ErrorCode: model.FailureFirewallDenied, Message: "request denied by firewall", RejectedAt: at},
		events.TrafficAttackStarted{AttackID: "attack", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
			RequestsPerMinute: 100, LoadUnitsPerRequest: 1, SourceCIDR: "203.0.113.0/24", UserAgent: "CompromisedCamera/1.0",
			RegionCode: "ZZ", Resolution: model.AttackScaleOrExpiry, ExpectedEndAt: at.Add(time.Hour), StartedAt: at},
	}
	for _, original := range all {
		t.Run(original.EventType(), func(t *testing.T) {
			eventType, payload, err := EncodeEvent(original)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeEvent(eventType, payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, original) {
				t.Fatalf("decoded = %#v, want %#v", decoded, original)
			}
		})
	}
}

func TestFirewallModelConstants(t *testing.T) {
	if model.FirewallAllow != "allow" || model.FirewallDeny != "deny" || model.UserAgentEquals != "equals" ||
		model.UserAgentContains != "contains" || model.FailureFirewallDenied != "FIREWALL_DENIED" {
		t.Fatal("firewall constants differ from the public contract")
	}
}
