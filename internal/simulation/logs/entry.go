package logs

import (
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// Entry is a read-model record projected from page request events.
// The projection itself is intentionally not implemented yet.
type Entry struct {
	Timestamp  time.Time
	RequestID  model.RequestID
	Source     model.RequestSource
	VisitorID  model.VisitorID
	Page       model.PageType
	ProductID  model.ProductID
	StatusCode int
	ErrorCode  model.RequestFailureCode
	Message    string
}
