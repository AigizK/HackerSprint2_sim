package generator

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const (
	EvaluatorVersion            = "world-evaluator.v1"
	DefaultAgentRequestDuration = 10 * time.Second
)

var ErrWorldCannotBeEvaluated = errors.New("world cannot be evaluated")

type OracleAction struct {
	Kind     string    `json:"kind"`
	TargetID string    `json:"target_id,omitempty"`
	At       time.Time `json:"at"`
}

// WorldEvaluation is an internal benchmark. OptimalPlan must never be exposed
// to an agent: it contains information from future world events.
type WorldEvaluation struct {
	EvaluatorVersion       string         `json:"evaluator_version"`
	MaximumBalanceMinor    int64          `json:"maximum_balance_minor"`
	MaximumRevenueMinor    int64          `json:"maximum_revenue_minor"`
	MinimumServerCostMinor int64          `json:"minimum_server_cost_minor"`
	MaximumPurchases       uint64         `json:"maximum_purchases"`
	RequiredServerCount    int            `json:"required_server_count"`
	AgentRequestCount      int            `json:"agent_request_count"`
	AgentRequestDuration   time.Duration  `json:"agent_request_duration"`
	MinimumRealTime        time.Duration  `json:"minimum_real_time"`
	OptimalPlan            []OracleAction `json:"optimal_plan"`
}

type EvaluationConfig struct {
	InitialBackendInstances int
	ServerCapacityUnits     int64
	ServerCostPerHourMinor  int64
	ServerProvisioningTime  time.Duration
	AgentRequestDuration    time.Duration
}

type WorldEvaluator struct{ config EvaluationConfig }

func NewWorldEvaluator(config EvaluationConfig) (*WorldEvaluator, error) {
	if config.InitialBackendInstances < 0 || config.ServerCapacityUnits <= 0 ||
		config.ServerCostPerHourMinor < 0 || config.ServerProvisioningTime <= 0 {
		return nil, fmt.Errorf("%w: invalid evaluator infrastructure", ErrWorldCannotBeEvaluated)
	}
	if config.AgentRequestDuration == 0 {
		config.AgentRequestDuration = DefaultAgentRequestDuration
	}
	if config.AgentRequestDuration < 0 {
		return nil, fmt.Errorf("%w: invalid agent request duration", ErrWorldCannotBeEvaluated)
	}
	return &WorldEvaluator{config: config}, nil
}

func (e *WorldEvaluator) Evaluate(world WorldDefinition) (WorldEvaluation, error) {
	if world.Key.Seed == 0 || !world.EndsAt.After(world.StartsAt) {
		return WorldEvaluation{}, fmt.Errorf("%w: invalid world clock or seed", ErrWorldCannotBeEvaluated)
	}
	products := make([]events.ProductAdded, 0)
	bugs := make([]events.PageBugActivated, 0)
	initialBalance := int64(0)
	pageLoads := make(map[model.PageType]int64)
	for _, event := range world.Bootstrap {
		switch event := event.(type) {
		case events.ProductAdded:
			products = append(products, event)
		case events.PageBugActivated:
			bugs = append(bugs, event)
		case events.EconomyConfigured:
			initialBalance = event.InitialBalanceMinor
		case events.PageConfigured:
			pageLoads[event.Page] = event.LoadUnits
		}
	}
	if initialBalance <= 0 || len(products) == 0 {
		return WorldEvaluation{}, fmt.Errorf("%w: economy or catalog is missing", ErrWorldCannotBeEvaluated)
	}
	sort.Slice(products, func(i, j int) bool { return products[i].ProductID < products[j].ProductID })
	sort.Slice(bugs, func(i, j int) bool { return bugs[i].BugID < bugs[j].BugID })

	requiredServers, err := minimumServersForJourney(pageLoads, e.config.ServerCapacityUnits)
	if err != nil {
		return WorldEvaluation{}, err
	}
	readyAt := world.StartsAt
	if e.config.InitialBackendInstances < requiredServers {
		readyAt = readyAt.Add(e.config.ServerProvisioningTime)
	}

	var maximumRevenue int64
	var maximumPurchases uint64
	neededBugs := make(map[model.BugID]events.PageBugActivated)
	for _, scheduled := range world.Events {
		visitor, ok := scheduled.Event.(events.VisitorArrived)
		if !ok || scheduled.OccursAt.Before(readyAt) ||
			(e.config.InitialBackendInstances < requiredServers && scheduled.OccursAt.Equal(readyAt)) {
			continue
		}
		product, selected := selectedProduct(world.Key.Seed, visitor.VisitorID, products)
		if !selected || deterministicEvaluationRoll(world.Key.Seed, string(visitor.VisitorID), "purchase") >= product.PurchaseProbabilityPPM {
			continue
		}
		maximumPurchases++
		maximumRevenue += product.PriceMinor
		for _, request := range []struct {
			page      model.PageType
			productID model.ProductID
			requestID model.RequestID
		}{
			{page: model.PageProductList, requestID: model.RequestID(string(visitor.VisitorID) + ":product_list")},
			{page: model.PageProduct, productID: product.ProductID, requestID: model.RequestID(string(visitor.VisitorID) + ":product_page")},
			{page: model.PagePurchase, productID: product.ProductID, requestID: model.RequestID(string(visitor.VisitorID) + ":purchase")},
		} {
			for _, bug := range bugs {
				if bug.Page == request.page && (bug.ProductID == "" || bug.ProductID == request.productID) &&
					evaluationProbabilityHit(world.Key.Seed, bug.BugID, request.requestID, bug.FailureProbabilityPPM) {
					neededBugs[bug.BugID] = bug
				}
			}
		}
	}

	serverCost := minimumServerCost(world, e.config, requiredServers, readyAt)
	plan := []OracleAction{{Kind: "start", At: world.StartsAt}}
	bugs = bugs[:0]
	for _, bug := range neededBugs {
		bugs = append(bugs, bug)
	}
	sort.Slice(bugs, func(i, j int) bool { return bugs[i].BugID < bugs[j].BugID })
	for _, bug := range bugs {
		plan = append(plan, OracleAction{Kind: "apply_fix", TargetID: string(bug.BugID), At: world.StartsAt})
	}
	if e.config.InitialBackendInstances != requiredServers {
		plan = append(plan, OracleAction{Kind: "set_backend_desired_instances", TargetID: fmt.Sprint(requiredServers), At: world.StartsAt})
	}
	if readyAt.After(world.StartsAt) {
		plan = append(plan, OracleAction{Kind: "advance_time", TargetID: readyAt.Format(time.RFC3339Nano), At: world.StartsAt})
	}
	plan = append(plan, OracleAction{Kind: "advance_time", TargetID: world.EndsAt.Format(time.RFC3339Nano), At: readyAt})

	requestCount := len(plan)
	return WorldEvaluation{
		EvaluatorVersion: EvaluatorVersion, MaximumBalanceMinor: initialBalance + maximumRevenue - serverCost,
		MaximumRevenueMinor: maximumRevenue, MinimumServerCostMinor: serverCost,
		MaximumPurchases: maximumPurchases, RequiredServerCount: requiredServers,
		AgentRequestCount: requestCount, AgentRequestDuration: e.config.AgentRequestDuration,
		MinimumRealTime: time.Duration(requestCount) * e.config.AgentRequestDuration,
		OptimalPlan:     plan,
	}, nil
}

func selectedProduct(seed int64, visitorID model.VisitorID, products []events.ProductAdded) (events.ProductAdded, bool) {
	roll := deterministicEvaluationRoll(seed, string(visitorID), "product")
	var cumulative uint64
	for _, product := range products {
		cumulative += uint64(product.ViewProbabilityPPM)
		if uint64(roll) < cumulative {
			return product, true
		}
	}
	return events.ProductAdded{}, false
}

func deterministicEvaluationRoll(seed int64, parts ...string) uint32 {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, strings.Join(parts, ":"))))
	return uint32(binary.BigEndian.Uint64(digest[:8]) % uint64(ProbabilityScale))
}

func evaluationProbabilityHit(seed int64, bugID model.BugID, requestID model.RequestID, probability uint32) bool {
	if probability == 0 {
		return false
	}
	if probability >= ProbabilityScale {
		return true
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", seed, bugID, requestID)))
	return binary.BigEndian.Uint64(digest[:8])%uint64(ProbabilityScale) < uint64(probability)
}

func minimumServersForJourney(loads map[model.PageType]int64, capacity int64) (int, error) {
	items := []int64{loads[model.PageProductList], loads[model.PageProduct], loads[model.PagePurchase]}
	for _, load := range items {
		if load <= 0 || load > capacity {
			return 0, fmt.Errorf("%w: page load %d cannot be served by capacity %d", ErrWorldCannotBeEvaluated, load, capacity)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i] > items[j] })
	for count := 1; count <= len(items); count++ {
		remaining := make([]int64, count)
		for i := range remaining {
			remaining[i] = capacity
		}
		if canPackLoads(items, remaining, 0) {
			return count, nil
		}
	}
	return 0, fmt.Errorf("%w: journey load cannot be packed", ErrWorldCannotBeEvaluated)
}

func canPackLoads(items, remaining []int64, index int) bool {
	if index == len(items) {
		return true
	}
	seen := make(map[int64]bool)
	for server := range remaining {
		if remaining[server] < items[index] || seen[remaining[server]] {
			continue
		}
		seen[remaining[server]] = true
		remaining[server] -= items[index]
		if canPackLoads(items, remaining, index+1) {
			return true
		}
		remaining[server] += items[index]
	}
	return false
}

func minimumServerCost(world WorldDefinition, config EvaluationConfig, required int, readyAt time.Time) int64 {
	if required == 0 {
		return 0
	}
	initialUsed := minInt(config.InitialBackendInstances, required)
	added := required - initialUsed
	return int64(initialUsed)*billedHours(world.EndsAt.Sub(world.StartsAt))*config.ServerCostPerHourMinor +
		int64(added)*billedHours(world.EndsAt.Sub(readyAt))*config.ServerCostPerHourMinor
}

func billedHours(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Hour - 1) / time.Hour)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
