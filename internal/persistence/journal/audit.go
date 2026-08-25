package journal

import "time"

// AgentRequestReceived is written before dispatching an HTTP request. Bodies
// are bytes rather than JSON so even malformed agent input remains auditable.
type AgentRequestReceived struct {
	RequestID  string              `json:"request_id"`
	CommandID  string              `json:"command_id,omitempty"`
	AgentID    string              `json:"agent_id,omitempty"`
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body,omitempty"`
	ReceivedAt time.Time           `json:"received_at"`
}

type AgentRequestCompleted struct {
	RequestID       string              `json:"request_id"`
	StatusCode      int                 `json:"status_code"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	ResponseBody    []byte              `json:"response_body,omitempty"`
	Error           string              `json:"error,omitempty"`
	CompletedAt     time.Time           `json:"completed_at"`
}

type AgentRequestAudit struct {
	Received  AgentRequestReceived   `json:"received"`
	Completed *AgentRequestCompleted `json:"completed,omitempty"`
}
