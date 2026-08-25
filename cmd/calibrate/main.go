package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"go.yaml.in/yaml/v3"
)

type runResult struct {
	seed, worldEvents, runEvents, visitors, maxDailyVisitors, maxDailyDelta int
	endedAt, worldEndsAt                                                    time.Time
	reason                                                                  string
	balanceMinor, revenueMinor, serverCostMinor, maximumBalanceMinor        int64
}

type calibrationConfig struct {
	Version               string         `yaml:"version"`
	Runs                  int            `yaml:"runs"`
	FirstSeed             int            `yaml:"first_seed"`
	TargetScheduledEvents int64          `yaml:"target_scheduled_events"`
	MaxWorldEvents        int            `yaml:"max_world_events"`
	MaxRunEvents          int            `yaml:"max_run_events"`
	TargetEarlyNegative   int            `yaml:"target_early_negative"`
	TargetCompletedRuns   int            `yaml:"target_completed_runs"`
	BadAgent              badAgentConfig `yaml:"bad_agent"`
}

type badAgentConfig struct {
	DesiredBackendInstances int `yaml:"desired_backend_instances"`
	AdvanceStepHours        int `yaml:"advance_step_hours"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "calibrate:", err)
		os.Exit(1)
	}
}

func run() error {
	profilePath := flag.String("world-config", "config/world-generation.v1.yaml", "world generation profile")
	calibrationPath := flag.String("calibration-config", "config/calibration.v1.yaml", "calibration profile")
	flag.Parse()
	calibration, err := loadCalibration(*calibrationPath)
	if err != nil {
		return err
	}
	profile, err := generator.LoadProfileFile(*profilePath)
	if err != nil {
		return err
	}
	profile.Traffic.TargetScheduledEvents = calibration.TargetScheduledEvents
	if profile.Limits.MaxVisitors < calibration.TargetScheduledEvents {
		profile.Limits.MaxVisitors = calibration.TargetScheduledEvents
	}
	if profile.Limits.MaxScheduledEvents < calibration.TargetScheduledEvents {
		profile.Limits.MaxScheduledEvents = calibration.TargetScheduledEvents
	}
	worldGenerator, err := generator.New(profile)
	if err != nil {
		return err
	}

	results := make([]runResult, 0, calibration.Runs)
	for seed := calibration.FirstSeed; seed < calibration.FirstSeed+calibration.Runs; seed++ {
		world, err := worldGenerator.Generate(int64(seed))
		if err != nil {
			return fmt.Errorf("seed %d: %w", seed, err)
		}
		result, err := simulateBadRun(world, calibration.BadAgent)
		if err != nil {
			return fmt.Errorf("seed %d: %w", seed, err)
		}
		results = append(results, result)
	}

	earlyNegative, completedRuns, maxWorldEvents, maxRunEvents, maxDailyVisitors, maxDailyDelta := 0, 0, 0, 0, 0, 0
	minimumMaximumBalance := int64(^uint64(0) >> 1)
	fmt.Println("seed world_events run_events visitors max_daily max_delta ended_at reason balance_minor revenue_minor server_cost_minor maximum_balance_minor")
	for _, result := range results {
		maxWorldEvents = max(maxWorldEvents, result.worldEvents)
		maxRunEvents = max(maxRunEvents, result.runEvents)
		maxDailyVisitors = max(maxDailyVisitors, result.maxDailyVisitors)
		maxDailyDelta = max(maxDailyDelta, result.maxDailyDelta)
		if result.maximumBalanceMinor < minimumMaximumBalance {
			minimumMaximumBalance = result.maximumBalanceMinor
		}
		if result.endedAt.Equal(result.worldEndsAt) {
			completedRuns++
		}
		if result.reason == "negative_balance" && result.endedAt.Before(result.worldEndsAt) {
			earlyNegative++
		}
		fmt.Printf("%d %d %d %d %d %d %s %s %d %d %d %d\n", result.seed, result.worldEvents, result.runEvents,
			result.visitors, result.maxDailyVisitors, result.maxDailyDelta, result.endedAt.Format(time.RFC3339), result.reason,
			result.balanceMinor, result.revenueMinor, result.serverCostMinor, result.maximumBalanceMinor)
	}
	fmt.Printf("summary runs=%d completed_runs=%d max_world_events=%d max_run_events=%d early_negative=%d min_maximum_balance_minor=%d max_daily_visitors=%d max_daily_delta=%d\n",
		len(results), completedRuns, maxWorldEvents, maxRunEvents, earlyNegative, minimumMaximumBalance, maxDailyVisitors, maxDailyDelta)
	if maxWorldEvents > calibration.MaxWorldEvents || maxRunEvents > calibration.MaxRunEvents ||
		earlyNegative != calibration.TargetEarlyNegative || completedRuns != calibration.TargetCompletedRuns || minimumMaximumBalance <= 0 {
		return fmt.Errorf("calibration gates failed")
	}
	return nil
}

func simulateBadRun(world generator.WorldDefinition, badAgent badAgentConfig) (runResult, error) {
	runID := fmt.Sprintf("calibration-seed-%d", world.Key.Seed)
	initial := []events.Event{events.WorldCreated{
		RunID: runID, Seed: world.Key.Seed, StartedAt: world.StartsAt, EndsAt: world.EndsAt,
	}}
	initial = append(initial, world.Bootstrap...)
	initial = append(initial, events.WorldScheduleCreated{Schedule: world.Events, CreatedAt: world.StartsAt})
	state := simulation.NewState()
	runEventCount := 0
	for _, event := range initial {
		if err := state.Apply(event); err != nil {
			return runResult{}, err
		}
		runEventCount++
	}
	scaleEvents, err := applyCalibrationCommand(runID, &state, simulation.SetBackendDesiredInstances{
		CommandID: "bad-scale", OperationID: "bad-scale-operation", DesiredInstances: badAgent.DesiredBackendInstances,
	})
	if err != nil {
		return runResult{}, err
	}
	runEventCount += len(scaleEvents)

	for step := 1; state.Status != simulation.RunCompleted; step++ {
		remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
		advance := time.Duration(badAgent.AdvanceStepHours) * time.Hour
		if remaining < advance {
			advance = remaining
		}
		emitted, err := applyCalibrationCommand(runID, &state, simulation.AdvanceTime{
			CommandID: model.CommandID(fmt.Sprintf("bad-step-%03d", step)), RequestedDuration: advance,
		})
		if err != nil {
			return runResult{}, err
		}
		runEventCount += len(emitted)
	}
	visitors, maxDailyVisitors, maxDailyDelta := trafficStats(world)
	return runResult{
		seed: int(world.Key.Seed), worldEvents: len(world.Bootstrap) + len(world.Events), runEvents: runEventCount,
		endedAt: state.Clock.CurrentTime, worldEndsAt: world.EndsAt, reason: calibrationEndReason(state),
		balanceMinor:        state.Economy.InitialBalanceMinor + state.Economy.RevenueMinor - state.Economy.ServerCostMinor - state.Economy.DeploymentCostMinor,
		revenueMinor:        state.Economy.RevenueMinor,
		serverCostMinor:     state.Economy.ServerCostMinor,
		maximumBalanceMinor: world.Evaluation.MaximumBalanceMinor,
		visitors:            visitors, maxDailyVisitors: maxDailyVisitors, maxDailyDelta: maxDailyDelta,
	}, nil
}

func applyCalibrationCommand(runID string, state *simulation.State, command simulation.Command) ([]events.Event, error) {
	decided, err := simulation.DecideWithRunPolicies(runID, *state, command)
	if err != nil {
		return nil, err
	}
	for _, event := range decided {
		if err := state.Apply(event); err != nil {
			return nil, err
		}
	}
	return decided, nil
}

func trafficStats(world generator.WorldDefinition) (int, int, int) {
	counts := make(map[string]int)
	total := 0
	for _, scheduled := range world.Events {
		if _, ok := scheduled.Event.(events.VisitorArrived); !ok {
			continue
		}
		counts[scheduled.OccursAt.UTC().Format(time.DateOnly)]++
		total++
	}
	maximum, maximumDelta, previous := 0, 0, 0
	for day := world.StartsAt; day.Before(world.EndsAt); day = day.AddDate(0, 0, 1) {
		current := counts[day.UTC().Format(time.DateOnly)]
		maximum = max(maximum, current)
		delta := current - previous
		if delta < 0 {
			delta = -delta
		}
		maximumDelta = max(maximumDelta, delta)
		previous = current
	}
	return total, maximum, maximumDelta
}

func calibrationEndReason(state simulation.State) string {
	balance := state.Economy.InitialBalanceMinor + state.Economy.RevenueMinor - state.Economy.ServerCostMinor - state.Economy.DeploymentCostMinor
	if balance < 0 {
		return "negative_balance"
	}
	return "world_completed"
}

func loadCalibration(path string) (calibrationConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return calibrationConfig{}, err
	}
	defer file.Close()
	return decodeCalibration(file)
}

func decodeCalibration(reader io.Reader) (calibrationConfig, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	var config calibrationConfig
	if err := decoder.Decode(&config); err != nil {
		return calibrationConfig{}, err
	}
	if config.Version == "" || config.Runs <= 0 || config.FirstSeed <= 0 || config.TargetScheduledEvents <= 0 || config.MaxWorldEvents <= 0 ||
		config.MaxRunEvents <= 0 || config.TargetEarlyNegative < 0 || config.TargetEarlyNegative > config.Runs ||
		config.TargetCompletedRuns < 0 || config.TargetCompletedRuns > config.Runs ||
		config.TargetEarlyNegative+config.TargetCompletedRuns != config.Runs ||
		config.BadAgent.DesiredBackendInstances < 0 || config.BadAgent.AdvanceStepHours <= 0 {
		return calibrationConfig{}, fmt.Errorf("invalid calibration config")
	}
	return config, nil
}
