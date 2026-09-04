package simulation

import (
	"encoding/json"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type Command interface {
	commandType() string
}

type CreateWorld struct {
	Seed      int64
	StartedAt time.Time
	EndsAt    time.Time
}

func (CreateWorld) commandType() string { return "CreateWorld" }

type AddProduct struct {
	ProductID          ProductID
	Name               string
	PriceMinor         int64
	ViewProbabilityPPM uint32
}

func (AddProduct) commandType() string { return "AddProduct" }

type AdvanceTime struct {
	CommandID         model.CommandID
	RealElapsed       time.Duration
	RequestedDuration time.Duration
	StopOnLogError    bool
	LogErrorCodes     []model.RequestFailureCode
	stopAt            time.Time
	previewLogErrors  bool
}

func (AdvanceTime) commandType() string { return "AdvanceTime" }

// OpenPage executes a visitor request against the current page and capacity state.
type OpenPage struct {
	RequestID  model.RequestID
	VisitorID  model.VisitorID
	Page       model.PageType
	ProductID  model.ProductID
	SourceIP   string
	UserAgent  string
	RegionCode model.RegionCode
}

func (OpenPage) commandType() string { return "OpenPage" }

type AddServer struct {
	CommandID        model.CommandID
	OperationID      model.OperationID
	ServerID         model.ServerID
	CapacityUnits    int64
	CostPerHourMinor int64
}

func (AddServer) commandType() string { return "AddServer" }

type RemoveServer struct {
	CommandID   model.CommandID
	OperationID model.OperationID
	ServerID    model.ServerID
}

func (RemoveServer) commandType() string { return "RemoveServer" }

type ProbePage struct {
	RequestID model.RequestID
	Page      model.PageType
	ProductID model.ProductID
}

func (ProbePage) commandType() string { return "ProbePage" }

type SimulateVisitor struct {
	VisitorID model.VisitorID
}

func (SimulateVisitor) commandType() string { return "SimulateVisitor" }

type SynchronizeRealTime struct {
	CommandID   model.CommandID
	RealElapsed time.Duration
}

func (SynchronizeRealTime) commandType() string { return "SynchronizeRealTime" }

// ConfigureServerCatalog defines the four infrastructure SKUs for a run.
// XBytes is the db.small disk size; medium and large use 2X and 4X.
type ConfigureServerCatalog struct {
	XBytes                   int64
	BackendCapacityUnits     int64
	BackendCostPerMonthMinor int64
}

func (ConfigureServerCatalog) commandType() string { return "ConfigureServerCatalog" }

type AddTypedServer struct {
	CommandID    model.CommandID
	OperationID  model.OperationID
	ServerID     model.ServerID
	Name         string
	CredentialID model.CredentialID
	InstanceType model.InstanceType
}

func (AddTypedServer) commandType() string { return "AddTypedServer" }

type CreateDatabase struct {
	CommandID  model.CommandID
	DatabaseID model.DatabaseID
	ServerID   model.ServerID
	Name       string
}

func (CreateDatabase) commandType() string { return "CreateDatabase" }

type GrowDatabase struct {
	CommandID      model.CommandID
	GrowthID       model.GrowthID
	DataDeltaBytes int64
	LogsDeltaBytes int64
}

func (GrowDatabase) commandType() string { return "GrowDatabase" }

type CleanupDatabaseLogs struct {
	CommandID model.CommandID
	ServerID  model.ServerID
}

func (CleanupDatabaseLogs) commandType() string { return "CleanupDatabaseLogs" }

type BackupDatabase struct {
	CommandID   model.CommandID
	OperationID model.OperationID
	BackupID    model.BackupID
	DatabaseID  model.DatabaseID
	Fail        bool
}

func (BackupDatabase) commandType() string { return "BackupDatabase" }

type RestoreDatabase struct {
	CommandID   model.CommandID
	OperationID model.OperationID
	BackupID    model.BackupID
	DatabaseID  model.DatabaseID
	Fail        bool
}

func (RestoreDatabase) commandType() string { return "RestoreDatabase" }

type StopSite struct {
	CommandID   model.CommandID
	OperationID model.OperationID
}

func (StopSite) commandType() string { return "StopSite" }

type StartSite struct{ CommandID model.CommandID }

func (StartSite) commandType() string { return "StartSite" }

type SetSiteDatabase struct {
	CommandID                 model.CommandID
	DatabaseID                model.DatabaseID
	ExpectedCurrentDatabaseID model.DatabaseID
}

func (SetSiteDatabase) commandType() string { return "SetSiteDatabase" }

type UpsertFirewallRule struct {
	CommandID model.CommandID
	Rule      model.FirewallRule
}

func (UpsertFirewallRule) commandType() string { return "UpsertFirewallRule" }

type DeleteFirewallRule struct {
	CommandID model.CommandID
	RuleID    model.FirewallRuleID
}

func (DeleteFirewallRule) commandType() string { return "DeleteFirewallRule" }

type RecordControlCommandResponse struct {
	CommandID     model.CommandID
	Command       string
	PayloadSHA256 string
	Params        json.RawMessage
	StatusCode    int
	OperationID   model.OperationID
	Result        json.RawMessage
	ErrorCode     string
	Message       string
}

func (RecordControlCommandResponse) commandType() string { return "RecordControlCommandResponse" }

type RecordControlOperationResult struct {
	OperationID model.OperationID
	CommandID   model.CommandID
	Command     string
	Result      json.RawMessage
	ErrorCode   string
	Message     string
}

func (RecordControlOperationResult) commandType() string { return "RecordControlOperationResult" }

type IssueServerCredential struct {
	CredentialID           model.CredentialID
	ServerID               model.ServerID
	Version                uint64
	SupersedesCredentialID model.CredentialID
	ValidFrom              time.Time
	ExpiresAt              time.Time
	MessageID              model.MessageID
}

func (IssueServerCredential) commandType() string { return "IssueServerCredential" }
