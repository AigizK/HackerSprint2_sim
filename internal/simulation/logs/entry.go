package logs

import (
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// Entry is a read-model record projected from page request events.
type Entry struct {
	Timestamp      time.Time
	RequestID      model.RequestID
	Source         model.RequestSource
	VisitorID      model.VisitorID
	Page           model.PageType
	ProductID      model.ProductID
	SourceIP       string
	UserAgent      string
	RegionCode     model.RegionCode
	FirewallRuleID model.FirewallRuleID
	StatusCode     int
	ErrorCode      model.RequestFailureCode
	Message        string
}
