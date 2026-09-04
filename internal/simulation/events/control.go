package events

import (
	"encoding/json"
	"time"
)

// ControlCommandAccepted is the durable idempotency receipt for a validated
// control-panel command. PayloadSHA256 covers only command and params: panel
// Authorization and target_auth secrets must never enter the event journal.
type ControlCommandAccepted struct {
	CommandID     CommandID
	Command       string
	PayloadSHA256 string
	Params        json.RawMessage
	OperationID   OperationID
	AcceptedAt    time.Time
}

func (ControlCommandAccepted) EventType() string { return "ControlCommandAccepted" }

// ControlCommandResponseRecorded preserves the exact first HTTP outcome for an
// accepted request_id. Result contains only the public result object. For an
// error Result is nil and ErrorCode/Message describe the saved response.
type ControlCommandResponseRecorded struct {
	CommandID   CommandID
	Command     string
	StatusCode  int
	OperationID OperationID
	Result      json.RawMessage
	ErrorCode   string
	Message     string
	RecordedAt  time.Time
}

func (ControlCommandResponseRecorded) EventType() string {
	return "ControlCommandResponseRecorded"
}

// ControlOperationResultRecorded keeps the typed terminal result returned by
// GET /operations/{operation_id}. Lifecycle and progress remain represented by
// OperationQueued/Started/Progressed/Succeeded/Failed.
type ControlOperationResultRecorded struct {
	OperationID OperationID
	CommandID   CommandID
	Command     string
	Result      json.RawMessage
	ErrorCode   string
	Message     string
	RecordedAt  time.Time
}

func (ControlOperationResultRecorded) EventType() string {
	return "ControlOperationResultRecorded"
}

// ServerCredentialIssued exposes only safe metadata to the event stream. The
// username and password are generated and persisted by a separate secret store.
// A non-empty SupersedesCredentialID makes this issuance a rotation.
type ServerCredentialIssued struct {
	CredentialID           CredentialID
	ServerID               ServerID
	Version                uint64
	SupersedesCredentialID CredentialID
	ValidFrom              time.Time
	ExpiresAt              time.Time
	IssuedAt               time.Time
}

func (ServerCredentialIssued) EventType() string { return "ServerCredentialIssued" }

// ServerCredentialRotationRequested is a generated-world trigger. When it is
// reached, the aggregate issues the next run-scoped credential version and a
// matching inbox message at the same simulation instant.
type ServerCredentialRotationRequested struct {
	RotationID  string
	ServerID    ServerID
	RequestedAt time.Time
}

func (ServerCredentialRotationRequested) EventType() string {
	return "ServerCredentialRotationRequested"
}
