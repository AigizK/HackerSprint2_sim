package httpapi

import (
	"encoding/json"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type clockResponse struct {
	SimulationTime        time.Time `json:"simulation_time"`
	SimulationEndsAt      time.Time `json:"simulation_ends_at"`
	RemainingSeconds      float64   `json:"remaining_seconds"`
	RealElapsedSeconds    float64   `json:"real_elapsed_seconds"`
	AppliedAdvanceSeconds float64   `json:"applied_advance_seconds"`
}

type startRunRequest struct {
	Seed         *int64 `json:"seed"`
	AgentID      string `json:"agent_id"`
	AgentVersion string `json:"agent_version"`
	RequestID    string `json:"request_id"`
}

type startRunResponse struct {
	RunID            string                   `json:"run_id"`
	Seed             int64                    `json:"seed"`
	AgentID          string                   `json:"agent_id"`
	AgentVersion     string                   `json:"agent_version"`
	Status           string                   `json:"status"`
	SimulationTime   time.Time                `json:"simulation_time"`
	SimulationEnds   time.Time                `json:"simulation_ends_at"`
	CommandsMarkdown string                   `json:"commands_markdown"`
	ControlPanelAuth controlPanelAuthResponse `json:"control_panel_auth"`
}

type controlPanelAuthResponse struct {
	Scheme       string `json:"scheme"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	Instructions string `json:"instructions"`
}

type overviewResponse struct {
	Costs               costsResponse        `json:"costs"`
	Availability        availabilityResponse `json:"availability"`
	Clock               clockResponse        `json:"clock"`
	RunID               string               `json:"run_id"`
	Status              string               `json:"status"`
	SiteStatus          string               `json:"site_status"`
	ServerCount         int                  `json:"server_count"`
	CapacityUtilization float64              `json:"capacity_utilization"`
	ErrorRate           float64              `json:"error_rate"`
}

type availabilityResponse struct {
	UptimeTarget     float64  `json:"uptime_target"`
	ObservedSeconds  float64  `json:"observed_seconds"`
	AvailableSeconds float64  `json:"available_seconds"`
	DowntimeSeconds  float64  `json:"downtime_seconds"`
	UptimeRatio      *float64 `json:"uptime_ratio"`
	SLOPassed        *bool    `json:"slo_passed"`
}

type timeWindowResponse struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type metricPointResponse struct {
	Timestamp time.Time `json:"timestamp"`
	Name      string    `json:"name"`
	Page      *string   `json:"page,omitempty"`
	Value     float64   `json:"value"`
}

type pageMetricsResponse struct {
	Page           string  `json:"page"`
	ActiveRequests int     `json:"active_requests"`
	UsedLoadUnits  int64   `json:"used_load_units"`
	Responses200   uint64  `json:"responses_200"`
	Responses403   uint64  `json:"responses_403"`
	Responses500   uint64  `json:"responses_500"`
	Responses503   uint64  `json:"responses_503"`
	ErrorRate      float64 `json:"error_rate"`
}

type metricSnapshotResponse struct {
	ServerCount               int                   `json:"server_count"`
	CapacityUnits             int64                 `json:"capacity_units"`
	UsedLoadUnits             int64                 `json:"used_load_units"`
	CapacityUtilization       float64               `json:"capacity_utilization"`
	ActiveRequests            int                   `json:"active_requests"`
	DatabaseActiveConnections int                   `json:"database_active_connections"`
	DatabaseConnectionLimit   int                   `json:"database_connection_limit"`
	DiskTotalBytes            int64                 `json:"disk_total_bytes"`
	DiskSystemBytes           int64                 `json:"disk_system_bytes"`
	DiskDatabaseBytes         int64                 `json:"disk_database_bytes"`
	DiskLogsBytes             int64                 `json:"disk_logs_bytes"`
	DiskFreeBytes             int64                 `json:"disk_free_bytes"`
	Responses200              uint64                `json:"responses_200"`
	Responses403              uint64                `json:"responses_403"`
	Responses500              uint64                `json:"responses_500"`
	Responses503              uint64                `json:"responses_503"`
	ErrorRate                 float64               `json:"error_rate"`
	LatencyP50MS              float64               `json:"latency_p50_ms,omitempty"`
	LatencyP95MS              float64               `json:"latency_p95_ms,omitempty"`
	ServerCostMinor           int64                 `json:"server_cost_minor"`
	BackupStorageCostMinor    int64                 `json:"backup_storage_cost_minor"`
	TotalCostMinor            int64                 `json:"total_cost_minor"`
	CurrentCostPerHourMinor   int64                 `json:"current_cost_per_hour_minor"`
	ObservedSeconds           float64               `json:"observed_seconds"`
	AvailableSeconds          float64               `json:"available_seconds"`
	DowntimeSeconds           float64               `json:"downtime_seconds"`
	UptimeRatio               *float64              `json:"uptime_ratio"`
	ByPage                    []pageMetricsResponse `json:"by_page"`
}

type metricsResponse struct {
	Clock   clockResponse          `json:"clock"`
	Window  *timeWindowResponse    `json:"window,omitempty"`
	Current metricSnapshotResponse `json:"current"`
	Series  []metricPointResponse  `json:"series"`
}

type requestLogResponse struct {
	Timestamp      time.Time `json:"timestamp"`
	RequestID      string    `json:"request_id"`
	Source         string    `json:"source"`
	VisitorID      *string   `json:"visitor_id,omitempty"`
	Page           string    `json:"page"`
	ProductID      *string   `json:"product_id,omitempty"`
	SourceIP       string    `json:"source_ip"`
	UserAgent      string    `json:"user_agent"`
	RegionCode     string    `json:"region_code"`
	FirewallRuleID *string   `json:"firewall_rule_id"`
	Status         int       `json:"status"`
	LatencyMS      float64   `json:"latency_ms,omitempty"`
	LoadUnits      int64     `json:"load_units"`
	ServerID       *string   `json:"server_id,omitempty"`
	Error          *string   `json:"error,omitempty"`
	Message        *string   `json:"message,omitempty"`
}

type logsResponse struct {
	Clock      clockResponse        `json:"clock"`
	Logs       []requestLogResponse `json:"logs"`
	NextCursor *string              `json:"next_cursor"`
}

type inboxMessageResponse struct {
	MessageID   string    `json:"message_id"`
	SenderEmail string    `json:"sender_email"`
	SentAt      time.Time `json:"sent_at"`
	Subject     string    `json:"subject"`
	Description string    `json:"description"`
}

type inboxResponse struct {
	Clock      clockResponse          `json:"clock"`
	Messages   []inboxMessageResponse `json:"messages"`
	NextCursor *string                `json:"next_cursor"`
}

type serverResourceResponse struct {
	ServerID         string            `json:"server_id"`
	Name             string            `json:"name"`
	Role             string            `json:"role"`
	InstanceType     string            `json:"instance_type"`
	Status           string            `json:"status"`
	CapacityUnits    int64             `json:"capacity_units"`
	UsedLoadUnits    int64             `json:"used_load_units"`
	CostPerHourMinor int64             `json:"cost_per_hour_minor"`
	Disk             diskUsageResponse `json:"disk"`
	DatabaseIDs      []string          `json:"database_ids"`
	CredentialID     string            `json:"credential_id"`
}

type diskUsageResponse struct {
	ServerID       string `json:"server_id"`
	TotalBytes     int64  `json:"total_bytes"`
	SystemBytes    int64  `json:"system_bytes"`
	DatabaseBytes  int64  `json:"database_bytes"`
	LogsBytes      int64  `json:"logs_bytes"`
	UsedBytes      int64  `json:"used_bytes"`
	FreeBytes      int64  `json:"free_bytes"`
	CleanableBytes int64  `json:"cleanable_bytes"`
}

type resourcesResponse struct {
	Clock                 clockResponse            `json:"clock"`
	ActiveInstances       int                      `json:"active_instances"`
	TotalCapacityUnits    int64                    `json:"total_capacity_units"`
	UsedLoadUnits         int64                    `json:"used_load_units"`
	TotalCostPerHourMinor int64                    `json:"total_cost_per_hour_minor"`
	Servers               []serverResourceResponse `json:"servers"`
}

type errorResponse struct {
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type operationResponse struct {
	Clock       clockResponse  `json:"clock"`
	OperationID string         `json:"operation_id"`
	Type        string         `json:"type"`
	Command     string         `json:"command"`
	RequestID   string         `json:"request_id"`
	Status      string         `json:"status"`
	Progress    float64        `json:"progress"`
	SubmittedAt time.Time      `json:"submitted_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	Result      any            `json:"result"`
	Error       *errorResponse `json:"error,omitempty"`
}

type probeRequest struct {
	RequestID string `json:"request_id"`
	Page      string `json:"page"`
	ProductID string `json:"product_id"`
}

type probeResponse struct {
	Clock          clockResponse `json:"clock"`
	RequestID      string        `json:"request_id"`
	Page           string        `json:"page"`
	ProductID      *string       `json:"product_id,omitempty"`
	SourceIP       string        `json:"source_ip"`
	UserAgent      string        `json:"user_agent"`
	RegionCode     string        `json:"region_code"`
	FirewallRuleID *string       `json:"firewall_rule_id"`
	Status         int           `json:"status"`
	LatencyMS      float64       `json:"latency_ms"`
	LoadUnits      int64         `json:"load_units"`
	Error          *string       `json:"error,omitempty"`
	Message        *string       `json:"message,omitempty"`
}

type advanceTimeRequest struct {
	RequestID       string                    `json:"request_id"`
	DurationSeconds *int64                    `json:"duration_seconds"`
	StopWhen        *advanceTimeStopCondition `json:"stop_when,omitempty"`
}

type advanceTimeStopCondition struct {
	NewLogErrors *int `json:"new_log_errors"`
}

type advanceTimeResponse struct {
	Clock                    clockResponse `json:"clock"`
	PreviousSimulationTime   time.Time     `json:"previous_simulation_time"`
	RequestedDurationSeconds int64         `json:"requested_duration_seconds"`
	ProcessedEvents          int           `json:"processed_events"`
	NewLogs                  int           `json:"new_logs"`
	LogsCursor               *string       `json:"logs_cursor,omitempty"`
	StopReason               string        `json:"stop_reason"`
}

type controlCommandRequest struct {
	RequestID  string             `json:"request_id"`
	Command    string             `json:"command"`
	Params     json.RawMessage    `json:"params"`
	TargetAuth *targetAuthRequest `json:"target_auth,omitempty"`
}

type targetAuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type firewallDeleteParams struct {
	RuleID model.FirewallRuleID `json:"rule_id"`
}

type firewallRuleResponse struct {
	model.FirewallRule
	Revision uint64 `json:"revision"`
}

type firewallRulesListResultResponse struct {
	Rules []firewallRuleResponse `json:"rules"`
}

type firewallRuleResultResponse struct {
	Rule firewallRuleResponse `json:"rule"`
}

type firewallRuleDeletedResultResponse struct {
	RuleID  model.FirewallRuleID `json:"rule_id"`
	Deleted bool                 `json:"deleted"`
}

type controlCommandResponse struct {
	Clock     clockResponse `json:"clock"`
	RequestID string        `json:"request_id"`
	Command   string        `json:"command"`
	Result    any           `json:"result"`
}

type controlOperationAcceptedResponse struct {
	Clock               clockResponse `json:"clock"`
	RequestID           string        `json:"request_id"`
	Command             string        `json:"command"`
	OperationID         string        `json:"operation_id"`
	Status              string        `json:"status"`
	EstimatedCompleteAt *time.Time    `json:"estimated_complete_at,omitempty"`
}

type commandDefinitionResponse struct {
	Command            string         `json:"command"`
	Description        string         `json:"description"`
	ParamsSchema       map[string]any `json:"params_schema"`
	ResultSchema       map[string]any `json:"result_schema"`
	TargetAuthRequired bool           `json:"target_auth_required"`
	Execution          string         `json:"execution"`
}

type controlCommandsResponse struct {
	Clock    clockResponse               `json:"clock"`
	Commands []commandDefinitionResponse `json:"commands"`
}

type credentialRecordResponse struct {
	CredentialID string    `json:"credential_id"`
	ResourceID   string    `json:"resource_id"`
	Version      uint64    `json:"version"`
	Username     string    `json:"username"`
	Password     string    `json:"password"`
	ValidFrom    time.Time `json:"valid_from"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type credentialsResponse struct {
	Clock      clockResponse            `json:"clock"`
	Credential credentialRecordResponse `json:"credential"`
}
