package httpapi

import "time"

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
	RunID          string    `json:"run_id"`
	Seed           int64     `json:"seed"`
	AgentID        string    `json:"agent_id"`
	AgentVersion   string    `json:"agent_version"`
	Status         string    `json:"status"`
	SimulationTime time.Time `json:"simulation_time"`
	SimulationEnds time.Time `json:"simulation_ends_at"`
}

type overviewResponse struct {
	Clock               clockResponse `json:"clock"`
	RunID               string        `json:"run_id"`
	Status              string        `json:"status"`
	SiteStatus          string        `json:"site_status"`
	CurrentDeploymentID *string       `json:"current_deployment_id"`
	ServerCount         int           `json:"server_count"`
	CapacityUtilization float64       `json:"capacity_utilization"`
	ErrorRate           float64       `json:"error_rate"`
	SuccessfulPurchases uint64        `json:"successful_purchases"`
	RevenueMinor        int64         `json:"revenue_minor"`
	ServerCostMinor     int64         `json:"server_cost_minor"`
	BalanceMinor        int64         `json:"balance_minor"`
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
	Responses500   uint64  `json:"responses_500"`
	ErrorRate      float64 `json:"error_rate"`
}

type metricSnapshotResponse struct {
	ServerCount         int                   `json:"server_count"`
	CapacityUnits       int64                 `json:"capacity_units"`
	UsedLoadUnits       int64                 `json:"used_load_units"`
	CapacityUtilization float64               `json:"capacity_utilization"`
	ActiveRequests      int                   `json:"active_requests"`
	Responses200        uint64                `json:"responses_200"`
	Responses500        uint64                `json:"responses_500"`
	ErrorRate           float64               `json:"error_rate"`
	LatencyP50MS        float64               `json:"latency_p50_ms,omitempty"`
	LatencyP95MS        float64               `json:"latency_p95_ms,omitempty"`
	SuccessfulPurchases uint64                `json:"successful_purchases"`
	RevenueMinor        int64                 `json:"revenue_minor"`
	LostRevenueMinor    int64                 `json:"lost_revenue_minor"`
	ServerCostMinor     int64                 `json:"server_cost_minor"`
	ByPage              []pageMetricsResponse `json:"by_page"`
}

type metricsResponse struct {
	Clock   clockResponse          `json:"clock"`
	Window  *timeWindowResponse    `json:"window,omitempty"`
	Current metricSnapshotResponse `json:"current"`
	Series  []metricPointResponse  `json:"series"`
}

type requestLogResponse struct {
	Timestamp time.Time `json:"timestamp"`
	RequestID string    `json:"request_id"`
	Source    string    `json:"source"`
	VisitorID *string   `json:"visitor_id,omitempty"`
	Page      string    `json:"page"`
	ProductID *string   `json:"product_id,omitempty"`
	Status    int       `json:"status"`
	LatencyMS float64   `json:"latency_ms,omitempty"`
	LoadUnits int64     `json:"load_units"`
	ServerID  *string   `json:"server_id,omitempty"`
	Error     *string   `json:"error,omitempty"`
	Message   *string   `json:"message,omitempty"`
}

type logsResponse struct {
	Clock      clockResponse        `json:"clock"`
	Logs       []requestLogResponse `json:"logs"`
	NextCursor *string              `json:"next_cursor"`
}

type serverResourceResponse struct {
	ServerID         string `json:"server_id"`
	Status           string `json:"status"`
	CapacityUnits    int64  `json:"capacity_units"`
	UsedLoadUnits    int64  `json:"used_load_units"`
	CostPerHourMinor int64  `json:"cost_per_hour_minor"`
}

type resourcesResponse struct {
	Clock                 clockResponse            `json:"clock"`
	DesiredInstances      int                      `json:"desired_instances"`
	ActiveInstances       int                      `json:"active_instances"`
	TotalCapacityUnits    int64                    `json:"total_capacity_units"`
	UsedLoadUnits         int64                    `json:"used_load_units"`
	TotalCostPerHourMinor int64                    `json:"total_cost_per_hour_minor"`
	Servers               []serverResourceResponse `json:"servers"`
}

type scaleBackendRequest struct {
	RequestID        string `json:"request_id"`
	DesiredInstances *int   `json:"desired_instances"`
}

type operationAcceptedResponse struct {
	Clock               clockResponse `json:"clock"`
	OperationID         string        `json:"operation_id"`
	Status              string        `json:"status"`
	EstimatedCompleteAt *time.Time    `json:"estimated_complete_at"`
}

type applyFixRequest struct {
	RequestID string `json:"request_id"`
	Message   string `json:"message"`
}

type applyFixResponse struct {
	Clock       clockResponse `json:"clock"`
	Applied     bool          `json:"applied"`
	FixedBug    *string       `json:"fixed_bug,omitempty"`
	FixedAttack *string       `json:"fixed_attack,omitempty"`
	Message     string        `json:"message,omitempty"`
}

type deploymentResponse struct {
	DeploymentID string  `json:"deployment_id"`
	Sequence     int     `json:"sequence"`
	Name         string  `json:"name"`
	Description  string  `json:"description,omitempty"`
	Status       string  `json:"status"`
	OperationID  *string `json:"operation_id,omitempty"`
}

type deploymentsResponse struct {
	Clock       clockResponse        `json:"clock"`
	Deployments []deploymentResponse `json:"deployments"`
}

type startDeploymentRequest struct {
	RequestID    string `json:"request_id"`
	DeploymentID string `json:"deployment_id"`
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
	Status      string         `json:"status"`
	Progress    float64        `json:"progress"`
	SubmittedAt time.Time      `json:"submitted_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	Error       *errorResponse `json:"error,omitempty"`
}

type probeRequest struct {
	RequestID string `json:"request_id"`
	Page      string `json:"page"`
	ProductID string `json:"product_id"`
}

type probeResponse struct {
	Clock     clockResponse `json:"clock"`
	RequestID string        `json:"request_id"`
	Page      string        `json:"page"`
	ProductID *string       `json:"product_id,omitempty"`
	Status    int           `json:"status"`
	LatencyMS float64       `json:"latency_ms"`
	LoadUnits int64         `json:"load_units"`
	Error     *string       `json:"error,omitempty"`
	Message   *string       `json:"message,omitempty"`
}

type economyResponse struct {
	Clock               clockResponse `json:"clock"`
	Currency            string        `json:"currency"`
	SuccessfulPurchases uint64        `json:"successful_purchases"`
	LostPurchases       uint64        `json:"lost_purchases"`
	RevenueMinor        int64         `json:"revenue_minor"`
	LostRevenueMinor    int64         `json:"lost_revenue_minor"`
	ServerCostMinor     int64         `json:"server_cost_minor"`
	DeploymentCostMinor int64         `json:"deployment_cost_minor"`
	BalanceMinor        int64         `json:"balance_minor"`
}

type advanceTimeRequest struct {
	RequestID       string `json:"request_id"`
	DurationSeconds *int64 `json:"duration_seconds"`
}

type advanceTimeResponse struct {
	Clock                    clockResponse `json:"clock"`
	PreviousSimulationTime   time.Time     `json:"previous_simulation_time"`
	RequestedDurationSeconds int64         `json:"requested_duration_seconds"`
	ProcessedEvents          int           `json:"processed_events"`
	NewLogs                  int           `json:"new_logs"`
	LogsCursor               *string       `json:"logs_cursor,omitempty"`
}
