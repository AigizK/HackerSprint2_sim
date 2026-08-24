package generator

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

const GeneratorVersion = "world-generator.v1"

var ErrGenerationLimit = errors.New("world generation limit exceeded")

type Generator struct {
	profile     WorldGenerationProfile
	profileHash string
}

func New(profile WorldGenerationProfile) (*Generator, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(payload)
	return &Generator{profile: profile, profileHash: hex.EncodeToString(hash[:])}, nil
}

func (g *Generator) ProfileHash() string { return g.profileHash }

func (g *Generator) Key(seed int64) (WorldKey, error) {
	if seed <= 0 {
		return WorldKey{}, fmt.Errorf("%w: generated seed must be positive", ErrInvalidProfile)
	}
	return WorldKey{Seed: seed, ProfileHash: g.profileHash, GeneratorVersion: GeneratorVersion}, nil
}

func (g *Generator) Generate(seed int64) (WorldDefinition, error) {
	key, err := g.Key(seed)
	if err != nil {
		return WorldDefinition{}, err
	}
	startsAt, _ := time.Parse(time.RFC3339, g.profile.Clock.StartsAt)
	endsAt, _ := time.Parse(time.RFC3339, g.profile.Clock.EndsAt)
	random := deterministicRandom{seed: seed, profileHash: g.profileHash, generatorVersion: GeneratorVersion}

	bootstrap, products, bugs, err := g.generateBootstrap(random, startsAt)
	if err != nil {
		return WorldDefinition{}, err
	}
	_ = products
	_ = bugs
	schedule, err := g.generateSchedule(random, startsAt, endsAt)
	if err != nil {
		return WorldDefinition{}, err
	}
	hash, err := hashWorldEvents(bootstrap, schedule)
	if err != nil {
		return WorldDefinition{}, err
	}
	worldDigest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", seed, g.profileHash, GeneratorVersion)))
	return WorldDefinition{
		WorldID: "w" + hex.EncodeToString(worldDigest[:16]), Key: key,
		ProfileVersion: g.profile.Version, ScheduleHash: hash, Source: "generated",
		StartsAt: startsAt, EndsAt: endsAt, CreatedAt: startsAt,
		Bootstrap: bootstrap, Events: schedule,
	}, nil
}

func (g *Generator) GetOrCreate(ctx context.Context, repository WorldRepository, seed int64) (WorldDefinition, bool, error) {
	key, err := g.Key(seed)
	if err != nil {
		return WorldDefinition{}, false, err
	}
	world, err := repository.FindWorld(ctx, key)
	if err == nil {
		return world, false, nil
	}
	if !errors.Is(err, ErrWorldNotFound) {
		return WorldDefinition{}, false, err
	}
	world, err = g.Generate(seed)
	if err != nil {
		return WorldDefinition{}, false, err
	}
	if err := repository.CreateWorld(ctx, world); err != nil {
		if errors.Is(err, ErrWorldAlreadyExists) {
			world, err = repository.FindWorld(ctx, key)
			return world, false, err
		}
		return WorldDefinition{}, false, err
	}
	return world, true, nil
}

type generatedProduct struct {
	id                  model.ProductID
	price               int64
	viewProbability     uint32
	purchaseProbability uint32
}

type generatedBug struct {
	id          model.BugID
	probability uint32
}

func (g *Generator) generateBootstrap(random deterministicRandom, at time.Time) ([]events.Event, []generatedProduct, []generatedBug, error) {
	result := make([]events.Event, 0)
	pageStates := make(map[model.PageType]PageConfig)
	for _, page := range []model.PageType{model.PageProductList, model.PageProduct, model.PagePurchase} {
		config := g.profile.Infrastructure.Pages[string(page)]
		load := random.int64Range(config.LoadUnits, "page:"+string(page)+":load")
		hold := random.int64Range(config.ResourceHoldSeconds, "page:"+string(page)+":hold")
		pageStates[page] = PageConfig{LoadUnits: IntRange{Min: load, Max: load}, ResourceHoldSeconds: IntRange{Min: hold, Max: hold}}
		result = append(result, events.PageConfigured{Page: page, LoadUnits: load, HoldDuration: time.Duration(hold) * time.Second, ConfiguredAt: at})
	}
	result = append(result,
		events.InfrastructureConfigured{ServerProvisioningDuration: time.Duration(g.profile.Infrastructure.ServerProvisioningSeconds) * time.Second, ConfiguredAt: at},
		events.EconomyConfigured{InitialBalanceMinor: g.profile.Economy.InitialBalanceMinor, StopRunOnNegativeBalance: g.profile.Economy.StopRunWhenBalanceIsNegative, ServerBillingPeriod: time.Duration(g.profile.Economy.ServerBillingPeriodSeconds) * time.Second, ConfiguredAt: at},
	)
	for index := 1; index <= g.profile.Infrastructure.InitialBackendInstances; index++ {
		serverID := model.ServerID(fmt.Sprintf("server-initial-%d", index))
		operationID := model.OperationID("initial-" + string(serverID))
		result = append(result,
			events.ServerProvisioningStarted{OperationID: operationID, ServerID: serverID, CapacityUnits: g.profile.Infrastructure.ServerCapacityUnits, CostPerHourMinor: g.profile.Infrastructure.ServerCostPerHourMinor, StartedAt: at, ReadyAt: at},
			events.ServerActivated{OperationID: operationID, ServerID: serverID, ActivatedAt: at},
		)
	}

	productCount := int(random.int64Range(g.profile.Catalog.ProductCount, "catalog:product_count"))
	weights := make([]int64, productCount)
	var totalWeight int64
	for index := range weights {
		weights[index] = random.int64Range(g.profile.Catalog.PopularityWeight, fmt.Sprintf("product:%d:popularity", index+1))
		totalWeight += weights[index]
	}
	viewProbabilities := distributeProbability(g.profile.Journey.CatalogToProductProbabilityPPM, weights, totalWeight)
	products := make([]generatedProduct, 0, productCount)
	for index := 0; index < productCount; index++ {
		number := index + 1
		id := model.ProductID(fmt.Sprintf("product-%03d", number))
		price := random.int64Range(g.profile.Catalog.PriceMinor, fmt.Sprintf("product:%d:price", number))
		purchase := random.ppmRange(g.profile.Journey.ProductToPurchaseProbability, fmt.Sprintf("product:%d:purchase_probability", number))
		name := g.profile.Catalog.NameTemplates[index%len(g.profile.Catalog.NameTemplates)] + fmt.Sprintf(" %03d", number)
		products = append(products, generatedProduct{id: id, price: price, viewProbability: viewProbabilities[index], purchaseProbability: purchase})
		result = append(result, events.ProductAdded{ProductID: id, Name: name, PriceMinor: price, ViewProbabilityPPM: viewProbabilities[index], PurchaseProbabilityPPM: purchase, AddedAt: at})
	}

	bugProducts := make([]int, 0, len(products))
	remainingProducts := make([]int, 0, len(products))
	for productIndex := range products {
		if random.hit(g.profile.Bugs.ProductHasBugProbabilityPPM, fmt.Sprintf("product:%d:has_bug", productIndex+1)) {
			bugProducts = append(bugProducts, productIndex)
		} else {
			remainingProducts = append(remainingProducts, productIndex)
		}
	}
	sort.Slice(bugProducts, func(i, j int) bool {
		return random.uint64(fmt.Sprintf("product:%d:bug_rank", bugProducts[i]+1)) < random.uint64(fmt.Sprintf("product:%d:bug_rank", bugProducts[j]+1))
	})
	sort.Slice(remainingProducts, func(i, j int) bool {
		return random.uint64(fmt.Sprintf("product:%d:bug_rank", remainingProducts[i]+1)) < random.uint64(fmt.Sprintf("product:%d:bug_rank", remainingProducts[j]+1))
	})
	for int64(len(bugProducts)) < g.profile.Bugs.InitialBugCount.Min && len(remainingProducts) > 0 {
		bugProducts = append(bugProducts, remainingProducts[0])
		remainingProducts = remainingProducts[1:]
	}
	if int64(len(bugProducts)) > g.profile.Bugs.InitialBugCount.Max {
		bugProducts = bugProducts[:g.profile.Bugs.InitialBugCount.Max]
	}
	bugs := make([]generatedBug, 0, len(bugProducts))
	for bugOffset, productIndex := range bugProducts {
		index := bugOffset + 1
		id := model.BugID(fmt.Sprintf("bug-initial-%03d", index))
		page := model.PageType(random.weighted(g.profile.Bugs.PageWeights, fmt.Sprintf("bug:%d:page", index)))
		productID := model.ProductID("")
		if page != model.PageProductList {
			productID = products[productIndex].id
		}
		probability := random.ppmRange(g.profile.Bugs.TriggerProbability, fmt.Sprintf("bug:%d:trigger", index))
		fixMessage := "FIX-" + strings.ToUpper(random.token(fmt.Sprintf("bug:%d:fix", index), g.profile.Bugs.FixTokenLength))
		fixHash := sha256.Sum256([]byte(fixMessage))
		bugs = append(bugs, generatedBug{id: id, probability: probability})
		result = append(result, events.PageBugActivated{BugID: id, Page: page, ProductID: productID, FailureProbabilityPPM: probability, FixMessage: fixMessage, FixMessageHash: hex.EncodeToString(fixHash[:]), ActivatedAt: at})
	}

	deploymentEvents := g.generateDeployments(random, at, pageStates, products, bugs)
	result = append(result, deploymentEvents...)
	return result, products, bugs, nil
}

func (g *Generator) generateDeployments(random deterministicRandom, at time.Time, pages map[model.PageType]PageConfig, products []generatedProduct, bugs []generatedBug) []events.Event {
	count := int(random.int64Range(g.profile.Deployments.Count, "deployments:count"))
	result := make([]events.Event, 0)
	for index := 1; index <= count; index++ {
		deploymentID := model.DeploymentID(fmt.Sprintf("deployment-%03d", index))
		result = append(result, events.DeploymentDefined{
			DeploymentID: deploymentID, Sequence: index, Name: fmt.Sprintf("Generated deployment %03d", index), Description: fmt.Sprintf("Generated deployment %03d", index),
			CostMinor:             random.int64Range(g.profile.Deployments.CostMinor, fmt.Sprintf("deployment:%d:cost", index)),
			Duration:              time.Duration(random.int64Range(g.profile.Deployments.DurationSeconds, fmt.Sprintf("deployment:%d:duration", index))) * time.Second,
			FailureProbabilityPPM: random.ppmRange(g.profile.Deployments.FailureProbability, fmt.Sprintf("deployment:%d:failure", index)), DefinedAt: at,
		})
		effectCount := int(random.int64Range(g.profile.Deployments.EffectsPerDeployment, fmt.Sprintf("deployment:%d:effect_count", index)))
		for effectIndex := 0; effectIndex < effectCount; effectIndex++ {
			namespace := fmt.Sprintf("deployment:%d:effect:%d", index, effectIndex+1)
			switch effectIndex % 3 {
			case 0:
				page := model.PageType(random.weighted(g.profile.Deployments.NewBugPageWeights, namespace+":page"))
				current := pages[page]
				loadReduction := random.ppmRange(g.profile.Deployments.LoadReduction, namespace+":load_reduction")
				holdReduction := random.ppmRange(g.profile.Deployments.RequestHoldReduction, namespace+":hold_reduction")
				newLoad := maxInt64(1, current.LoadUnits.Min*int64(ProbabilityScale-loadReduction)/int64(ProbabilityScale))
				newHold := maxInt64(1, current.ResourceHoldSeconds.Min*int64(ProbabilityScale-holdReduction)/int64(ProbabilityScale))
				pages[page] = PageConfig{LoadUnits: IntRange{Min: newLoad, Max: newLoad}, ResourceHoldSeconds: IntRange{Min: newHold, Max: newHold}}
				result = append(result, events.DeploymentPageLoadEffectDefined{DeploymentID: deploymentID, Page: page, NewLoadUnits: newLoad, NewHoldDuration: time.Duration(newHold) * time.Second, DefinedAt: at})
			case 1:
				reduction := random.ppmRange(g.profile.Deployments.FutureDeploymentDurationReduction, namespace+":duration_reduction")
				result = append(result, events.DeploymentFutureDurationEffectDefined{DeploymentID: deploymentID, ReductionPPM: reduction, MinimumDuration: time.Duration(g.profile.Deployments.MinimumFutureDeploymentSeconds) * time.Second, DefinedAt: at})
			case 2:
				if len(bugs) > 0 {
					bug := bugs[int(random.uint64(namespace+":bug")%uint64(len(bugs)))]
					reduction := random.ppmRange(g.profile.Deployments.OldBugProbabilityReduction, namespace+":bug_reduction")
					newProbability := uint32(uint64(bug.probability) * uint64(ProbabilityScale-reduction) / uint64(ProbabilityScale))
					result = append(result, events.DeploymentBugProbabilityEffectDefined{DeploymentID: deploymentID, BugID: bug.id, NewProbabilityPPM: newProbability, DefinedAt: at})
				}
			}
		}
		if random.hit(g.profile.Deployments.NewBugProbabilityPPM, fmt.Sprintf("deployment:%d:new_bugs", index)) {
			newBugCount := int(random.int64Range(g.profile.Deployments.NewBugCount, fmt.Sprintf("deployment:%d:new_bug_count", index)))
			for bugIndex := 1; bugIndex <= newBugCount; bugIndex++ {
				namespace := fmt.Sprintf("deployment:%d:new_bug:%d", index, bugIndex)
				bugID := model.BugID(fmt.Sprintf("bug-deployment-%03d-%03d", index, bugIndex))
				page := model.PageType(random.weighted(g.profile.Deployments.NewBugPageWeights, namespace+":page"))
				productID := model.ProductID("")
				if page != model.PageProductList {
					productID = products[int(random.uint64(namespace+":product")%uint64(len(products)))].id
				}
				fixMessage := "FIX-" + strings.ToUpper(random.token(namespace+":fix", g.profile.Bugs.FixTokenLength))
				fixHash := sha256.Sum256([]byte(fixMessage))
				result = append(result, events.DeploymentNewBugEffectDefined{
					DeploymentID: deploymentID, BugID: bugID, Page: page, ProductID: productID,
					FailureProbabilityPPM: random.ppmRange(g.profile.Deployments.NewBugTriggerProbability, namespace+":trigger"),
					FixMessage:            fixMessage, FixMessageHash: hex.EncodeToString(fixHash[:]), DefinedAt: at,
				})
			}
		}
	}
	if count > 0 {
		result = append(result, events.DeploymentUnlocked{DeploymentID: "deployment-001", UnlockedAt: at})
	}
	return result
}

func (g *Generator) generateSchedule(random deterministicRandom, startsAt, endsAt time.Time) (events.EventSchedule, error) {
	location, _ := time.LoadLocation(g.profile.Clock.Timezone)
	schedule := make(events.EventSchedule, 0)
	visitorCount := int64(0)
	for hour := startsAt.Truncate(time.Hour); hour.Before(endsAt); hour = hour.Add(time.Hour) {
		local := hour.In(location)
		count := g.profile.Traffic.BaseArrivalsPerHour
		count = multiplyPPM(count, g.profile.Traffic.HourlyMultipliers[local.Hour()])
		count = multiplyPPM(count, g.profile.Traffic.WeekdayMultipliers[strings.ToLower(local.Weekday().String())])
		count = multiplyPPM(count, specialDayMultiplier(g.profile.Traffic.SpecialDays, local))
		noise := random.ppmRange(g.profile.Traffic.NoiseMultiplier, "traffic:"+hour.Format(time.RFC3339)+":noise")
		count = multiplyPPM(count, noise)
		if count > g.profile.Traffic.MaxArrivalsPerHour {
			count = g.profile.Traffic.MaxArrivalsPerHour
		}
		if visitorCount+count > g.profile.Limits.MaxVisitors {
			return nil, fmt.Errorf("%w: visitors exceed %d", ErrGenerationLimit, g.profile.Limits.MaxVisitors)
		}
		for index := int64(0); index < count; index++ {
			namespace := fmt.Sprintf("traffic:%s:visitor:%d", hour.Format(time.RFC3339), index)
			offset := time.Duration(random.uint64(namespace+":offset") % uint64(time.Hour))
			arrivedAt := hour.Add(offset)
			if arrivedAt.Before(startsAt) || !arrivedAt.Before(endsAt) {
				continue
			}
			visitorID := model.VisitorID("v" + random.token(namespace+":id", 24))
			schedule = append(schedule, events.ScheduledWorldEvent{OccursAt: arrivedAt, Event: events.VisitorArrived{VisitorID: visitorID, ArrivedAt: arrivedAt}})
			visitorCount++
		}
	}
	if g.profile.DDoS.Enabled {
		attackCount := int(random.int64Range(g.profile.DDoS.AttackCount, "ddos:count"))
		for index := 1; index <= attackCount; index++ {
			namespace := fmt.Sprintf("ddos:%d", index)
			kind := g.profile.DDoS.Kinds[random.weightedKind(g.profile.DDoS.Kinds, namespace+":kind")]
			duration := time.Duration(random.int64Range(kind.DurationSeconds, namespace+":duration")) * time.Second
			available := endsAt.Sub(startsAt) - duration
			if available <= 0 {
				return nil, fmt.Errorf("%w: DDoS duration exceeds world", ErrInvalidProfile)
			}
			startedAt := startsAt.Add(time.Duration(random.uint64(namespace+":start") % uint64(available)))
			endedAt := startedAt.Add(duration)
			attackID := model.AttackID(fmt.Sprintf("attack-%03d-%s", index, kind.ID))
			fixMessage, fixHash := "", ""
			if kind.Resolution == string(model.AttackFixOrExpiry) {
				fixMessage = "MITIGATE-" + strings.ToUpper(random.token(namespace+":fix", kind.FixTokenLength))
				hash := sha256.Sum256([]byte(fixMessage))
				fixHash = hex.EncodeToString(hash[:])
			}
			page := model.PageType(random.weighted(g.profile.DDoS.TargetPageWeights, namespace+":page"))
			schedule = append(schedule,
				events.ScheduledWorldEvent{OccursAt: startedAt, Event: events.TrafficAttackStarted{
					AttackID: attackID, Kind: model.AttackDDoS, TargetPage: page,
					RequestsPerMinute:   random.int64Range(kind.RequestsPerMinute, namespace+":rpm"),
					LoadUnitsPerRequest: random.int64Range(kind.LoadUnitsPerRequest, namespace+":load"),
					Resolution:          model.AttackResolution(kind.Resolution), ExpectedEndAt: endedAt,
					FixMessage: fixMessage, FixMessageHash: fixHash, StartedAt: startedAt,
				}},
				events.ScheduledWorldEvent{OccursAt: endedAt, Event: events.TrafficAttackEnded{AttackID: attackID, EndedAt: endedAt}},
			)
		}
	}
	if int64(len(schedule)) > g.profile.Limits.MaxScheduledEvents {
		return nil, fmt.Errorf("%w: events exceed %d", ErrGenerationLimit, g.profile.Limits.MaxScheduledEvents)
	}
	sort.SliceStable(schedule, func(i, j int) bool {
		if !schedule[i].OccursAt.Equal(schedule[j].OccursAt) {
			return schedule[i].OccursAt.Before(schedule[j].OccursAt)
		}
		leftType, leftPayload, _ := simulation.EncodeEvent(schedule[i].Event)
		rightType, rightPayload, _ := simulation.EncodeEvent(schedule[j].Event)
		if leftType != rightType {
			return leftType < rightType
		}
		return string(leftPayload) < string(rightPayload)
	})
	for index := range schedule {
		schedule[index].Sequence = uint64(index + 1)
	}
	return schedule, nil
}

type deterministicRandom struct {
	seed                          int64
	profileHash, generatorVersion string
}

func (r deterministicRandom) uint64(namespace string) uint64 {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s:%s", r.seed, r.profileHash, r.generatorVersion, namespace)))
	return binary.BigEndian.Uint64(hash[:8])
}
func (r deterministicRandom) int64Range(value IntRange, namespace string) int64 {
	if value.Min == value.Max {
		return value.Min
	}
	return value.Min + int64(r.uint64(namespace)%uint64(value.Max-value.Min+1))
}
func (r deterministicRandom) ppmRange(value PPMRange, namespace string) uint32 {
	if value.Min == value.Max {
		return value.Min
	}
	return value.Min + uint32(r.uint64(namespace)%uint64(value.Max-value.Min+1))
}
func (r deterministicRandom) hit(probability uint32, namespace string) bool {
	return probability >= ProbabilityScale || uint32(r.uint64(namespace)%uint64(ProbabilityScale)) < probability
}
func (r deterministicRandom) token(namespace string, length int) string {
	result := ""
	for index := 0; len(result) < length; index++ {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s:%s:%d", r.seed, r.profileHash, r.generatorVersion, namespace, index)))
		result += hex.EncodeToString(hash[:])
	}
	return result[:length]
}
func (r deterministicRandom) weighted(weights map[string]uint32, namespace string) string {
	keys := make([]string, 0, len(weights))
	var total uint64
	for key, weight := range weights {
		keys = append(keys, key)
		total += uint64(weight)
	}
	sort.Strings(keys)
	roll := r.uint64(namespace) % total
	var cumulative uint64
	for _, key := range keys {
		cumulative += uint64(weights[key])
		if roll < cumulative {
			return key
		}
	}
	return keys[len(keys)-1]
}
func (r deterministicRandom) weightedKind(kinds []DDoSKindConfig, namespace string) int {
	var total uint64
	for _, kind := range kinds {
		total += uint64(kind.Weight)
	}
	roll := r.uint64(namespace) % total
	var cumulative uint64
	for index, kind := range kinds {
		cumulative += uint64(kind.Weight)
		if roll < cumulative {
			return index
		}
	}
	return len(kinds) - 1
}

func distributeProbability(total uint32, weights []int64, totalWeight int64) []uint32 {
	result := make([]uint32, len(weights))
	var used uint32
	for i, w := range weights {
		result[i] = uint32(uint64(total) * uint64(w) / uint64(totalWeight))
		used += result[i]
	}
	for i := 0; used < total; i = (i + 1) % len(result) {
		result[i]++
		used++
	}
	return result
}
func multiplyPPM(value int64, multiplier uint32) int64 {
	return value * int64(multiplier) / int64(ProbabilityScale)
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func specialDayMultiplier(days []SpecialDayConfig, local time.Time) uint32 {
	result := uint32(ProbabilityScale)
	for _, day := range days {
		var start time.Time
		if day.Rule == "fixed_date" {
			start = time.Date(local.Year(), time.Month(day.Month), day.Day, 0, 0, 0, 0, local.Location())
		} else {
			start = blackFriday(local.Year(), local.Location())
		}
		if !local.Before(start) && local.Before(start.Add(time.Duration(day.DurationHours)*time.Hour)) {
			result = uint32(uint64(result) * uint64(day.TrafficMultiplierPPM) / uint64(ProbabilityScale))
		}
	}
	return result
}
func blackFriday(year int, location *time.Location) time.Time {
	first := time.Date(year, time.November, 1, 0, 0, 0, 0, location)
	offset := (int(time.Thursday) - int(first.Weekday()) + 7) % 7
	thanksgiving := first.AddDate(0, 0, offset+21)
	return thanksgiving.AddDate(0, 0, 1)
}
func hashWorldEvents(bootstrap []events.Event, schedule events.EventSchedule) (string, error) {
	hash := sha256.New()
	for index, event := range bootstrap {
		eventType, payload, err := simulation.EncodeEvent(event)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "b:%d:%s:", index+1, eventType)
		hash.Write(payload)
		hash.Write([]byte{'\n'})
	}
	for _, scheduled := range schedule {
		eventType, payload, err := simulation.EncodeEvent(scheduled.Event)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "s:%d:%s:%s:", scheduled.Sequence, scheduled.OccursAt.UTC().Format(time.RFC3339Nano), eventType)
		hash.Write(payload)
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
