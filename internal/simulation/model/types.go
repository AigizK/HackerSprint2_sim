package model

type ProductID string
type PurchaseID string
type VisitorID string
type RequestID string
type ServerID string
type BugID string
type DeploymentID string
type OperationID string
type ProviderID string
type AttackID string
type CommandID string

type PageType string

const (
	PageProductList PageType = "product_list"
	PageProduct     PageType = "product_page"
	PagePurchase    PageType = "purchase"
)

type RequestSource string

const (
	RequestSourceVisitor RequestSource = "visitor"
	RequestSourceProbe   RequestSource = "probe"
)

type RequestFailureCode string

const (
	FailureServerCapacityExceeded RequestFailureCode = "SERVER_CAPACITY_EXCEEDED"
	FailurePageBug                RequestFailureCode = "PAGE_BUG"
	FailureDeployment             RequestFailureCode = "DEPLOYMENT_ERROR"
	FailureExternalProvider       RequestFailureCode = "EXTERNAL_PROVIDER_ERROR"
)

type PageRequestStatus string

const (
	PageRequestInProgress PageRequestStatus = "in_progress"
	PageRequestSucceeded  PageRequestStatus = "succeeded"
	PageRequestFailed     PageRequestStatus = "failed"
)

type BugStatus string

const (
	BugActive BugStatus = "active"
	BugFixed  BugStatus = "fixed"
)

type FixSubmissionStatus string

const (
	FixSubmitted FixSubmissionStatus = "submitted"
	FixAccepted  FixSubmissionStatus = "accepted"
	FixRejected  FixSubmissionStatus = "rejected"
)

type RevenueLossReason string

const (
	RevenueLostToCapacity      RevenueLossReason = "server_capacity_exceeded"
	RevenueLostToPageBug       RevenueLossReason = "page_bug"
	RevenueLostToDeployment    RevenueLossReason = "deployment_error"
	RevenueLostToProvider      RevenueLossReason = "external_provider_error"
	RevenueLostToAbandonment   RevenueLossReason = "visitor_abandoned"
	RevenueLostToSimulationEnd RevenueLossReason = "simulation_ended"
)

type VisitorOutcome string

const (
	VisitorLeftAfterProductList VisitorOutcome = "left_after_product_list"
	VisitorLeftAfterProductPage VisitorOutcome = "left_after_product_page"
	VisitorLeftAfterPageError   VisitorOutcome = "left_after_page_error"
	VisitorPurchased            VisitorOutcome = "purchased"
	VisitorSimulationEnded      VisitorOutcome = "simulation_ended"
)

type OperationKind string

const (
	OperationScaleBackend OperationKind = "scale_backend"
	OperationDeployment   OperationKind = "deployment"
)

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

type DeploymentLifecycleStatus string

const (
	DeploymentStatusLocked    DeploymentLifecycleStatus = "locked"
	DeploymentStatusAvailable DeploymentLifecycleStatus = "available"
	DeploymentStatusQueued    DeploymentLifecycleStatus = "queued"
	DeploymentStatusRunning   DeploymentLifecycleStatus = "running"
	DeploymentStatusSucceeded DeploymentLifecycleStatus = "succeeded"
	DeploymentStatusFailed    DeploymentLifecycleStatus = "failed"
)

type AttackKind string

const (
	AttackDDoS       AttackKind = "ddos"
	AttackBruteForce AttackKind = "brute_force"
)
