package spec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
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

	World       WorldDSL
	Product     ProductDSL
	Page        PageDSL
	Server      ServerDSL
	User        UserDSL
	Bug         BugDSL
	Deployment  DeploymentDSL
	Time        TimeDSL
	State       StateDSL
	Events      EventsDSL
	Logs        LogsDSL
	Deployments DeploymentsDSL
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
	s.Page = PageDSL{s: s}
	s.Server = ServerDSL{s: s}
	s.User = UserDSL{s: s}
	s.Bug = BugDSL{s: s}
	s.Deployment = DeploymentDSL{s: s}
	s.Time = TimeDSL{s: s}
	s.State = StateDSL{s: s}
	s.Events = EventsDSL{s: s}
	s.Logs = LogsDSL{s: s}
	s.Deployments = DeploymentsDSL{s: s}
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

func (d WorldDSL) InfrastructureConfigured(serverProvisioningDuration time.Duration) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		return s.appendGiven(events.InfrastructureConfigured{
			ServerProvisioningDuration: serverProvisioningDuration,
			ConfiguredAt:               state.Clock.CurrentTime,
		})
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

type PageDSL struct{ s *Scenario }

func (d PageDSL) Configured(page model.PageType, loadUnits int64, holdDuration time.Duration) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		return s.appendGiven(events.PageConfigured{
			Page:         page,
			LoadUnits:    loadUnits,
			HoldDuration: holdDuration,
			ConfiguredAt: state.Clock.CurrentTime,
		})
	}
}

type ServerDSL struct{ s *Scenario }

func (d ServerDSL) Active(id model.ServerID, capacityUnits, costPerHourMinor int64) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		operationID := model.OperationID("initial-" + string(id))
		if err := s.appendGiven(events.ServerProvisioningStarted{
			OperationID:      operationID,
			ServerID:         id,
			CapacityUnits:    capacityUnits,
			CostPerHourMinor: costPerHourMinor,
			StartedAt:        state.Clock.CurrentTime,
			ReadyAt:          state.Clock.CurrentTime,
		}); err != nil {
			return err
		}
		return s.appendGiven(events.ServerActivated{
			OperationID: operationID,
			ServerID:    id,
			ActivatedAt: state.Clock.CurrentTime,
		})
	}
}

func (d ServerDSL) Add(
	commandID model.CommandID,
	operationID model.OperationID,
	serverID model.ServerID,
	capacityUnits, costPerHourMinor int64,
) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.AddServer{
			CommandID:        commandID,
			OperationID:      operationID,
			ServerID:         serverID,
			CapacityUnits:    capacityUnits,
			CostPerHourMinor: costPerHourMinor,
		})
	}
}

func (d ServerDSL) Remove(commandID model.CommandID, operationID model.OperationID, serverID model.ServerID) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.RemoveServer{
			CommandID:   commandID,
			OperationID: operationID,
			ServerID:    serverID,
		})
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

func (d BugDSL) Fix(commandID model.CommandID, message string) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.ApplyFix{
			CommandID: commandID,
			Message:   message,
		})
	}
}

type DeploymentDSL struct{ s *Scenario }

func (d DeploymentDSL) Defined(
	id model.DeploymentID,
	sequence int,
	name string,
	duration time.Duration,
	failureProbabilityPPM uint32,
) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		return s.appendGiven(events.DeploymentDefined{
			DeploymentID:          id,
			Sequence:              sequence,
			Name:                  name,
			Description:           name,
			Duration:              duration,
			FailureProbabilityPPM: failureProbabilityPPM,
			DefinedAt:             state.Clock.CurrentTime,
		})
	}
}

func (d DeploymentDSL) Unlocked(id model.DeploymentID) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		return s.appendGiven(events.DeploymentUnlocked{
			DeploymentID: id,
			UnlockedAt:   state.Clock.CurrentTime,
		})
	}
}

func (d DeploymentDSL) PageLoadEffect(
	id model.DeploymentID,
	page model.PageType,
	newLoadUnits int64,
	newHoldDuration time.Duration,
) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		return s.appendGiven(events.DeploymentPageLoadEffectDefined{
			DeploymentID:    id,
			Page:            page,
			NewLoadUnits:    newLoadUnits,
			NewHoldDuration: newHoldDuration,
			DefinedAt:       state.Clock.CurrentTime,
		})
	}
}

func (d DeploymentDSL) BugProbabilityEffect(id model.DeploymentID, bugID model.BugID, newProbabilityPPM uint32) Step {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		return s.appendGiven(events.DeploymentBugProbabilityEffectDefined{
			DeploymentID:      id,
			BugID:             bugID,
			NewProbabilityPPM: newProbabilityPPM,
			DefinedAt:         state.Clock.CurrentTime,
		})
	}
}

func (d DeploymentDSL) Start(commandID model.CommandID, deploymentID model.DeploymentID, operationID model.OperationID) Step {
	return func(s *Scenario) error {
		return s.execute(simulation.StartDeployment{
			CommandID:    commandID,
			DeploymentID: deploymentID,
			OperationID:  operationID,
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

func (d StateDSL) ServerStatus(id model.ServerID, want model.ServerLifecycleStatus) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		server, exists := state.Servers[id]
		if !exists {
			return fmt.Errorf("server %q does not exist", id)
		}
		if server.Status != want {
			return fmt.Errorf("server %q status = %q, want %q", id, server.Status, want)
		}
		return nil
	}
}

func (d StateDSL) HasNoServer(id model.ServerID) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		if _, exists := state.Servers[id]; exists {
			return fmt.Errorf("server %q still exists", id)
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

func (d EventsDSL) Contains(want ...events.Event) Assertion {
	return func(s *Scenario) error {
		for _, expected := range want {
			found := false
			for _, actual := range s.emitted {
				if reflect.DeepEqual(actual, expected) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("emitted events %#v do not contain %#v", s.emitted, expected)
			}
		}
		return nil
	}
}

func (d EventsDSL) HasNoType(eventType string) Assertion {
	return func(s *Scenario) error {
		for _, event := range s.emitted {
			if event.EventType() == eventType {
				return fmt.Errorf("emitted events unexpectedly contain %s: %#v", eventType, s.emitted)
			}
		}
		return nil
	}
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

type DeploymentStatusExpectation struct {
	ID       model.DeploymentID
	Sequence int
	Status   model.DeploymentLifecycleStatus
}

type DeploymentsDSL struct{ s *Scenario }

func (d DeploymentsDSL) Statuses(want ...DeploymentStatusExpectation) Assertion {
	return func(s *Scenario) error {
		state, err := s.state()
		if err != nil {
			return err
		}
		got := make([]DeploymentStatusExpectation, 0, len(state.Deployments))
		for _, deployment := range state.Deployments {
			got = append(got, DeploymentStatusExpectation{
				ID:       deployment.ID,
				Sequence: deployment.Sequence,
				Status:   deployment.Status,
			})
		}
		sort.Slice(got, func(i, j int) bool { return got[i].Sequence < got[j].Sequence })
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("deployments = %#v, want %#v", got, want)
		}
		return nil
	}
}
