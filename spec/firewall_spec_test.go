package spec_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

var browserRU = model.ClientProfile{SourceIP: "203.0.113.42", UserAgent: "Mozilla/5.0 Camera Device", RegionCode: "RU"}

func firewallRule(id string, priority int, action model.FirewallAction, match model.FirewallMatch) model.FirewallRule {
	return model.FirewallRule{ID: model.FirewallRuleID(id), Priority: priority, Action: action, Match: match, Enabled: true}
}

func upsertFirewall(h *databaseHarness, rule model.FirewallRule) []events.Event {
	h.t.Helper()
	return h.execute(simulation.UpsertFirewallRule{CommandID: h.id("firewall-upsert"), Rule: rule})
}

func deleteFirewall(h *databaseHarness, id model.FirewallRuleID) []events.Event {
	h.t.Helper()
	return h.execute(simulation.DeleteFirewallRule{CommandID: h.id("firewall-delete"), RuleID: id})
}

func openAs(h *databaseHarness, profile model.ClientProfile) []events.Event {
	h.t.Helper()
	id := h.id("firewall-request")
	return h.execute(simulation.OpenPage{RequestID: model.RequestID(id), VisitorID: model.VisitorID(id),
		Page: model.PageProductList, SourceIP: profile.SourceIP, UserAgent: profile.UserAgent, RegionCode: profile.RegionCode})
}

func firewallEvaluation(t *testing.T, items []events.Event) events.FirewallRequestEvaluated {
	t.Helper()
	for _, item := range items {
		if event, ok := item.(events.FirewallRequestEvaluated); ok {
			return event
		}
	}
	t.Fatalf("events do not contain FirewallRequestEvaluated: %v", eventTypes(items))
	return events.FirewallRequestEvaluated{}
}

func requestResult(t *testing.T, items []events.Event) (int, model.RequestFailureCode, model.FirewallRuleID) {
	t.Helper()
	for _, item := range items {
		switch event := item.(type) {
		case events.PageRequestCompleted:
			return event.StatusCode, event.ErrorCode, event.FirewallRuleID
		case events.PageRequestRejected:
			return event.StatusCode, event.ErrorCode, event.FirewallRuleID
		}
	}
	t.Fatalf("events do not contain request result: %v", eventTypes(items))
	return 0, "", ""
}

func requireRequestResult(t *testing.T, items []events.Event, status int, code model.RequestFailureCode, ruleID model.FirewallRuleID) {
	t.Helper()
	gotStatus, gotCode, gotRuleID := requestResult(t, items)
	if gotStatus != status || gotCode != code || gotRuleID != ruleID {
		t.Fatalf("request result = (%d, %q, %q), want (%d, %q, %q)", gotStatus, gotCode, gotRuleID, status, code, ruleID)
	}
}

func TestFirewallRuleManagement(t *testing.T) {
	t.Run("new_run_has_empty_rule_list", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		version := h.state().Version
		if got := h.projection().FirewallRules(); len(got) != 0 || h.state().Version != version {
			t.Fatalf("rules/version = %#v/%d, want empty/%d", got, h.state().Version, version)
		}
	})

	t.Run("create_and_replace_all_match_kinds", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		ua := &model.UserAgentMatch{Operator: model.UserAgentContains, Value: "Camera"}
		rule := firewallRule("deny-clients", 20, model.FirewallDeny, model.FirewallMatch{
			SourceCIDR: "2001:db8::/32", RegionCode: "RU", UserAgent: ua,
		})
		requireEventTypes(t, upsertFirewall(h, rule), "FirewallRuleUpserted")
		got := h.projection().FirewallRules()
		if len(got) != 1 || got[0].Revision != 1 || !reflect.DeepEqual(got[0].Rule, rule) {
			t.Fatalf("created rule = %#v", got)
		}
		rule.Priority = 5
		rule.Action = model.FirewallAllow
		rule.Enabled = false
		rule.ExpiresAt = h.state().Clock.CurrentTime.Add(time.Hour)
		items := upsertFirewall(h, rule)
		upserted := items[1].(events.FirewallRuleUpserted)
		if !upserted.Replaced || upserted.Revision != 2 || !reflect.DeepEqual(h.projection().FirewallRules()[0].Rule, rule) {
			t.Fatalf("replacement = %#v; state = %#v", upserted, h.projection().FirewallRules())
		}
		rule.ExpiresAt = time.Time{}
		upsertFirewall(h, rule)
		got = h.projection().FirewallRules()
		if got[0].Revision != 3 || !got[0].Rule.ExpiresAt.IsZero() {
			t.Fatalf("expiry was not cleared: %#v", got[0])
		}
	})

	t.Run("list_is_stable_complete_and_sorted", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for _, rule := range []model.FirewallRule{
			firewallRule("beta", 10, model.FirewallDeny, model.FirewallMatch{RegionCode: "US"}),
			firewallRule("zeta", 20, model.FirewallDeny, model.FirewallMatch{RegionCode: "DE"}),
			firewallRule("alpha", 10, model.FirewallAllow, model.FirewallMatch{RegionCode: "KZ"}),
		} {
			upsertFirewall(h, rule)
		}
		disabled := firewallRule("disabled", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "ZZ"})
		disabled.Enabled = false
		upsertFirewall(h, disabled)
		first, second := h.projection().FirewallRules(), h.projection().FirewallRules()
		if !reflect.DeepEqual(first, second) {
			t.Fatal("repeated rule lists differ")
		}
		got := []model.FirewallRuleID{first[0].Rule.ID, first[1].Rule.ID, first[2].Rule.ID, first[3].Rule.ID}
		want := []model.FirewallRuleID{"disabled", "alpha", "beta", "zeta"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})

	t.Run("delete_existing_and_missing", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		rule := firewallRule("deny-subnet", 1, model.FirewallDeny, model.FirewallMatch{SourceCIDR: "203.0.113.0/24"})
		for range 3 {
			upsertFirewall(h, rule)
		}
		items := deleteFirewall(h, rule.ID)
		deleted := items[1].(events.FirewallRuleDeleted)
		if deleted.Revision != 3 || len(h.projection().FirewallRules()) != 0 {
			t.Fatalf("deletion = %#v, rules = %#v", deleted, h.projection().FirewallRules())
		}
		version := h.state().Version
		h.executeError(simulation.ErrResourceNotFound, simulation.DeleteFirewallRule{CommandID: h.id("missing"), RuleID: "missing"})
		if h.state().Version != version {
			t.Fatal("failed delete changed journal")
		}
	})

	t.Run("rule_commands_are_idempotent", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		command := simulation.UpsertFirewallRule{CommandID: "same-request", Rule: firewallRule("rule", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "DE"})}
		h.execute(command)
		version := h.state().Version
		if got := h.execute(command); len(got) != 0 || h.state().Version != version {
			t.Fatalf("retry = %v, version = %d", got, h.state().Version)
		}
		command.Rule.Priority = 2
		h.executeError(simulation.ErrIdempotencyConflict, command)
	})
}

func TestFirewallRuleValidation(t *testing.T) {
	longValue := strings.Repeat("x", 2049)
	cases := []struct {
		name string
		rule model.FirewallRule
	}{
		{"match_must_not_be_empty", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{})},
		{"rule_id_must_not_be_empty", firewallRule("", 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})},
		{"rule_id_must_start_with_alphanumeric", firewallRule("-bad", 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})},
		{"rule_id_must_not_contain_spaces", firewallRule("bad id", 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})},
		{"rule_id_has_length_bound", firewallRule(strings.Repeat("x", 129), 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})},
		{"priority_must_be_non_negative", firewallRule("valid", -1, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})},
		{"action_is_closed_enum", firewallRule("valid", 0, "drop", model.FirewallMatch{RegionCode: "RU"})},
		{"ipv4_cidr_must_be_parseable", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{SourceCIDR: "203.0.113.0/33"})},
		{"cidr_must_have_zero_host_bits", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{SourceCIDR: "203.0.113.7/24"})},
		{"ipv6_cidr_must_be_parseable", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{SourceCIDR: "2001:db8::/129"})},
		{"region_must_be_uppercase", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "ru"})},
		{"region_must_have_two_letters", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "RUS"})},
		{"user_agent_operator_is_closed_enum", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: "regex", Value: "x"}})},
		{"user_agent_value_must_not_be_empty", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentEquals}})},
		{"user_agent_value_has_length_bound", firewallRule("valid", 0, model.FirewallDeny, model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentContains, Value: longValue}})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDatabaseHarness(t, model.InstanceDBSmall)
			version := h.state().Version
			h.executeError(simulation.ErrInvalidCommand, simulation.UpsertFirewallRule{CommandID: "invalid-request", Rule: tc.rule})
			if h.state().Version != version || len(h.projection().FirewallRules()) != 0 {
				t.Fatal("invalid rule mutated the run")
			}
		})
	}

	t.Run("expiry_must_be_strictly_future", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for index, expiry := range []time.Time{h.state().Clock.CurrentTime.Add(-time.Nanosecond), h.state().Clock.CurrentTime} {
			rule := firewallRule(fmt.Sprintf("expired-%d", index), 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "DE"})
			rule.ExpiresAt = expiry
			h.executeError(simulation.ErrInvalidCommand, simulation.UpsertFirewallRule{CommandID: h.id("invalid-expiry"), Rule: rule})
		}
		rule := firewallRule("future", 0, model.FirewallDeny, model.FirewallMatch{RegionCode: "DE"})
		rule.ExpiresAt = h.state().Clock.CurrentTime.Add(time.Nanosecond)
		upsertFirewall(h, rule)
	})

	t.Run("client_profile_is_all_or_nothing_and_valid", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for index, profile := range []model.ClientProfile{
			{SourceIP: "bad", UserAgent: "UA", RegionCode: "RU"},
			{SourceIP: "203.0.113.1", UserAgent: "", RegionCode: "RU"},
			{SourceIP: "203.0.113.1", UserAgent: "UA", RegionCode: "ru"},
		} {
			h.executeError(simulation.ErrInvalidCommand, simulation.OpenPage{RequestID: model.RequestID(fmt.Sprintf("bad-%d", index)), VisitorID: "visitor",
				Page: model.PageProductList, SourceIP: profile.SourceIP, UserAgent: profile.UserAgent, RegionCode: profile.RegionCode})
		}
	})
}

func TestFirewallMatching(t *testing.T) {
	tests := []struct {
		name    string
		match   model.FirewallMatch
		profile model.ClientProfile
		denied  bool
	}{
		{"ipv4_address_inside_cidr_matches", model.FirewallMatch{SourceCIDR: "203.0.113.0/24"}, browserRU, true},
		{"ipv4_address_outside_cidr_does_not_match", model.FirewallMatch{SourceCIDR: "203.0.114.0/24"}, browserRU, false},
		{"ipv4_32_matches_only_one_address", model.FirewallMatch{SourceCIDR: "203.0.113.42/32"}, browserRU, true},
		{"ipv6_address_inside_cidr_matches", model.FirewallMatch{SourceCIDR: "2001:db8::/32"}, model.ClientProfile{SourceIP: "2001:db8::42", UserAgent: "UA", RegionCode: "RU"}, true},
		{"ipv4_rule_never_matches_ipv6", model.FirewallMatch{SourceCIDR: "203.0.113.0/24"}, model.ClientProfile{SourceIP: "2001:db8::42", UserAgent: "UA", RegionCode: "RU"}, false},
		{"region_condition_matches_exact_code", model.FirewallMatch{RegionCode: "RU"}, browserRU, true},
		{"unknown_region_zz_can_be_matched", model.FirewallMatch{RegionCode: "ZZ"}, model.ClientProfile{SourceIP: "203.0.113.2", UserAgent: "UA", RegionCode: "ZZ"}, true},
		{"user_agent_equals_is_exact", model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentEquals, Value: browserRU.UserAgent}}, browserRU, true},
		{"user_agent_equals_is_case_sensitive", model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentEquals, Value: strings.ToLower(browserRU.UserAgent)}}, browserRU, false},
		{"user_agent_contains_matches_literal_substring", model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentContains, Value: "Camera"}}, browserRU, true},
		{"user_agent_contains_is_case_sensitive", model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentContains, Value: "camera"}}, browserRU, false},
		{"compound_rule_requires_every_condition", model.FirewallMatch{SourceCIDR: "203.0.113.0/24", RegionCode: "RU", UserAgent: &model.UserAgentMatch{Operator: model.UserAgentContains, Value: "Camera"}}, browserRU, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDatabaseHarness(t, model.InstanceDBSmall)
			upsertFirewall(h, firewallRule("deny", 10, model.FirewallDeny, tc.match))
			items := openAs(h, tc.profile)
			evaluation := firewallEvaluation(t, items)
			if tc.denied {
				requireRequestResult(t, items, 403, model.FailureFirewallDenied, "deny")
				if evaluation.Action != model.FirewallDeny || evaluation.MatchedRuleRevision != 1 {
					t.Fatalf("evaluation = %#v", evaluation)
				}
			} else {
				requireRequestResult(t, items, 200, "", "")
				if evaluation.Action != model.FirewallAllow || evaluation.MatchedRuleID != "" {
					t.Fatalf("default evaluation = %#v", evaluation)
				}
			}
		})
	}

	t.Run("priority_ties_allow_and_disabled_rules", func(t *testing.T) {
		tests := []struct {
			name  string
			rules []model.FirewallRule
			want  model.FirewallRuleID
			code  int
		}{
			{"lower_priority_number_wins", []model.FirewallRule{
				firewallRule("allow", 10, model.FirewallAllow, model.FirewallMatch{RegionCode: "RU"}),
				firewallRule("deny", 20, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})}, "allow", 200},
			{"rule_id_breaks_equal_priority_ties", []model.FirewallRule{
				firewallRule("beta", 10, model.FirewallAllow, model.FirewallMatch{RegionCode: "RU"}),
				firewallRule("alpha", 10, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})}, "alpha", 403},
			{"nonmatching_high_priority_is_skipped", []model.FirewallRule{
				firewallRule("skip", 1, model.FirewallAllow, model.FirewallMatch{RegionCode: "US"}),
				firewallRule("deny", 2, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})}, "deny", 403},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				h := newDatabaseHarness(t, model.InstanceDBSmall)
				for _, rule := range tc.rules {
					upsertFirewall(h, rule)
				}
				items := openAs(h, browserRU)
				if got := firewallEvaluation(t, items).MatchedRuleID; got != tc.want {
					t.Fatalf("matched rule = %q, want %q", got, tc.want)
				}
				status, _, _ := requestResult(t, items)
				if status != tc.code {
					t.Fatalf("status = %d, want %d", status, tc.code)
				}
			})
		}

		h := newDatabaseHarness(t, model.InstanceDBSmall)
		disabled := firewallRule("disabled", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})
		disabled.Enabled = false
		upsertFirewall(h, disabled)
		upsertFirewall(h, firewallRule("allowed", 2, model.FirewallAllow, model.FirewallMatch{RegionCode: "RU"}))
		requireRequestResult(t, openAs(h, browserRU), 200, "", "allowed")
	})
}

func TestFirewallRequestEffects(t *testing.T) {
	t.Run("denied_request_uses_no_backend_or_database", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		upsertFirewall(h, firewallRule("deny-camera", 1, model.FirewallDeny,
			model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentContains, Value: "Camera"}}))
		before := h.state()
		items := openAs(h, browserRU)
		requireRequestResult(t, items, 403, model.FailureFirewallDenied, "deny-camera")
		if eventTypes(items)["PageRequestAccepted"] != 0 || eventTypes(items)["DatabaseConnectionOpened"] != 0 ||
			h.state().DatabaseConnectionCounts["db-main"] != before.DatabaseConnectionCounts["db-main"] ||
			h.state().UsedCapacityByServer["backend-1"] != before.UsedCapacityByServer["backend-1"] {
			t.Fatalf("denied request consumed resources: %v", eventTypes(items))
		}
	})

	t.Run("firewall_runs_before_database_limit", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		for range 100 {
			h.open()
		}
		upsertFirewall(h, firewallRule("deny", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"}))
		items := openAs(h, browserRU)
		requireRequestResult(t, items, 403, model.FailureFirewallDenied, "deny")
		if eventTypes(items)["DatabaseConnectionRejected"] != 0 {
			t.Fatal("database limit ran before firewall")
		}
	})

	t.Run("allowed_and_denied_logs_keep_identity_and_rule", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		upsertFirewall(h, firewallRule("allow-ru", 1, model.FirewallAllow, model.FirewallMatch{RegionCode: "RU"}))
		openAs(h, browserRU)
		upsertFirewall(h, firewallRule("deny-us", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "US"}))
		blocked := model.ClientProfile{SourceIP: "203.0.113.3", UserAgent: "Browser", RegionCode: "US"}
		openAs(h, blocked)
		logsView, err := h.projection().Logs(simulation.LogsQuery{})
		if err != nil || len(logsView.Logs) != 2 {
			t.Fatalf("logs = %#v, err = %v", logsView, err)
		}
		if got := logsView.Logs[0].Entry; got.SourceIP != browserRU.SourceIP || got.UserAgent != browserRU.UserAgent || got.RegionCode != browserRU.RegionCode || got.FirewallRuleID != "allow-ru" || got.StatusCode != 200 {
			t.Fatalf("allow log = %#v", got)
		}
		if got := logsView.Logs[1].Entry; got.SourceIP != blocked.SourceIP || got.FirewallRuleID != "deny-us" || got.StatusCode != 403 || got.ErrorCode != model.FailureFirewallDenied {
			t.Fatalf("deny log = %#v", got)
		}
		metrics := h.projection().Metrics(simulation.MetricsQuery{}).Current
		if metrics.Responses200 != 1 || metrics.Responses403 != 1 || metrics.ErrorRate != .5 {
			t.Fatalf("metrics = %#v", metrics)
		}
	})

	t.Run("probe_uses_deterministic_client_profile", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		profile := simulation.ProbeClientProfile(h.state().Seed)
		upsertFirewall(h, firewallRule("deny-probe", 1, model.FirewallDeny, model.FirewallMatch{SourceCIDR: profile.SourceIP + "/32"}))
		items := h.execute(simulation.ProbePage{RequestID: model.RequestID(h.id("probe")), Page: model.PageProductList})
		requireRequestResult(t, items, 403, model.FailureFirewallDenied, "deny-probe")
		started := items[0].(events.PageRequestStarted)
		if started.SourceIP != profile.SourceIP || started.UserAgent != profile.UserAgent || started.RegionCode != profile.RegionCode {
			t.Fatalf("probe profile = %#v, want %#v", started, profile)
		}
	})

	t.Run("blocked_first_page_ends_visitor_journey", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		visitorID := model.VisitorID("blocked-visitor")
		profile := simulation.VisitorClientProfile(h.state().Seed, visitorID)
		upsertFirewall(h, firewallRule("deny-visitor", 1, model.FirewallDeny,
			model.FirewallMatch{SourceCIDR: profile.SourceIP + "/32"}))
		items := h.execute(simulation.SimulateVisitor{VisitorID: visitorID})
		var outcome model.VisitorOutcome
		for _, item := range items {
			if completed, ok := item.(events.VisitorJourneyCompleted); ok {
				outcome = completed.Outcome
			}
		}
		if outcome != model.VisitorLeftAfterPageError || eventTypes(items)["ProductSelected"] != 0 {
			t.Fatalf("blocked journey events/outcome = %v/%q", eventTypes(items), outcome)
		}
	})

	t.Run("default_allowed_log_has_no_rule", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		openAs(h, browserRU)
		view, err := h.projection().Logs(simulation.LogsQuery{})
		if err != nil || len(view.Logs) != 1 || view.Logs[0].Entry.FirewallRuleID != "" || view.Logs[0].Entry.SourceIP != browserRU.SourceIP {
			t.Fatalf("default allow log = %#v, err = %v", view, err)
		}
	})

	t.Run("subnet_and_user_agent_rules_filter_ddos_load", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		at := h.state().Clock.CurrentTime
		h.append(events.TrafficAttackStarted{AttackID: "attack", Kind: model.AttackDDoS, TargetPage: model.PageProductList,
			RequestsPerMinute: 600, LoadUnitsPerRequest: 1, SourceCIDR: "203.0.113.0/24", UserAgent: "CompromisedCamera/1.0",
			RegionCode: "ZZ", Resolution: model.AttackScaleOrExpiry, StartedAt: at, ExpectedEndAt: at.Add(time.Hour)})
		before := h.projection().Resources().UsedLoadUnits
		if before == 0 {
			t.Fatal("attack did not create background load")
		}
		upsertFirewall(h, firewallRule("deny-cameras", 1, model.FirewallDeny,
			model.FirewallMatch{UserAgent: &model.UserAgentMatch{Operator: model.UserAgentContains, Value: "CompromisedCamera/"}}))
		if got := h.projection().Resources().UsedLoadUnits; got != 0 {
			t.Fatalf("blocked attack load = %d", got)
		}
	})

	t.Run("blocking_attack_only_does_not_cause_downtime", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		upsertFirewall(h, firewallRule("deny-attacker", 1, model.FirewallDeny, model.FirewallMatch{SourceCIDR: "203.0.113.0/24"}))
		if h.state().FirewallUnavailable || !h.state().Site.UnavailableSince.IsZero() {
			t.Fatal("attack-only rule caused downtime")
		}
	})
}

func TestFirewallUpdatesExpiryAndReplay(t *testing.T) {
	t.Run("upsert_replace_delete_take_effect_immediately", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		rule := firewallRule("switch", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"})
		upsertFirewall(h, rule)
		requireRequestResult(t, openAs(h, browserRU), 403, model.FailureFirewallDenied, "switch")
		rule.Action = model.FirewallAllow
		upsertFirewall(h, rule)
		requireRequestResult(t, openAs(h, browserRU), 200, "", "switch")
		deleteFirewall(h, rule.ID)
		requireRequestResult(t, openAs(h, browserRU), 200, "", "")
	})

	t.Run("deleting_allow_exposes_later_deny", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		upsertFirewall(h, firewallRule("allow", 1, model.FirewallAllow, model.FirewallMatch{RegionCode: "RU"}))
		upsertFirewall(h, firewallRule("deny", 2, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"}))
		requireRequestResult(t, openAs(h, browserRU), 200, "", "allow")
		deleteFirewall(h, "allow")
		requireRequestResult(t, openAs(h, browserRU), 403, model.FailureFirewallDenied, "deny")
	})

	t.Run("expiry_stays_listed_and_closes_downtime_without_delete", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		rule := firewallRule("temporary", 1, model.FirewallDeny, model.FirewallMatch{SourceCIDR: "198.51.100.10/32"})
		rule.ExpiresAt = h.state().Clock.CurrentTime.Add(10 * time.Minute)
		upsertFirewall(h, rule)
		if !h.state().FirewallUnavailable {
			t.Fatal("legitimate control profile was not blocked")
		}
		items := h.execute(simulation.AdvanceTime{RealElapsed: 10 * time.Minute})
		if eventTypes(items)["FirewallRuleDeleted"] != 0 || eventTypes(items)["FirewallAvailabilityChanged"] != 1 {
			t.Fatalf("expiry events = %v", eventTypes(items))
		}
		state := h.state()
		if state.FirewallUnavailable || state.Site.DowntimeDuration != 10*time.Minute || len(h.projection().FirewallRules()) != 1 {
			t.Fatalf("state after expiry = unavailable:%t downtime:%s rules:%d", state.FirewallUnavailable, state.Site.DowntimeDuration, len(h.projection().FirewallRules()))
		}
	})

	t.Run("journal_replay_preserves_rules_and_request_decisions", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		upsertFirewall(h, firewallRule("deny", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"}))
		openAs(h, browserRU)
		rule := firewallRule("deny", 1, model.FirewallAllow, model.FirewallMatch{RegionCode: "RU"})
		upsertFirewall(h, rule)
		stateBefore := h.state()
		records, err := h.store.Load(h.ctx, h.runID)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := simulation.Rehydrate(records)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stateBefore.FirewallRules, replayed.FirewallRules) {
			t.Fatalf("replayed rules = %#v, want %#v", replayed.FirewallRules, stateBefore.FirewallRules)
		}
		logsView, err := simulation.NewProjection(records, replayed).Logs(simulation.LogsQuery{})
		if err != nil || len(logsView.Logs) != 1 || logsView.Logs[0].Entry.FirewallRuleID != "deny" || logsView.Logs[0].Entry.StatusCode != 403 {
			t.Fatalf("replayed log = %#v, err = %v", logsView, err)
		}
	})

	t.Run("runs_have_isolated_firewalls", func(t *testing.T) {
		first, second := newDatabaseHarness(t, model.InstanceDBSmall), newDatabaseHarness(t, model.InstanceDBSmall)
		upsertFirewall(first, firewallRule("deny", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"}))
		if len(second.projection().FirewallRules()) != 0 {
			t.Fatal("rule leaked into another run")
		}
		requireRequestResult(t, openAs(second, browserRU), 200, "", "")
	})

	t.Run("commands_work_while_site_stopped", func(t *testing.T) {
		h := newDatabaseHarness(t, model.InstanceDBSmall)
		h.stop()
		rule := firewallRule("during-migration", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "ZZ"})
		requireEventTypes(t, upsertFirewall(h, rule), "FirewallRuleUpserted")
		requireEventTypes(t, deleteFirewall(h, rule.ID), "FirewallRuleDeleted")
	})
}

func TestFirewallErrorsAreClassifiable(t *testing.T) {
	if !errors.Is(fmt.Errorf("wrapped: %w", simulation.ErrResourceNotFound), simulation.ErrResourceNotFound) {
		t.Fatal("resource not found must remain classifiable")
	}
}
