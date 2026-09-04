package simulation

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

var (
	resourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	regionCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)
)

type firewallDecision struct {
	Action   model.FirewallAction
	RuleID   model.FirewallRuleID
	Revision uint64
}

func validateAndNormalizeFirewallRule(rule model.FirewallRule, now time.Time) (model.FirewallRule, error) {
	if !resourceIDPattern.MatchString(string(rule.ID)) || rule.Priority < 0 ||
		(rule.Action != model.FirewallAllow && rule.Action != model.FirewallDeny) {
		return model.FirewallRule{}, fmt.Errorf("%w: invalid firewall rule envelope", ErrInvalidCommand)
	}
	if rule.Match.SourceCIDR == "" && rule.Match.RegionCode == "" && rule.Match.UserAgent == nil {
		return model.FirewallRule{}, fmt.Errorf("%w: firewall match must not be empty", ErrInvalidCommand)
	}
	if rule.Match.SourceCIDR != "" {
		prefix, err := netip.ParsePrefix(rule.Match.SourceCIDR)
		if err != nil || prefix != prefix.Masked() {
			return model.FirewallRule{}, fmt.Errorf("%w: invalid canonical source_cidr", ErrInvalidCommand)
		}
		rule.Match.SourceCIDR = prefix.String()
	}
	if rule.Match.RegionCode != "" && !regionCodePattern.MatchString(string(rule.Match.RegionCode)) {
		return model.FirewallRule{}, fmt.Errorf("%w: invalid region_code", ErrInvalidCommand)
	}
	if ua := rule.Match.UserAgent; ua != nil {
		if (ua.Operator != model.UserAgentEquals && ua.Operator != model.UserAgentContains) ||
			ua.Value == "" || utf8.RuneCountInString(ua.Value) > 2048 {
			return model.FirewallRule{}, fmt.Errorf("%w: invalid user_agent match", ErrInvalidCommand)
		}
		copyMatch := *ua
		rule.Match.UserAgent = &copyMatch
	}
	if !rule.ExpiresAt.IsZero() && !rule.ExpiresAt.After(now) {
		return model.FirewallRule{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidCommand)
	}
	return rule, nil
}

func validateClientProfile(profile model.ClientProfile) error {
	if _, err := netip.ParseAddr(profile.SourceIP); err != nil || profile.UserAgent == "" || utf8.RuneCountInString(profile.UserAgent) > 2048 ||
		!regionCodePattern.MatchString(string(profile.RegionCode)) {
		return fmt.Errorf("%w: invalid client profile", ErrInvalidCommand)
	}
	return nil
}

func evaluateFirewall(state State, profile model.ClientProfile, at time.Time) firewallDecision {
	rules := make([]FirewallRuleState, 0, len(state.FirewallRules))
	for _, rule := range state.FirewallRules {
		if !rule.Rule.Enabled || (!rule.Rule.ExpiresAt.IsZero() && !at.Before(rule.Rule.ExpiresAt)) {
			continue
		}
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Rule.Priority == rules[j].Rule.Priority {
			return rules[i].Rule.ID < rules[j].Rule.ID
		}
		return rules[i].Rule.Priority < rules[j].Rule.Priority
	})
	for _, rule := range rules {
		if firewallRuleMatches(rule.Rule, profile) {
			return firewallDecision{Action: rule.Rule.Action, RuleID: rule.Rule.ID, Revision: rule.Revision}
		}
	}
	return firewallDecision{Action: model.FirewallAllow}
}

func firewallRuleMatches(rule model.FirewallRule, profile model.ClientProfile) bool {
	if rule.Match.SourceCIDR != "" {
		prefix, prefixErr := netip.ParsePrefix(rule.Match.SourceCIDR)
		address, addressErr := netip.ParseAddr(profile.SourceIP)
		if prefixErr != nil || addressErr != nil || !prefix.Contains(address) {
			return false
		}
	}
	if rule.Match.RegionCode != "" && rule.Match.RegionCode != profile.RegionCode {
		return false
	}
	if ua := rule.Match.UserAgent; ua != nil {
		switch ua.Operator {
		case model.UserAgentEquals:
			if profile.UserAgent != ua.Value {
				return false
			}
		case model.UserAgentContains:
			if !strings.Contains(profile.UserAgent, ua.Value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ProbeClientProfile is stable for a seed, so an agent can reproduce probe
// filtering and event-stream replay never needs external client data.
func ProbeClientProfile(seed int64) model.ClientProfile {
	return deterministicClientProfile(seed, "probe")
}

// VisitorClientProfile returns the stable fallback used when a caller does not
// supply an explicit simulated client profile.
func VisitorClientProfile(seed int64, visitorID model.VisitorID) model.ClientProfile {
	return deterministicClientProfile(seed, string(visitorID))
}

func deterministicClientProfile(seed int64, key string) model.ClientProfile {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:client:%s", seed, key)))
	regions := [...]model.RegionCode{"RU", "US", "KZ", "DE", "ZZ"}
	agents := [...]string{"Mozilla/5.0 SimBrowser/1.0", "MobileApp/2.0", "WebCamera/1.0", "HealthProbe/1.0"}
	return model.ClientProfile{
		SourceIP:   fmt.Sprintf("198.18.%d.%d", digest[0], 1+int(digest[1])%254),
		UserAgent:  agents[int(digest[2])%len(agents)],
		RegionCode: regions[int(digest[3])%len(regions)],
	}
}

func requestClientProfile(state State, command OpenPage) (model.ClientProfile, error) {
	profile := model.ClientProfile{SourceIP: command.SourceIP, UserAgent: command.UserAgent, RegionCode: command.RegionCode}
	if profile.SourceIP == "" && profile.UserAgent == "" && profile.RegionCode == "" {
		if command.VisitorID == "__probe__" {
			return ProbeClientProfile(state.Seed), nil
		}
		if visitor, exists := state.Visitors[command.VisitorID]; exists && visitor.SourceIP != "" {
			return model.ClientProfile{SourceIP: visitor.SourceIP, UserAgent: visitor.UserAgent, RegionCode: visitor.RegionCode}, nil
		}
		return VisitorClientProfile(state.Seed, command.VisitorID), nil
	}
	return profile, validateClientProfile(profile)
}

func uptimeControlProfiles(seed int64) []model.ClientProfile {
	// Two independent control profiles avoid making uptime depend on a single
	// region or IP family. Blocking either one makes the service unavailable.
	return []model.ClientProfile{
		{SourceIP: "198.51.100.10", UserAgent: "UptimeControl/1.0", RegionCode: "RU"},
		{SourceIP: "2001:db8:ffff::10", UserAgent: "UptimeControl/1.0", RegionCode: "US"},
	}
}

func firewallBlockingRuleIDs(state State, at time.Time) []model.FirewallRuleID {
	seen := make(map[model.FirewallRuleID]struct{})
	for _, profile := range uptimeControlProfiles(state.Seed) {
		decision := evaluateFirewall(state, profile, at)
		if decision.Action == model.FirewallDeny {
			seen[decision.RuleID] = struct{}{}
		}
	}
	result := make([]model.FirewallRuleID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func attackClientProfile(attack AttackState) (model.ClientProfile, bool) {
	if attack.SourceCIDR == "" || attack.UserAgent == "" || attack.RegionCode == "" {
		return model.ClientProfile{}, false
	}
	prefix, err := netip.ParsePrefix(attack.SourceCIDR)
	if err != nil {
		return model.ClientProfile{}, false
	}
	return model.ClientProfile{SourceIP: prefix.Addr().String(), UserAgent: attack.UserAgent, RegionCode: attack.RegionCode}, true
}

func stableFirewallPayload(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("firewall:%x", binary.BigEndian.Uint64(digest[:8]))
}
