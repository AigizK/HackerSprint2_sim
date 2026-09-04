package simulation

import (
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const (
	ProbabilityScale        uint32        = 1_000_000
	MinimumBackendInstances               = 1
	MinExplicitAdvance      time.Duration = 5 * time.Minute
)

type RunStatus string

const (
	RunNotCreated RunStatus = "not_created"
	RunRunning    RunStatus = "running"
	RunCompleted  RunStatus = "completed"
)

type ProductID = model.ProductID
type MessageID = model.MessageID

type ClockState struct {
	StartedAt   time.Time
	CurrentTime time.Time
	EndsAt      time.Time
}

type ProductState struct {
	ID                 ProductID
	Name               string
	Description        string
	Manufacturer       string
	PriceMinor         int64
	Available          bool
	Version            uint64
	ViewProbabilityPPM uint32
	AddedAt            time.Time
	UpdatedAt          time.Time
}

type InboxMessageState struct {
	ID          MessageID
	SenderEmail string
	SentAt      time.Time
	Subject     string
	Description string
}

type CostsState struct {
	Currency               string
	ServerCostMinor        int64
	BackupStorageCostMinor int64
}

type PageConfigState struct {
	Page         model.PageType
	LoadUnits    int64
	HoldDuration time.Duration
	BaseLatency  time.Duration
	ConfiguredAt time.Time
}

type InfrastructureConfigState struct {
	ServerProvisioningDuration time.Duration
	ConfiguredAt               time.Time
}

type ServerState struct {
	ID                model.ServerID
	Name              string
	CredentialID      model.CredentialID
	OperationID       model.OperationID
	Status            model.ServerLifecycleStatus
	InstanceType      model.InstanceType
	Role              model.ServerRole
	CapacityUnits     int64
	DiskBytes         int64
	SystemBytes       int64
	LogsBytes         int64
	ConnectionLimit   int
	ConnectionHold    time.Duration
	CostPerHourMinor  int64
	CostPerMonthMinor int64
	AccruedCostMinor  int64
	StartedAt         time.Time
	ReadyAt           time.Time
	ActivatedAt       time.Time
	BilledHours       int64
}

type ServerTypeState struct {
	InstanceType         model.InstanceType
	Role                 model.ServerRole
	DiskBytes            int64
	ConnectionLimit      int
	ConnectionHold       time.Duration
	BackendCapacityUnits int64
	CostPerHourMinor     int64
	CostPerMonthMinor    int64
}

type DatabaseState struct {
	ID                     model.DatabaseID
	ServerID               model.ServerID
	Name                   string
	DataBytes              int64
	LogsBytes              int64
	DataVersion            uint64
	Ready                  bool
	RestoredBackupID       model.BackupID
	RestoredFromDatabaseID model.DatabaseID
	AvailabilityReasons    map[model.DatabaseAvailabilityReason]struct{}
}

func (d DatabaseState) UsedBytes() int64 { return d.DataBytes + d.LogsBytes }

func (d DatabaseState) Available() bool { return d.Ready && len(d.AvailabilityReasons) == 0 }

type DatabaseConnectionState struct {
	RequestID  model.RequestID
	DatabaseID model.DatabaseID
	ServerID   model.ServerID
	OpenedAt   time.Time
	ReleasesAt time.Time
}

type DatabaseGrowthState struct {
	ID             model.GrowthID
	DatabaseID     model.DatabaseID
	DataDeltaBytes int64
	LogsDeltaBytes int64
	RequestedAt    time.Time
	Blocked        bool
}

type BackupState struct {
	ID                      model.BackupID
	DatabaseID              model.DatabaseID
	ServerID                model.ServerID
	OperationID             model.OperationID
	Status                  model.BackupLifecycleStatus
	DataBytes               int64
	DataVersion             uint64
	CreatedAt               time.Time
	CompletedAt             time.Time
	StorageCostPerHourMinor int64
	BilledHours             int64
	ErrorCode               string
	Message                 string
}

type SiteState struct {
	Status           model.SiteLifecycleStatus
	DatabaseID       model.DatabaseID
	OperationID      model.OperationID
	UnavailableSince time.Time
	DowntimeDuration time.Duration
}

func (s SiteState) DowntimeAt(now time.Time) time.Duration {
	result := s.DowntimeDuration
	if !s.UnavailableSince.IsZero() && now.After(s.UnavailableSince) {
		result += now.Sub(s.UnavailableSince)
	}
	return result
}

type CapacityAllocationState struct {
	RequestID  model.RequestID
	ServerID   model.ServerID
	LoadUnits  int64
	AcceptedAt time.Time
	ReleasesAt time.Time
}

type OperationState struct {
	ID          model.OperationID
	Kind        model.OperationKind
	Status      model.OperationLifecycleStatus
	QueuedAt    time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	ErrorCode   string
	Message     string
	ProgressPPM uint32
}

type ControlCommandReceiptState struct {
	CommandID     model.CommandID
	Command       string
	PayloadSHA256 string
	Params        []byte
	OperationID   model.OperationID
	StatusCode    int
	Result        []byte
	ErrorCode     string
	Message       string
	AcceptedAt    time.Time
	RecordedAt    time.Time
}

type ControlOperationResultState struct {
	OperationID model.OperationID
	CommandID   model.CommandID
	Command     string
	Result      []byte
	ErrorCode   string
	Message     string
	RecordedAt  time.Time
}

type ServerCredentialState struct {
	CredentialID           model.CredentialID
	ServerID               model.ServerID
	Version                uint64
	SupersedesCredentialID model.CredentialID
	ValidFrom              time.Time
	ExpiresAt              time.Time
	IssuedAt               time.Time
}

type VisitorState struct {
	ID          model.VisitorID
	ProductID   ProductID
	SourceIP    string
	UserAgent   string
	RegionCode  model.RegionCode
	Outcome     model.VisitorOutcome
	ArrivedAt   time.Time
	CompletedAt time.Time
}

type AttackState struct {
	ID                  model.AttackID
	Kind                model.AttackKind
	TargetPage          model.PageType
	RequestsPerMinute   int64
	LoadUnitsPerRequest int64
	SourceCIDR          string
	UserAgent           string
	RegionCode          model.RegionCode
	Resolution          model.AttackResolution
	ExpectedEndAt       time.Time
	StartedAt           time.Time
}

type PageRequestState struct {
	ID             model.RequestID
	Source         model.RequestSource
	VisitorID      model.VisitorID
	Page           model.PageType
	ProductID      ProductID
	SourceIP       string
	UserAgent      string
	RegionCode     model.RegionCode
	FirewallAction model.FirewallAction
	FirewallRuleID model.FirewallRuleID
	LoadUnits      int64
	Status         model.PageRequestStatus
	StatusCode     int
	ErrorCode      model.RequestFailureCode
	Message        string
	Latency        time.Duration
	ServerID       model.ServerID
	DatabaseID     model.DatabaseID
	ReleasesAt     time.Time
	StartedAt      time.Time
	CompletedAt    time.Time
}

type FirewallRuleState struct {
	Rule     model.FirewallRule
	Revision uint64
}

type State struct {
	RunID                    string
	Seed                     int64
	Status                   RunStatus
	EndReason                string
	Version                  uint64
	Clock                    ClockState
	Infrastructure           InfrastructureConfigState
	Products                 map[ProductID]ProductState
	InboxMessages            map[MessageID]InboxMessageState
	Pages                    map[model.PageType]PageConfigState
	Servers                  map[model.ServerID]ServerState
	ServerTypes              map[model.InstanceType]ServerTypeState
	Databases                map[model.DatabaseID]DatabaseState
	DatabaseConnections      map[model.RequestID]DatabaseConnectionState
	DatabaseConnectionCounts map[model.DatabaseID]int
	PendingDatabaseGrowth    map[model.GrowthID]DatabaseGrowthState
	Backups                  map[model.BackupID]BackupState
	Site                     SiteState
	Capacity                 map[model.RequestID]CapacityAllocationState
	UsedCapacityByServer     map[model.ServerID]int64
	CapacityIndexedAt        time.Time
	Operations               map[model.OperationID]OperationState
	Commands                 map[model.CommandID]model.OperationID
	CommandPayloads          map[model.CommandID]string
	ControlCommandReceipts   map[model.CommandID]ControlCommandReceiptState
	ControlOperationResults  map[model.OperationID]ControlOperationResultState
	ServerCredentials        map[model.CredentialID]ServerCredentialState
	CredentialRotationIDs    map[string]struct{}
	Requests                 map[model.RequestID]PageRequestState
	SeenRequests             map[model.RequestID]struct{}
	Visitors                 map[model.VisitorID]VisitorState
	SeenVisitors             map[model.VisitorID]struct{}
	ActiveAttacks            map[model.AttackID]AttackState
	ResolvedAttacks          map[model.AttackID]struct{}
	FirewallRules            map[model.FirewallRuleID]FirewallRuleState
	FirewallUnavailable      bool
	BackendUnavailable       bool
	Schedule                 events.EventSchedule
	ScheduleCursor           int
	Costs                    CostsState
}

func NewState() State {
	return State{
		Status:                   RunNotCreated,
		Products:                 make(map[ProductID]ProductState),
		InboxMessages:            make(map[MessageID]InboxMessageState),
		Pages:                    make(map[model.PageType]PageConfigState),
		Servers:                  make(map[model.ServerID]ServerState),
		ServerTypes:              make(map[model.InstanceType]ServerTypeState),
		Databases:                make(map[model.DatabaseID]DatabaseState),
		DatabaseConnections:      make(map[model.RequestID]DatabaseConnectionState),
		DatabaseConnectionCounts: make(map[model.DatabaseID]int),
		PendingDatabaseGrowth:    make(map[model.GrowthID]DatabaseGrowthState),
		Backups:                  make(map[model.BackupID]BackupState),
		Site:                     SiteState{Status: model.SiteRunning},
		Capacity:                 make(map[model.RequestID]CapacityAllocationState),
		UsedCapacityByServer:     make(map[model.ServerID]int64),
		Operations:               make(map[model.OperationID]OperationState),
		Commands:                 make(map[model.CommandID]model.OperationID),
		CommandPayloads:          make(map[model.CommandID]string),
		ControlCommandReceipts:   make(map[model.CommandID]ControlCommandReceiptState),
		ControlOperationResults:  make(map[model.OperationID]ControlOperationResultState),
		ServerCredentials:        make(map[model.CredentialID]ServerCredentialState),
		CredentialRotationIDs:    make(map[string]struct{}),
		Requests:                 make(map[model.RequestID]PageRequestState),
		SeenRequests:             make(map[model.RequestID]struct{}),
		Visitors:                 make(map[model.VisitorID]VisitorState),
		SeenVisitors:             make(map[model.VisitorID]struct{}),
		ActiveAttacks:            make(map[model.AttackID]AttackState),
		ResolvedAttacks:          make(map[model.AttackID]struct{}),
		FirewallRules:            make(map[model.FirewallRuleID]FirewallRuleState),
	}
}
