package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

const (
	pageBugID      model.BugID = "bug-product-page"
	correctFixText             = "FIX-PAGE-7F92A"
)

func TestCorrectFixRemovesPageBug(t *testing.T) {
	s := spec.New(t, "run-correct-fix")
	beforeFixRequestID := model.RequestID("request-before-correct-fix")
	afterFixRequestID := model.RequestID("request-after-correct-fix")

	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 850_000, 200_000),
		s.Bug.Activated(pageBugID, model.PageProduct, "product-1", 1_000_000, correctFixText),
	)

	s.When(
		s.User.OpensPage(beforeFixRequestID, "visitor-before-fix", model.PageProduct, "product-1"),
	)

	s.Then(
		s.Logs.Exactly(pageBugLog(beforeFixRequestID, "visitor-before-fix")),
	)

	s.When(
		s.Bug.Fix("correct-fix-command", correctFixText),
	)

	s.Then(
		s.Events.Exactly(
			events.BugFixSubmitted{
				CommandID:   "correct-fix-command",
				Message:     correctFixText,
				SubmittedAt: worldStartsAt,
			},
			events.PageBugFixed{
				CommandID: "correct-fix-command",
				BugID:     pageBugID,
				FixedAt:   worldStartsAt,
			},
		),
	)

	s.When(
		s.User.OpensPage(afterFixRequestID, "visitor-after-fix", model.PageProduct, "product-1"),
	)

	s.Then(
		s.Logs.Exactly(
			pageBugLog(beforeFixRequestID, "visitor-before-fix"),
			successPageLog(afterFixRequestID, "visitor-after-fix"),
		),
	)
}

func TestIncorrectFixKeepsPageBugActive(t *testing.T) {
	s := spec.New(t, "run-incorrect-fix")
	beforeFixRequestID := model.RequestID("request-before-incorrect-fix")
	afterFixRequestID := model.RequestID("request-after-incorrect-fix")

	s.Given(
		s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
		s.Product.Added("product-1", "Coffee machine", 12_990, 850_000, 200_000),
		s.Bug.Activated(pageBugID, model.PageProduct, "product-1", 1_000_000, correctFixText),
	)

	s.When(
		s.User.OpensPage(beforeFixRequestID, "visitor-before-fix", model.PageProduct, "product-1"),
	)

	s.Then(
		s.Logs.Exactly(pageBugLog(beforeFixRequestID, "visitor-before-fix")),
	)

	s.When(
		s.Bug.Fix("incorrect-fix-command", "WRONG-FIX"),
	)

	s.Then(
		s.Events.Exactly(
			events.BugFixSubmitted{
				CommandID:   "incorrect-fix-command",
				Message:     "WRONG-FIX",
				SubmittedAt: worldStartsAt,
			},
			events.BugFixRejected{
				CommandID:  "incorrect-fix-command",
				Reason:     "FIX_MESSAGE_DOES_NOT_MATCH",
				RejectedAt: worldStartsAt,
			},
		),
	)

	s.When(
		s.User.OpensPage(afterFixRequestID, "visitor-after-fix", model.PageProduct, "product-1"),
	)

	s.Then(
		s.Logs.Exactly(
			pageBugLog(beforeFixRequestID, "visitor-before-fix"),
			pageBugLog(afterFixRequestID, "visitor-after-fix"),
		),
	)
}

func pageBugLog(requestID model.RequestID, visitorID model.VisitorID) logs.Entry {
	return logs.Entry{
		Timestamp:  worldStartsAt,
		RequestID:  requestID,
		Source:     model.RequestSourceVisitor,
		VisitorID:  visitorID,
		Page:       model.PageProduct,
		ProductID:  "product-1",
		StatusCode: 500,
		ErrorCode:  model.FailurePageBug,
		Message:    "чтоб этот баг пропал полностью, надо сделать фикс с текстом " + correctFixText,
	}
}

func successPageLog(requestID model.RequestID, visitorID model.VisitorID) logs.Entry {
	return successPageLogAt(requestID, visitorID, worldStartsAt)
}

func successPageLogAt(requestID model.RequestID, visitorID model.VisitorID, at time.Time) logs.Entry {
	return logs.Entry{
		Timestamp:  at,
		RequestID:  requestID,
		Source:     model.RequestSourceVisitor,
		VisitorID:  visitorID,
		Page:       model.PageProduct,
		ProductID:  "product-1",
		StatusCode: 200,
	}
}
