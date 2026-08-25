package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	pageBugError        = "PAGE_BUG"
	capacityError       = "SERVER_CAPACITY_EXCEEDED"
	fixMessagePrefix    = "чтоб этот баг пропал полностью, надо сделать фикс с текстом "
	deploymentAvailable = "available"
)

type agentConfig struct {
	seed         int64
	agentID      string
	agentVersion string
	advance      time.Duration
	maxSteps     int
}

type agentStats struct {
	steps, bugErrors, capacityErrors, fixes          int
	deploymentsStarted, serversAdded, serversRemoved int
}

type httpAgent struct {
	client        *apiClient
	config        agentConfig
	runID         string
	requestPrefix string
	requestNumber int
	fixedMessages map[string]bool
	stats         agentStats
	lastLogTime   time.Time
}

func newHTTPAgent(client *apiClient, config agentConfig) (*httpAgent, error) {
	prefixBytes := make([]byte, 8)
	if _, err := rand.Read(prefixBytes); err != nil {
		return nil, fmt.Errorf("create request prefix: %w", err)
	}
	return &httpAgent{client: client, config: config, requestPrefix: hex.EncodeToString(prefixBytes), fixedMessages: make(map[string]bool)}, nil
}

func (a *httpAgent) run(ctx context.Context) error {
	started, err := a.client.start(ctx, startRunRequest{
		Seed: a.config.seed, AgentID: a.config.agentID, AgentVersion: a.config.agentVersion,
		RequestID: a.nextRequestID("start"),
	})
	if err != nil {
		return err
	}
	a.runID = started.RunID
	a.lastLogTime = started.SimulationTime
	fmt.Printf("run_id=%s seed=%d starts_at=%s ends_at=%s\n", a.runID, a.config.seed,
		started.SimulationTime.Format(time.RFC3339), started.SimulationEnds.Format(time.RFC3339))

	for a.stats.steps < a.config.maxSteps {
		overview, err := a.client.overview(ctx, a.runID)
		if err != nil {
			return err
		}
		logs, err := a.client.logs(ctx, a.runID, a.lastLogTime, overview.Clock.SimulationTime)
		if err != nil {
			return err
		}
		// The next interval starts strictly after this boundary, so a request logged
		// exactly at the boundary is not processed twice.
		a.lastLogTime = overview.Clock.SimulationTime.Add(time.Nanosecond)
		capacityFailures, fixes := a.inspectLogs(logs)
		if overview.Status != "running" {
			return a.printResult(ctx, overview.Status)
		}
		if err := a.applyFixes(ctx, fixes); err != nil {
			return err
		}
		if err := a.deploy(ctx); err != nil {
			return err
		}
		if err := a.scale(ctx, capacityFailures); err != nil {
			return err
		}

		remaining := overview.Clock.SimulationEndsAt.Sub(overview.Clock.SimulationTime)
		if remaining <= 0 {
			return a.printResult(ctx, overview.Status)
		}
		step := a.config.advance
		if step > remaining {
			step = remaining
		}
		advanced, err := a.client.advance(ctx, a.runID, a.nextRequestID("advance"), step)
		if err != nil {
			var apiErr *apiError
			if errors.As(err, &apiErr) && apiErr.Code == "RUN_COMPLETED" {
				return a.printResult(ctx, "completed")
			}
			return err
		}
		a.stats.steps++
		fmt.Printf("step=%d simulation_time=%s processed_events=%d new_logs=%d capacity_errors=%d\n",
			a.stats.steps, advanced.Clock.SimulationTime.Format(time.RFC3339), advanced.ProcessedEvents, advanced.NewLogs, capacityFailures)
	}
	return fmt.Errorf("agent stopped after safety limit of %d steps", a.config.maxSteps)
}

func (a *httpAgent) inspectLogs(entries []requestLog) (int, []string) {
	capacityFailures := 0
	fixes := make([]string, 0)
	for _, entry := range entries {
		if entry.Error == nil {
			continue
		}
		switch *entry.Error {
		case pageBugError:
			a.stats.bugErrors++
			if entry.Message == nil || !strings.HasPrefix(*entry.Message, fixMessagePrefix) {
				continue
			}
			message := strings.TrimPrefix(*entry.Message, fixMessagePrefix)
			if message != "" && !a.fixedMessages[message] {
				a.fixedMessages[message] = true
				fixes = append(fixes, message)
			}
		case capacityError:
			capacityFailures++
			a.stats.capacityErrors++
		}
	}
	sort.Strings(fixes)
	return capacityFailures, fixes
}

func (a *httpAgent) applyFixes(ctx context.Context, fixes []string) error {
	for _, message := range fixes {
		if err := a.client.applyFix(ctx, a.runID, a.nextRequestID("fix"), message); err != nil {
			return err
		}
		a.stats.fixes++
	}
	return nil
}

func (a *httpAgent) deploy(ctx context.Context) error {
	result, err := a.client.deployments(ctx, a.runID)
	if err != nil {
		return err
	}
	available := make([]deployment, 0)
	for _, item := range result.Deployments {
		if item.Status == deploymentAvailable {
			available = append(available, item)
		}
	}
	sort.Slice(available, func(i, j int) bool { return available[i].Sequence < available[j].Sequence })
	if len(available) == 0 {
		return nil
	}
	if err := a.client.startDeployment(ctx, a.runID, a.nextRequestID("deploy"), available[0].DeploymentID); err != nil {
		return err
	}
	a.stats.deploymentsStarted++
	return nil
}

func (a *httpAgent) scale(ctx context.Context, capacityFailures int) error {
	resources, err := a.client.resources(ctx, a.runID)
	if err != nil {
		return err
	}
	metrics, err := a.client.metrics(ctx, a.runID)
	if err != nil {
		return err
	}
	desired := resources.DesiredInstances
	switch {
	case capacityFailures > 0 && resources.DesiredInstances <= resources.ActiveInstances:
		desired++
	case capacityFailures == 0 && resources.DesiredInstances == resources.ActiveInstances &&
		resources.ActiveInstances > 1 && resources.UsedLoadUnits == 0 && metrics.Current.CapacityUtilization < 0.15:
		desired--
	}
	if desired == resources.DesiredInstances {
		return nil
	}
	if err := a.client.scale(ctx, a.runID, a.nextRequestID("scale"), desired); err != nil {
		return err
	}
	if desired > resources.DesiredInstances {
		a.stats.serversAdded += desired - resources.DesiredInstances
	} else {
		a.stats.serversRemoved += resources.DesiredInstances - desired
	}
	return nil
}

func (a *httpAgent) nextRequestID(kind string) string {
	a.requestNumber++
	return fmt.Sprintf("http-agent-%s-%s-%05d", a.requestPrefix, kind, a.requestNumber)
}

func (a *httpAgent) printResult(ctx context.Context, status string) error {
	economy, err := a.client.economy(ctx, a.runID)
	if err != nil {
		return err
	}
	fmt.Printf("status=%s\nsteps=%d\nbug_errors_seen=%d\nfixes_applied=%d\n", status, a.stats.steps, a.stats.bugErrors, a.stats.fixes)
	fmt.Printf("deployments_started=%d\ncapacity_errors_seen=%d\nservers_added=%d\nservers_removed=%d\n",
		a.stats.deploymentsStarted, a.stats.capacityErrors, a.stats.serversAdded, a.stats.serversRemoved)
	fmt.Printf("purchases=%d\nrevenue_minor=%d\nserver_cost_minor=%d\ndeployment_cost_minor=%d\nbalance_minor=%d\n",
		economy.SuccessfulPurchases, economy.RevenueMinor, economy.ServerCostMinor, economy.DeploymentCostMinor, economy.BalanceMinor)
	return nil
}
