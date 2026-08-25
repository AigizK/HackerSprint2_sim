package main

import "time"

type clock struct {
	SimulationTime   time.Time `json:"simulation_time"`
	SimulationEndsAt time.Time `json:"simulation_ends_at"`
}

type startRunRequest struct {
	Seed         int64  `json:"seed"`
	AgentID      string `json:"agent_id"`
	AgentVersion string `json:"agent_version"`
	RequestID    string `json:"request_id"`
}

type startRunResponse struct {
	RunID          string    `json:"run_id"`
	Status         string    `json:"status"`
	SimulationTime time.Time `json:"simulation_time"`
	SimulationEnds time.Time `json:"simulation_ends_at"`
}

type overviewResponse struct {
	Clock  clock  `json:"clock"`
	Status string `json:"status"`
}

type requestLog struct {
	Timestamp time.Time `json:"timestamp"`
	Status    int       `json:"status"`
	Error     *string   `json:"error"`
	Message   *string   `json:"message"`
}

type logsResponse struct {
	Logs       []requestLog `json:"logs"`
	NextCursor *string      `json:"next_cursor"`
}

type deployment struct {
	DeploymentID string `json:"deployment_id"`
	Sequence     int    `json:"sequence"`
	Status       string `json:"status"`
}

type deploymentsResponse struct {
	Deployments []deployment `json:"deployments"`
}

type resourcesResponse struct {
	DesiredInstances int `json:"desired_instances"`
	ActiveInstances  int `json:"active_instances"`
	UsedLoadUnits    int `json:"used_load_units"`
}

type metricSnapshot struct {
	CapacityUtilization float64 `json:"capacity_utilization"`
}

type metricsResponse struct {
	Current metricSnapshot `json:"current"`
}

type economyResponse struct {
	SuccessfulPurchases uint64 `json:"successful_purchases"`
	RevenueMinor        int64  `json:"revenue_minor"`
	ServerCostMinor     int64  `json:"server_cost_minor"`
	DeploymentCostMinor int64  `json:"deployment_cost_minor"`
	BalanceMinor        int64  `json:"balance_minor"`
}

type advanceTimeResponse struct {
	Clock           clock `json:"clock"`
	ProcessedEvents int   `json:"processed_events"`
	NewLogs         int   `json:"new_logs"`
}

type apiErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
