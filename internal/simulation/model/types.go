package model

type ProductID string
type MessageID string
type VisitorID string
type RequestID string
type ServerID string
type OperationID string
type AttackID string
type CommandID string
type CredentialID string

type PageType string

const (
	PageProductList PageType = "product_list"
	PageProduct     PageType = "product_page"
)

type RequestSource string

const (
	RequestSourceVisitor RequestSource = "visitor"
	RequestSourceProbe   RequestSource = "probe"
)

type RequestFailureCode string

const (
	FailureServerCapacityExceeded RequestFailureCode = "SERVER_CAPACITY_EXCEEDED"
	FailureDBConnectionLimit      RequestFailureCode = "DB_CONNECTION_LIMIT_EXCEEDED"
	FailureDiskFull               RequestFailureCode = "DISK_FULL"
	FailureSiteUnavailable        RequestFailureCode = "SITE_UNAVAILABLE"
	FailureDatabaseUnavailable    RequestFailureCode = "DB_UNAVAILABLE"
	FailureFirewallDenied         RequestFailureCode = "FIREWALL_DENIED"
)

type PageRequestStatus string

const (
	PageRequestInProgress PageRequestStatus = "in_progress"
	PageRequestSucceeded  PageRequestStatus = "succeeded"
	PageRequestFailed     PageRequestStatus = "failed"
)

type VisitorOutcome string

const (
	VisitorLeftAfterProductList VisitorOutcome = "left_after_product_list"
	VisitorLeftAfterProductPage VisitorOutcome = "left_after_product_page"
	VisitorLeftAfterPageError   VisitorOutcome = "left_after_page_error"
	VisitorSimulationEnded      VisitorOutcome = "simulation_ended"
)

type OperationKind string

const OperationControlCommand OperationKind = "control_command"

type OperationLifecycleStatus string

const (
	OperationStatusQueued    OperationLifecycleStatus = "queued"
	OperationStatusRunning   OperationLifecycleStatus = "running"
	OperationStatusSucceeded OperationLifecycleStatus = "succeeded"
	OperationStatusFailed    OperationLifecycleStatus = "failed"
)

type ServerLifecycleStatus string

const (
	ServerProvisioning ServerLifecycleStatus = "provisioning"
	ServerActive       ServerLifecycleStatus = "active"
	ServerDraining     ServerLifecycleStatus = "draining"
	ServerStopped      ServerLifecycleStatus = "stopped"
	ServerFailed       ServerLifecycleStatus = "failed"
)

type AttackKind string

const (
	AttackDDoS       AttackKind = "ddos"
	AttackBruteForce AttackKind = "brute_force"
)

type AttackResolution string

const (
	AttackScaleOrExpiry AttackResolution = "scale_or_expiry"
)
