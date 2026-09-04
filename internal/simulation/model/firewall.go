package model

import "time"

type FirewallRuleID string
type FirewallAction string
type UserAgentOperator string
type RegionCode string

const (
	FirewallAllow FirewallAction = "allow"
	FirewallDeny  FirewallAction = "deny"

	UserAgentEquals   UserAgentOperator = "equals"
	UserAgentContains UserAgentOperator = "contains"
)

type UserAgentMatch struct {
	Operator UserAgentOperator `json:"operator"`
	Value    string            `json:"value"`
}

type FirewallMatch struct {
	SourceCIDR string          `json:"source_cidr,omitempty"`
	RegionCode RegionCode      `json:"region_code,omitempty"`
	UserAgent  *UserAgentMatch `json:"user_agent,omitempty"`
}

type FirewallRule struct {
	ID        FirewallRuleID `json:"rule_id"`
	Priority  int            `json:"priority"`
	Action    FirewallAction `json:"action"`
	Match     FirewallMatch  `json:"match"`
	Enabled   bool           `json:"enabled"`
	ExpiresAt time.Time      `json:"expires_at,omitempty"`
}

// ClientProfile is the part of a simulated site request visible to the
// firewall. RegionCode is assigned by the world generator; no external GeoIP
// service participates in replay.
type ClientProfile struct {
	SourceIP   string     `json:"source_ip"`
	UserAgent  string     `json:"user_agent"`
	RegionCode RegionCode `json:"region_code"`
}
