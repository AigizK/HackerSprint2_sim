package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestUnknownOperationIsNotFound(t *testing.T) {
	s := spec.New(t, "run-operation-missing")
	s.Given(s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))
	s.Then(s.Future.OperationMissing("unknown-operation"))
}

func TestAdvanceTimeCommandIsIdempotent(t *testing.T) {
	s := spec.New(t, "run-idempotent-time")
	s.Given(s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)))

	s.When(s.Time.AdvanceRequest("same-time-request", 0, 5*time.Minute))
	s.When(s.Time.AdvanceRequest("same-time-request", 0, 5*time.Minute))
	s.Then(
		s.Events.None(),
		s.State.CurrentTime(worldStartsAt.Add(5*time.Minute)),
	)
}
