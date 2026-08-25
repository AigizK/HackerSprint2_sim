package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const (
	agentID             = "daily-ops-agent"
	scheduleChunkEvents = 10_000
)

type runner struct {
	ctx            context.Context
	runID          string
	storage        *persistence.Storage
	session        *simulation.RunSession
	requestNumber  int
	commandNumber  int
	fixedMessages  map[string]bool
	lastLogVersion uint64
	stats          runStats
}

type runStats struct {
	days, agentRequests, bugErrors, capacityErrors, fixes       int
	deploymentsStarted, deploymentsSucceeded, deploymentsFailed int
	serversAdded, serversRemoved                                int
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "daily agent:", err)
		os.Exit(1)
	}
}

func run() error {
	seed := flag.Int64("seed", 1, "positive world seed")
	profilePath := flag.String("config", "config/world-generation.v1.yaml", "world generation profile")
	dataPath := flag.String("data", "data", "storage root")
	flag.Parse()
	ctx := context.Background()
	profile, err := generator.LoadProfileFile(*profilePath)
	if err != nil {
		return err
	}
	worldGenerator, err := generator.New(profile)
	if err != nil {
		return err
	}
	storage, err := persistence.Open(*dataPath)
	if err != nil {
		return err
	}
	defer storage.Close()
	world, _, err := worldGenerator.GetOrCreate(ctx, storage.Catalog, *seed)
	if err != nil {
		return err
	}
	runID, err := randomRunID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := storage.Catalog.CreateRun(ctx, simulation.RunRecord{
		RunID: runID, WorldID: world.WorldID, AgentID: agentID, AgentVersion: "v1", StartRequestID: "start-" + runID,
		Status: simulation.RunNotCreated, StartedAt: world.StartsAt, EndsAt: world.EndsAt, CurrentTime: world.StartsAt,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return err
	}

	r := &runner{ctx: ctx, runID: runID, storage: storage, fixedMessages: make(map[string]bool)}
	initial := []events.Event{events.WorldCreated{RunID: runID, Seed: world.Key.Seed, StartedAt: world.StartsAt, EndsAt: world.EndsAt}}
	initial = append(initial, world.Bootstrap...)
	if err := r.agentCall("POST", "/v1/start", func() error {
		appended, appendErr := storage.Journal.Append(ctx, runID, 0, initial)
		if appendErr != nil {
			return appendErr
		}
		version := appended[len(appended)-1].Version
		for offset := 0; offset < len(world.Events); offset += scheduleChunkEvents {
			end := offset + scheduleChunkEvents
			if end > len(world.Events) {
				end = len(world.Events)
			}
			appended, appendErr = storage.Journal.Append(ctx, runID, version, []events.Event{
				events.WorldScheduleCreated{Schedule: world.Events[offset:end], CreatedAt: world.StartsAt},
			})
			if appendErr != nil {
				return appendErr
			}
			version = appended[len(appended)-1].Version
		}
		return nil
	}); err != nil {
		return err
	}
	r.session, err = simulation.OpenRunSession(ctx, storage.Journal, runID)
	if err != nil {
		return err
	}
	r.lastLogVersion = r.session.Version()

	lastCheck := world.StartsAt
	for {
		state, err := r.state()
		if err != nil {
			return err
		}
		if state.Status == simulation.RunCompleted {
			break
		}
		if err := r.dailyActions(lastCheck, state); err != nil {
			return err
		}
		state, err = r.state()
		if err != nil {
			return err
		}
		if state.Status == simulation.RunCompleted {
			break
		}
		remaining := state.Clock.EndsAt.Sub(state.Clock.CurrentTime)
		step := 24 * time.Hour
		if remaining < step {
			step = remaining
		}
		lastCheck = state.Clock.CurrentTime
		r.stats.days++
		if err := r.command("POST", "/v1/runs/"+runID+"/time:advance", simulation.AdvanceTime{
			CommandID: r.commandID("advance"), RequestedDuration: step,
		}); err != nil {
			return err
		}
	}
	return r.printResult(world)
}

func (r *runner) dailyActions(_ time.Time, state simulation.State) error {
	var logEntries []logs.Entry
	if err := r.agentCall("GET", "/v1/runs/"+r.runID+"/logs", func() error {
		records, err := r.session.EventsAfter(r.lastLogVersion)
		if err != nil {
			return err
		}
		delta := make([]events.Event, 0, len(records))
		for _, record := range records {
			delta = append(delta, record.Event)
		}
		logEntries, err = logs.Project(delta)
		if err == nil {
			r.lastLogVersion = r.session.Version()
		}
		return err
	}); err != nil {
		return err
	}
	capacityFailures := 0
	fixes := make([]string, 0)
	for _, entry := range logEntries {
		if entry.StatusCode < 500 {
			continue
		}
		switch entry.ErrorCode {
		case model.FailurePageBug:
			r.stats.bugErrors++
			const prefix = "чтоб этот баг пропал полностью, надо сделать фикс с текстом "
			if strings.HasPrefix(entry.Message, prefix) {
				message := strings.TrimPrefix(entry.Message, prefix)
				if !r.fixedMessages[message] {
					fixes = append(fixes, message)
					r.fixedMessages[message] = true
				}
			}
		case model.FailureServerCapacityExceeded:
			capacityFailures++
			r.stats.capacityErrors++
		}
	}
	sort.Strings(fixes)
	for _, message := range fixes {
		if err := r.command("POST", "/v1/runs/"+r.runID+"/fixes", simulation.ApplyFix{
			CommandID: r.commandID("fix"), Message: message,
		}); err != nil {
			return err
		}
		r.stats.fixes++
	}

	state, err := r.state()
	if err != nil {
		return err
	}
	if state.ActiveDeployment == "" {
		available := make([]simulation.DeploymentState, 0)
		for _, deployment := range state.Deployments {
			if deployment.Status == model.DeploymentStatusAvailable {
				available = append(available, deployment)
			}
		}
		sort.Slice(available, func(i, j int) bool { return available[i].Sequence < available[j].Sequence })
		if len(available) > 0 {
			id := r.commandID("deploy")
			if err := r.command("POST", "/v1/runs/"+r.runID+"/deployments", simulation.StartDeployment{
				CommandID: id, DeploymentID: available[0].ID, OperationID: model.OperationID(id + "-operation"),
			}); err != nil {
				return err
			}
			r.stats.deploymentsStarted++
		}
	}

	resources := resourcesFromState(r.session.State())
	if err := r.agentCall("GET", "/v1/runs/"+r.runID+"/resources", func() error {
		resources = resourcesFromState(r.session.State())
		return nil
	}); err != nil {
		return err
	}
	if capacityFailures > 0 {
		var template simulation.ServerResourceView
		for _, server := range resources.Servers {
			if server.Status == model.ServerActive {
				template = server
				break
			}
		}
		id := r.commandID("scale-up")
		if err := r.command("POST", "/v1/runs/"+r.runID+"/resources", simulation.AddServer{
			CommandID: id, OperationID: model.OperationID(id + "-operation"), ServerID: model.ServerID(id + "-server"),
			CapacityUnits: template.CapacityUnits, CostPerHourMinor: template.CostPerHourMinor,
		}); err != nil {
			return err
		}
		r.stats.serversAdded++
	} else if resources.ActiveInstances > 1 && resources.UsedLoadUnits == 0 {
		for index := len(resources.Servers) - 1; index >= 0; index-- {
			server := resources.Servers[index]
			if server.Status != model.ServerActive {
				continue
			}
			id := r.commandID("scale-down")
			if err := r.command("POST", "/v1/runs/"+r.runID+"/resources", simulation.RemoveServer{
				CommandID: id, OperationID: model.OperationID(id + "-operation"), ServerID: server.ServerID,
			}); err != nil {
				return err
			}
			r.stats.serversRemoved++
			break
		}
	}
	return nil
}

func (r *runner) state() (simulation.State, error) {
	var state simulation.State
	err := r.agentCall("GET", "/v1/runs/"+r.runID+"/overview", func() error {
		state = r.session.State()
		return nil
	})
	return state, err
}

func (r *runner) command(method, path string, command simulation.Command) error {
	return r.agentCall(method, path, func() error { _, err := r.session.Execute(r.ctx, command); return err })
}

func (r *runner) agentCall(method, path string, call func() error) error {
	r.requestNumber++
	requestID := fmt.Sprintf("agent-request-%05d", r.requestNumber)
	started := time.Now().UTC()
	if err := r.storage.Journal.RecordAgentRequest(r.ctx, r.runID, journal.AgentRequestReceived{
		RequestID: requestID, AgentID: agentID, Method: method, Path: path, ReceivedAt: started,
	}); err != nil {
		return err
	}
	callErr := call()
	status := 200
	errorMessage := ""
	if callErr != nil {
		status = 500
		errorMessage = callErr.Error()
	}
	if err := r.storage.Journal.CompleteAgentRequest(r.ctx, r.runID, journal.AgentRequestCompleted{
		RequestID: requestID, StatusCode: status, Error: errorMessage, CompletedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	r.stats.agentRequests++
	return callErr
}

func (r *runner) commandID(kind string) model.CommandID {
	r.commandNumber++
	return model.CommandID(fmt.Sprintf("agent-%s-%05d", kind, r.commandNumber))
}

func (r *runner) printResult(world generator.WorldDefinition) error {
	state := r.session.State()
	for _, deployment := range state.Deployments {
		switch deployment.Status {
		case model.DeploymentStatusApplied, model.DeploymentStatusSucceeded:
			r.stats.deploymentsSucceeded++
		case model.DeploymentStatusFailed:
			r.stats.deploymentsFailed++
		}
	}
	economy := simulation.EconomyView{SuccessfulPurchases: state.Economy.SuccessfulPurchases,
		RevenueMinor: state.Economy.RevenueMinor, ServerCostMinor: state.Economy.ServerCostMinor,
		DeploymentCostMinor: state.Economy.DeploymentCostMinor}
	economy.BalanceMinor = state.Economy.InitialBalanceMinor + economy.RevenueMinor - economy.ServerCostMinor - economy.DeploymentCostMinor
	activeBugs := 0
	for _, bug := range state.Bugs {
		if bug.Status == model.BugActive {
			activeBugs++
		}
	}
	fmt.Printf("run_id=%s\nseed=%d\nstarted_at=%s\nended_at=%s\nstatus=%s\n", r.runID, world.Key.Seed, world.StartsAt.Format(time.RFC3339), state.Clock.CurrentTime.Format(time.RFC3339), state.Status)
	fmt.Printf("days=%d\nagent_requests=%d\nrun_events=%d\n", r.stats.days, r.stats.agentRequests, r.session.Version())
	fmt.Printf("bug_errors_seen=%d\nfixes_applied=%d\nactive_bugs_left=%d\n", r.stats.bugErrors, r.stats.fixes, activeBugs)
	fmt.Printf("deployments_started=%d\ndeployments_succeeded=%d\ndeployments_failed=%d\n", r.stats.deploymentsStarted, r.stats.deploymentsSucceeded, r.stats.deploymentsFailed)
	fmt.Printf("capacity_errors_seen=%d\nservers_added=%d\nservers_removed=%d\n", r.stats.capacityErrors, r.stats.serversAdded, r.stats.serversRemoved)
	fmt.Printf("purchases=%d\nrevenue_minor=%d\nserver_cost_minor=%d\ndeployment_cost_minor=%d\nbalance_minor=%d\n", economy.SuccessfulPurchases, economy.RevenueMinor, economy.ServerCostMinor, economy.DeploymentCostMinor, economy.BalanceMinor)
	fmt.Printf("oracle_maximum_balance_minor=%d\n", world.Evaluation.MaximumBalanceMinor)
	return nil
}

func resourcesFromState(state simulation.State) simulation.ResourcesView {
	result := simulation.ResourcesView{DesiredInstances: state.DesiredInstances}
	ids := make([]string, 0, len(state.Servers))
	for id := range state.Servers {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, rawID := range ids {
		server := state.Servers[model.ServerID(rawID)]
		view := simulation.ServerResourceView{ServerID: server.ID, Status: server.Status, CapacityUnits: server.CapacityUnits, CostPerHourMinor: server.CostPerHourMinor}
		for _, allocation := range state.Capacity {
			if allocation.ServerID == server.ID && allocation.ReleasesAt.After(state.Clock.CurrentTime) {
				view.UsedLoadUnits += allocation.LoadUnits
			}
		}
		result.UsedLoadUnits += view.UsedLoadUnits
		if server.Status == model.ServerActive {
			result.ActiveInstances++
			result.TotalCapacityUnits += server.CapacityUnits
		}
		if server.Status == model.ServerActive || server.Status == model.ServerDraining {
			result.TotalCostPerHourMinor += server.CostPerHourMinor
		}
		result.Servers = append(result.Servers, view)
	}
	return result
}

func randomRunID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "r" + hex.EncodeToString(bytes), nil
}
