package spec

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

var ErrFutureDriverNotImplemented = errors.New("future simulation driver is not implemented")

type OverviewView struct {
	SimulationTime       time.Time
	SimulationEndsAt     time.Time
	Remaining            time.Duration
	RunStatus            string
	SiteStatus           string
	ServerCount          int
	CapacityUtilization  float64
	ErrorRate            float64
	VisitorRequestsTotal uint64
	VisitorErrorRate     float64
	Uptime               float64
}

type MetricSnapshotView struct {
	ServerCount     int
	CapacityUnits   int64
	UsedLoadUnits   int64
	ActiveRequests  int
	Responses200    uint64
	Responses403    uint64
	Responses500    uint64
	Responses503    uint64
	ErrorRate       float64
	LatencyP50      time.Duration
	LatencyP95      time.Duration
	ServerCostMinor int64
	ByPage          []PageMetricView
}

type PageMetricView struct {
	Page           model.PageType
	ActiveRequests int
	UsedLoadUnits  int64
	Responses200   uint64
	Responses403   uint64
	Responses500   uint64
	Responses503   uint64
	ErrorRate      float64
}

type MetricPointView struct {
	Timestamp time.Time
	Name      string
	Page      model.PageType
	Value     float64
}

type MetricsQuery struct {
	From  time.Time
	To    time.Time
	Step  time.Duration
	Names []string
	Page  model.PageType
}

type MetricsView struct {
	Current MetricSnapshotView
	Series  []MetricPointView
}

type LogsQuery struct {
	From       time.Time
	To         time.Time
	Page       model.PageType
	StatusCode int
	Cursor     string
	Limit      int
}

type RequestLogView struct {
	Entry     logs.Entry
	Latency   time.Duration
	LoadUnits int64
	ServerID  model.ServerID
}

type LogsView struct {
	Logs       []RequestLogView
	NextCursor string
}

type ServerResourceView struct {
	ServerID         model.ServerID
	Status           model.ServerLifecycleStatus
	CapacityUnits    int64
	UsedLoadUnits    int64
	CostPerHourMinor int64
}

type ResourcesView struct {
	ActiveInstances       int
	TotalCapacityUnits    int64
	UsedLoadUnits         int64
	TotalCostPerHourMinor int64
	Servers               []ServerResourceView
}

type OperationView struct {
	OperationID model.OperationID
	Kind        model.OperationKind
	Status      model.OperationLifecycleStatus
	Progress    float64
	SubmittedAt time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	ErrorCode   string
	Message     string
}

type FutureDriver interface {
	GenerateWorld(seed int64) (events.EventSchedule, error)
	LoadManualWorld(seed int64) (events.EventSchedule, error)
	Overview(runID string) (OverviewView, error)
	Metrics(runID string, query MetricsQuery) (MetricsView, error)
	Logs(runID string, query LogsQuery) (LogsView, error)
	Resources(runID string) (ResourcesView, error)
	Operation(runID string, operationID model.OperationID) (OperationView, error)
}

type FutureDSL struct{ s *Scenario }

func (d FutureDSL) GeneratedWorldsEqual(seed int64) Assertion {
	return func(s *Scenario) error {
		if s.future == nil {
			return ErrFutureDriverNotImplemented
		}
		first, err := s.future.GenerateWorld(seed)
		if err != nil {
			return err
		}
		second, err := s.future.GenerateWorld(seed)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(first, second) {
			return fmt.Errorf("worlds generated from seed %d differ", seed)
		}
		return nil
	}
}

func (d FutureDSL) GeneratedWorldsDiffer(firstSeed, secondSeed int64) Assertion {
	return func(s *Scenario) error {
		if s.future == nil {
			return ErrFutureDriverNotImplemented
		}
		first, err := s.future.GenerateWorld(firstSeed)
		if err != nil {
			return err
		}
		second, err := s.future.GenerateWorld(secondSeed)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(first, second) {
			return fmt.Errorf("worlds generated from seeds %d and %d are equal", firstSeed, secondSeed)
		}
		return nil
	}
}

func (d FutureDSL) ManualWorldAvailable(seed int64) Assertion {
	return func(s *Scenario) error {
		if s.future == nil {
			return ErrFutureDriverNotImplemented
		}
		_, err := s.future.LoadManualWorld(seed)
		return err
	}
}

func (d FutureDSL) ManualWorldMissing(seed int64) Assertion {
	return func(s *Scenario) error {
		if s.future == nil {
			return ErrFutureDriverNotImplemented
		}
		if _, err := s.future.LoadManualWorld(seed); err == nil {
			return fmt.Errorf("manual world %d unexpectedly exists", seed)
		}
		return nil
	}
}

func (d FutureDSL) Overview(want OverviewView) Assertion {
	return d.equals("overview", want, func(driver FutureDriver, runID string) (any, error) {
		return driver.Overview(runID)
	})
}

func (d FutureDSL) Metrics(query MetricsQuery, want MetricsView) Assertion {
	return d.equals("metrics", want, func(driver FutureDriver, runID string) (any, error) {
		return driver.Metrics(runID, query)
	})
}

func (d FutureDSL) Logs(query LogsQuery, want LogsView) Assertion {
	return d.equals("logs", want, func(driver FutureDriver, runID string) (any, error) {
		return driver.Logs(runID, query)
	})
}

func (d FutureDSL) Resources(want ResourcesView) Assertion {
	return d.equals("resources", want, func(driver FutureDriver, runID string) (any, error) {
		return driver.Resources(runID)
	})
}

func (d FutureDSL) Operation(operationID model.OperationID, want OperationView) Assertion {
	return d.equals("operation", want, func(driver FutureDriver, runID string) (any, error) {
		return driver.Operation(runID, operationID)
	})
}

func (d FutureDSL) OperationMissing(operationID model.OperationID) Assertion {
	return func(s *Scenario) error {
		if s.future == nil {
			return ErrFutureDriverNotImplemented
		}
		if _, err := s.future.Operation(s.runID, operationID); err == nil {
			return fmt.Errorf("operation %q unexpectedly exists", operationID)
		}
		return nil
	}
}

func (d FutureDSL) equals(
	name string,
	want any,
	get func(FutureDriver, string) (any, error),
) Assertion {
	return func(s *Scenario) error {
		if s.future == nil {
			return ErrFutureDriverNotImplemented
		}
		got, err := get(s.future, s.runID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("%s = %#v, want %#v", name, got, want)
		}
		return nil
	}
}
