package spec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

type Step func(*Scenario) error
type Assertion func(*Scenario) error

type Scenario struct {
	t       *testing.T
	ctx     context.Context
	runID   string
	store   *simulation.MemoryEventStore
	handler *simulation.Handler
	engine  *simulation.Engine
	emitted []events.Event
	logs    []logs.Entry

	World   WorldDSL
	Product ProductDSL
	User    UserDSL
	Bug     BugDSL
	Time    TimeDSL
	State   StateDSL
	Events  EventsDSL
	Logs    LogsDSL
}

func New(t *testing.T, runID string) *Scenario {
	t.Helper()
	store := simulation.NewMemoryEventStore()
	engine := simulation.NewEngine(store)
	s := &Scenario{
		t:       t,
		ctx:     context.Background(),
		runID:   runID,
		store:   store,
		handler: simulation.NewHandler(store),
		engine:  engine,
	}
	t.Cleanup(engine.Close)
	s.World = WorldDSL{s: s}
	s.Product = ProductDSL{s: s}
	s.User = UserDSL{s: s}
	s.Bug = BugDSL{s: s}
	s.Time = TimeDSL{s: s}
	s.State = StateDSL{s: s}
	s.Events = EventsDSL{s: s}
	s.Logs = LogsDSL{s: s}
	return s
}

func (s *Scenario) Given(steps ...Step) {
	s.t.Helper()
	for _, step := range steps {
		if err := step(s); err != nil {
			s.t.Fatalf("given failed: %v", err)
		}
	}
	s.emitted = nil
}

func (s *Scenario) When(steps ...Step) {
	s.t.Helper()
	s.emitted = nil
	for _, step := range steps {
		if err := step(s); err != nil {
			s.t.Fatalf("when failed: %v", err)
		}
	}
}

func (s *Scenario) WhenFails(want error, step Step) {
	s.t.Helper()
	s.emitted = nil
	err := step(s)
	if !errors.Is(err, want) {
		s.t.Fatalf("when error = %v, want %v", err, want)
	}
}

func (s *Scenario) Then(assertions ...Assertion) {
	s.t.Helper()
	for _, assertion := range assertions {
		if err := assertion(s); err != nil {
			s.t.Errorf("then failed: %v", err)
		}
	}
}

func (s *Scenario) appendGiven(event events.Event) error {
	records, err := s.store.Load(s.ctx, s.runID)
	if err != nil {
		return err
	}
	_, err = s.store.Append(s.ctx, s.runID, uint64(len(records)), []events.Event{event})
	if err != nil {
		return err
	}
	_, err = s.handler.State(s.ctx, s.runID)
	return err
}

func (s *Scenario) execute(command simulation.Command) error {
	emitted, err := s.engine.Execute(s.ctx, s.runID, command)
	if err != nil {
		return err
	}
	s.emitted = append(s.emitted, emitted...)
	records, err := s.store.Load(s.ctx, s.runID)
	if err != nil {
		return err
	}
	stream := make([]events.Event, 0, len(records))
	for _, record := range records {
		stream = append(stream, record.Event)
	}
	s.logs, err = logs.Project(stream)
	return err
}

func (s *Scenario) state() (simulation.State, error) {
	return s.handler.State(s.ctx, s.runID)
}

type WorldDSL struct{ s *Scenario }

func (d WorldDSL) Created(seed int64, startedAt, endsAt time.Time) Step {
	return func(s *Scenario) error {
		return s.appendGiven(events.WorldCreated{
			RunID:     s.runID,
			Seed:      seed,
			StartedAt: startedAt,
			EndsAt:    endsAt,
		})
	}
}

func (d WorldDSL) Create(seed int64, startedAt, endsAt time.Time) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.CreateWorld{Seed: seed, StartedAt: startedAt, EndsAt: endsAt})
	}
}

type ProductDSL struct{ s *Scenario }

func (d ProductDSL) Added(id simulation.ProductID, name string, priceMinor int64, viewPPM, purchasePPM uint32) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		return s.appendGiven(events.ProductAdded{
			ProductID:              id,
			Name:                   name,
			PriceMinor:             priceMinor,
			ViewProbabilityPPM:     viewPPM,
			PurchaseProbabilityPPM: purchasePPM,
			AddedAt:                state.Clock.CurrentTime,
		})
	}
}

func (d ProductDSL) Add(id simulation.ProductID, name string, priceMinor int64, viewPPM, purchasePPM uint32) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.AddProduct{
			ProductID:              id,
			Name:                   name,
			PriceMinor:             priceMinor,
			ViewProbabilityPPM:     viewPPM,
			PurchaseProbabilityPPM: purchasePPM,
		})
	}
}

func (d ProductDSL) Purchase(purchaseID simulation.PurchaseID, productID simulation.ProductID) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.PurchaseProduct{PurchaseID: purchaseID, ProductID: productID})
	}
}

type TimeDSL struct{ s *Scenario }

func (d TimeDSL) Advance(realElapsed, requested time.Duration) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.AdvanceTime{
			RealElapsed:       realElapsed,
			RequestedDuration: requested,
		})
	}
}

type UserDSL struct{ s *Scenario }

func (d UserDSL) OpensPage(requestID model.RequestID, visitorID model.VisitorID, page model.PageType, productID model.ProductID) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.OpenPage{
			RequestID: requestID,
			VisitorID: visitorID,
			Page:      page,
			ProductID: productID,
		})
	}
}

type BugDSL struct{ s *Scenario }

func (d BugDSL) Activated(id model.BugID, page model.PageType, productID model.ProductID, failurePPM uint32, fixMessage string) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		fixMessageHash := sha256.Sum256([]byte(fixMessage))
		return s.appendGiven(events.PageBugActivated{
			BugID:                 id,
			Page:                  page,
			ProductID:             productID,
			FailureProbabilityPPM: failurePPM,
			FixMessage:            fixMessage,
			FixMessageHash:        hex.EncodeToString(fixMessageHash[:]),
			ActivatedAt:           state.Clock.CurrentTime,
		})
	}
}

type StateDSL struct{ s *Scenario }

func (d StateDSL) IsRunning() Assertion {
	return d.statusIs(simulation.RunRunning)
}

func (d StateDSL) IsCompleted() Assertion {
	return d.statusIs(simulation.RunCompleted)
}

func (d StateDSL) statusIs(want simulation.RunStatus) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		if state.Status != want {
			return fmt.Errorf("status = %q, want %q", state.Status, want)
		}
		return nil
	}
}

func (d StateDSL) HasProduct(want simulation.ProductState) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		got, exists := state.Products[want.ID]
		if !exists {
			return fmt.Errorf("product %q does not exist", want.ID)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("product = %#v, want %#v", got, want)
		}
		return nil
	}
}

func (d StateDSL) Economy(revenueMinor int64, successfulPurchases uint64) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		if state.Economy.RevenueMinor != revenueMinor || state.Economy.SuccessfulPurchases != successfulPurchases {
			return fmt.Errorf("economy = %#v, want revenue=%d purchases=%d", state.Economy, revenueMinor, successfulPurchases)
		}
		return nil
	}
}

func (d StateDSL) CurrentTime(want time.Time) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		if !state.Clock.CurrentTime.Equal(want) {
			return fmt.Errorf("current time = %s, want %s", state.Clock.CurrentTime, want)
		}
		return nil
	}
}

func (d StateDSL) Version(want uint64) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		if state.Version != want {
			return fmt.Errorf("version = %d, want %d", state.Version, want)
		}
		return nil
	}
}

type EventsDSL struct{ s *Scenario }

func (d EventsDSL) Exactly(want ...events.Event) Assertion {
	return func(s *Scenario) error {
		if !reflect.DeepEqual(s.emitted, want) {
			return fmt.Errorf("emitted events = %#v, want %#v", s.emitted, want)
		}
		return nil
	}
}

func (d EventsDSL) None() Assertion {
	return d.Exactly()
}

type LogsDSL struct{ s *Scenario }

func (d LogsDSL) Exactly(want ...logs.Entry) Assertion {
	return func(s *Scenario) error {
		if !reflect.DeepEqual(s.logs, want) {
			return fmt.Errorf("site logs = %#v, want %#v", s.logs, want)
		}
		return nil
	}
}
