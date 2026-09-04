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

const (
	GeneratorVersion       = "world-generator.v12"
	generatorRandomVersion = "world-generator.v12"
)

var (
	ErrGenerationLimit = errors.New("world generation limit exceeded")
	ErrWorldIntegrity  = errors.New("regenerated world does not match catalog")
)

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

func (g *Generator) ProfileHash() string             { return g.profileHash }
func (g *Generator) Profile() WorldGenerationProfile { return g.profile }

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
	// This version starts a new read-only service world format.
	random := deterministicRandom{seed: seed, generatorVersion: generatorRandomVersion}
	startsAt, endsAt, err := g.worldClock(random)
	if err != nil {
		return WorldDefinition{}, err
	}

	bootstrap := g.generateBootstrap(random, startsAt)
	schedule, err := g.generateSchedule(random, startsAt, endsAt)
	if err != nil {
		return WorldDefinition{}, err
	}
	hash, err := hashWorldEvents(bootstrap, schedule)
	if err != nil {
		return WorldDefinition{}, err
	}
	worldDigest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", seed, g.profileHash, GeneratorVersion)))
	world := WorldDefinition{
		WorldID: "w" + hex.EncodeToString(worldDigest[:16]), Key: key,
		ProfileVersion: g.profile.Version, ScheduleHash: hash, Source: "generated",
		StartsAt: startsAt, EndsAt: endsAt, CreatedAt: startsAt,
		Bootstrap: bootstrap, Events: schedule,
	}
	return world, nil
}

func (g *Generator) worldClock(random deterministicRandom) (time.Time, time.Time, error) {
	windowStart, _ := time.Parse(time.RFC3339, g.profile.Clock.StartsAt)
	windowEnd, _ := time.Parse(time.RFC3339, g.profile.Clock.EndsAt)
	location, _ := time.LoadLocation(g.profile.Clock.Timezone)
	duration := time.Duration(g.profile.Clock.SimulationDurationDays) * 24 * time.Hour
	localStart := windowStart.In(location)
	first := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 0, 0, 0, 0, location)
	for first.Weekday() != time.Monday || first.UTC().Before(windowStart) {
		first = first.AddDate(0, 0, 1)
	}
	candidates := make([]time.Time, 0, 53)
	for candidate := first.UTC(); !candidate.Add(duration).After(windowEnd); candidate = candidate.AddDate(0, 0, 7) {
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: no calendar week fits clock selection window", ErrInvalidProfile)
	}
	startsAt := candidates[random.uint64("clock:start_week")%uint64(len(candidates))]
	return startsAt, startsAt.Add(duration), nil
}

func (g *Generator) GetOrCreate(ctx context.Context, repository WorldRepository, seed int64) (WorldDefinition, bool, error) {
	key, err := g.Key(seed)
	if err != nil {
		return WorldDefinition{}, false, err
	}
	world, err := repository.FindWorld(ctx, key)
	if err == nil {
		regenerated, generationErr := g.Generate(seed)
		if generationErr != nil {
			return WorldDefinition{}, false, generationErr
		}
		if err := verifyCatalogWorld(world, regenerated); err != nil {
			return WorldDefinition{}, false, err
		}
		return regenerated, false, nil
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
			catalogWorld, findErr := repository.FindWorld(ctx, key)
			if findErr != nil {
				return WorldDefinition{}, false, findErr
			}
			if err := verifyCatalogWorld(catalogWorld, world); err != nil {
				return WorldDefinition{}, false, err
			}
			return world, false, nil
		}
		return WorldDefinition{}, false, err
	}
	return world, true, nil
}

func verifyCatalogWorld(catalog, generated WorldDefinition) error {
	if catalog.WorldID != generated.WorldID || catalog.ScheduleHash != generated.ScheduleHash ||
		catalog.ProfileVersion != generated.ProfileVersion || catalog.Source != generated.Source ||
		!catalog.StartsAt.Equal(generated.StartsAt) || !catalog.EndsAt.Equal(generated.EndsAt) {
		return fmt.Errorf("%w: seed %d, catalog schedule %s, generated schedule %s",
			ErrWorldIntegrity, generated.Key.Seed, catalog.ScheduleHash, generated.ScheduleHash)
	}
	return nil
}

// HashWorldEvents exposes the same canonical hash used by generated worlds so
// operator-created negative-seed worlds can be registered safely.
func HashWorldEvents(bootstrap []events.Event, schedule events.EventSchedule) (string, error) {
	return hashWorldEvents(bootstrap, schedule)
}

func (g *Generator) generateSchedule(random deterministicRandom, startsAt, endsAt time.Time) (events.EventSchedule, error) {
	location, _ := time.LoadLocation(g.profile.Clock.Timezone)
	schedule := make(events.EventSchedule, 0)
	visitorCount := int64(0)
	attackCount := 0
	if g.profile.DDoS.Enabled {
		attackCount = int(random.int64Range(g.profile.DDoS.AttackCount, "ddos:count"))
	}
	visitorTarget := g.profile.Traffic.TargetScheduledEvents - int64(2*attackCount) -
		g.profile.Infrastructure.DatabaseGrowthEvents - g.profile.Infrastructure.BackendLogGrowthEvents -
		g.profile.Credentials.RotationEvents
	if visitorTarget <= 0 || visitorTarget > g.profile.Limits.MaxVisitors {
		return nil, fmt.Errorf("%w: visitor target %d is outside limits", ErrGenerationLimit, visitorTarget)
	}
	type hourAllocation struct {
		hour      time.Time
		weight    int64
		count     int64
		remainder int64
	}
	allocations := make([]hourAllocation, 0, int(endsAt.Sub(startsAt)/time.Hour)+1)
	var totalWeight int64
	for hour := startsAt.Truncate(time.Hour); hour.Before(endsAt); hour = hour.Add(time.Hour) {
		local := hour.In(location)
		expectedPPM := g.profile.Traffic.BaseArrivalsPerHour * int64(ProbabilityScale)
		expectedPPM = scaleFixedPPM(expectedPPM, g.profile.Traffic.HourlyMultipliers[local.Hour()])
		expectedPPM = scaleFixedPPM(expectedPPM, g.profile.Traffic.WeekdayMultipliers[strings.ToLower(local.Weekday().String())])
		expectedPPM = scaleFixedPPM(expectedPPM, specialDayMultiplier(g.profile.Traffic.SpecialDays, local))
		noise := random.ppmRange(g.profile.Traffic.NoiseMultiplier, "traffic:"+hour.Format(time.RFC3339)+":noise")
		expectedPPM = scaleFixedPPM(expectedPPM, noise)
		if expectedPPM <= 0 {
			continue
		}
		allocations = append(allocations, hourAllocation{hour: hour, weight: expectedPPM})
		totalWeight += expectedPPM
	}
	if totalWeight <= 0 {
		return nil, fmt.Errorf("%w: traffic weights are empty", ErrInvalidProfile)
	}
	var allocated int64
	for index := range allocations {
		scaled := visitorTarget * allocations[index].weight
		allocations[index].count = scaled / totalWeight
		allocations[index].remainder = scaled % totalWeight
		allocated += allocations[index].count
	}
	remaining := visitorTarget - allocated
	ranked := make([]int, len(allocations))
	for index := range ranked {
		ranked[index] = index
	}
	sort.Slice(ranked, func(i, j int) bool {
		left, right := allocations[ranked[i]], allocations[ranked[j]]
		if left.remainder != right.remainder {
			return left.remainder > right.remainder
		}
		return random.uint64("traffic:allocation:"+left.hour.Format(time.RFC3339)) < random.uint64("traffic:allocation:"+right.hour.Format(time.RFC3339))
	})
	for index := int64(0); index < remaining; index++ {
		allocations[ranked[index]].count++
	}
	surgeHour := time.Time{}
	clusteredVisitors := g.profile.Infrastructure.BackendSurgeVisitors + g.profile.Infrastructure.DatabaseConnectionSurgeVisitors
	if clusteredVisitors > 0 {
		// Keep this independent of generated DDoS attacks, whose window starts
		// at +12h. A normal visitor burst demonstrates why a second backend is
		// useful even when the firewall is not involved.
		var surgeHourVisitors int64
		for _, allocation := range allocations {
			if !allocation.hour.Before(startsAt.Add(12*time.Hour)) || allocation.count < clusteredVisitors {
				continue
			}
			if surgeHour.IsZero() || allocation.count > surgeHourVisitors {
				surgeHour = allocation.hour
				surgeHourVisitors = allocation.count
			}
		}
		if surgeHour.IsZero() {
			return nil, fmt.Errorf("%w: no first-half-day hour can host %d clustered visitors", ErrGenerationLimit, clusteredVisitors)
		}
	}
	surgeLoad := int64(0)
	listLoad := random.int64Range(g.profile.Infrastructure.Pages["product_list"].LoadUnits, "page:product_list:load")
	productLoad := random.int64Range(g.profile.Infrastructure.Pages["product_page"].LoadUnits, "page:product_page:load")
	for _, allocation := range allocations {
		hour, count := allocation.hour, allocation.count
		if count > g.profile.Traffic.MaxArrivalsPerHour {
			return nil, fmt.Errorf("%w: hour %s needs %d visitors, max is %d", ErrGenerationLimit, hour.Format(time.RFC3339), count, g.profile.Traffic.MaxArrivalsPerHour)
		}
		if visitorCount+count > g.profile.Limits.MaxVisitors {
			return nil, fmt.Errorf("%w: visitors exceed %d", ErrGenerationLimit, g.profile.Limits.MaxVisitors)
		}
		hourVisitors := make(events.EventSchedule, 0, count)
		for index := int64(0); index < count; index++ {
			namespace := fmt.Sprintf("traffic:%s:visitor:%d", hour.Format(time.RFC3339), index)
			visitorID := model.VisitorID("v" + random.token(namespace+":id", 24))
			offset := time.Duration(random.uint64(namespace+":offset") % uint64(time.Hour))
			if allocation.hour.Equal(surgeHour) && index < g.profile.Infrastructure.BackendSurgeVisitors && surgeLoad <= g.profile.Infrastructure.ServerCapacityUnits {
				offset = 30 * time.Minute
				surgeLoad += listLoad
				if deterministicVisitorRoll(random.seed, visitorID, "product") < g.profile.Journey.CatalogToProductProbabilityPPM {
					surgeLoad += productLoad
				}
			}
			if allocation.hour.Equal(surgeHour) && index >= g.profile.Infrastructure.BackendSurgeVisitors && index < clusteredVisitors {
				offset = 45 * time.Minute
			}
			arrivedAt := hour.Add(offset)
			if arrivedAt.Before(startsAt) || !arrivedAt.Before(endsAt) {
				continue
			}
			regions := [...]model.RegionCode{"RU", "US", "KZ", "DE", "ZZ"}
			agents := [...]string{"Mozilla/5.0 SimBrowser/1.0", "MobileApp/2.0", "WebCamera/1.0"}
			sourceIP := fmt.Sprintf("198.18.%d.%d", random.uint64(namespace+":ip-subnet")%256, 1+random.uint64(namespace+":ip-host")%254)
			hourVisitors = append(hourVisitors, events.ScheduledWorldEvent{OccursAt: arrivedAt, Event: events.VisitorArrived{
				VisitorID: visitorID, SourceIP: sourceIP,
				UserAgent:  agents[random.uint64(namespace+":user-agent")%uint64(len(agents))],
				RegionCode: regions[random.uint64(namespace+":region")%uint64(len(regions))], ArrivedAt: arrivedAt}})
			visitorCount++
		}
		sort.Slice(hourVisitors, func(i, j int) bool { return scheduledBefore(hourVisitors[i], hourVisitors[j]) })
		schedule = append(schedule, hourVisitors...)
	}
	if !surgeHour.IsZero() && surgeLoad <= g.profile.Infrastructure.ServerCapacityUnits {
		return nil, fmt.Errorf("%w: generated backend surge uses only %d of %d capacity units", ErrGenerationLimit, surgeLoad, g.profile.Infrastructure.ServerCapacityUnits)
	}
	incidents := make(events.EventSchedule, 0, 2*attackCount)
	if g.profile.DDoS.Enabled {
		for index := 1; index <= attackCount; index++ {
			namespace := fmt.Sprintf("ddos:%d", index)
			kind := g.profile.DDoS.Kinds[random.weightedKind(g.profile.DDoS.Kinds, namespace+":kind")]
			duration := time.Duration(random.int64Range(kind.DurationSeconds, namespace+":duration")) * time.Second
			attackWindowStart := startsAt.Add(12 * time.Hour)
			available := endsAt.Sub(attackWindowStart) - duration
			if available <= 0 {
				return nil, fmt.Errorf("%w: DDoS duration exceeds world", ErrInvalidProfile)
			}
			startedAt := attackWindowStart.Add(time.Duration(random.uint64(namespace+":start") % uint64(available)))
			endedAt := startedAt.Add(duration)
			attackID := model.AttackID(fmt.Sprintf("attack-%03d-%s", index, kind.ID))
			page := model.PageType(random.weighted(g.profile.DDoS.TargetPageWeights, namespace+":page"))
			incidents = append(incidents,
				events.ScheduledWorldEvent{OccursAt: startedAt, Event: events.TrafficAttackStarted{
					AttackID: attackID, Kind: model.AttackDDoS, TargetPage: page,
					RequestsPerMinute:   random.int64Range(kind.RequestsPerMinute, namespace+":rpm"),
					LoadUnitsPerRequest: random.int64Range(kind.LoadUnitsPerRequest, namespace+":load"),
					SourceCIDR:          kind.SourceCIDR,
					UserAgent:           kind.UserAgent,
					RegionCode:          model.RegionCode(kind.RegionCode),
					Resolution:          model.AttackResolution(kind.Resolution), ExpectedEndAt: endedAt,
					StartedAt: startedAt,
				}},
				events.ScheduledWorldEvent{OccursAt: endedAt, Event: events.TrafficAttackEnded{AttackID: attackID, EndedAt: endedAt}},
			)
		}
	}
	for index := int64(1); index <= g.profile.Infrastructure.DatabaseGrowthEvents; index++ {
		namespace := fmt.Sprintf("database:growth:%d", index)
		growthWindowStart := startsAt.Add(12 * time.Hour)
		growthWindow := 60 * time.Hour
		occursAt := growthWindowStart.Add(time.Duration(random.uint64(namespace+":at") % uint64(growthWindow)))
		dataBytes := random.int64Range(g.profile.Infrastructure.DatabaseGrowthDataBytes, namespace+":data")
		logsBytes := random.int64Range(g.profile.Infrastructure.DatabaseGrowthLogsBytes, namespace+":logs")
		incidents = append(incidents, events.ScheduledWorldEvent{OccursAt: occursAt, Event: events.DatabaseGrowthRequested{
			GrowthID: model.GrowthID(fmt.Sprintf("database-growth-%03d", index)), DataDeltaBytes: dataBytes, LogsDeltaBytes: logsBytes, RequestedAt: occursAt,
		}})
	}
	for index := int64(1); index <= g.profile.Infrastructure.BackendLogGrowthEvents; index++ {
		namespace := fmt.Sprintf("backend:logs:%d", index)
		growthWindowStart := startsAt.Add(6 * time.Hour)
		growthWindow := 60 * time.Hour
		occursAt := growthWindowStart.Add(time.Duration(random.uint64(namespace+":at") % uint64(growthWindow)))
		incidents = append(incidents, events.ScheduledWorldEvent{OccursAt: occursAt, Event: events.DiskLogsGrowthRequested{
			GrowthID: model.GrowthID(fmt.Sprintf("backend-logs-growth-%03d", index)), ServerID: "server-initial-1",
			DeltaBytes: random.int64Range(g.profile.Infrastructure.BackendLogGrowthBytes, namespace+":bytes"), RequestedAt: occursAt,
		}})
	}
	rotationTargets := make([]model.ServerID, 0, g.profile.Infrastructure.InitialBackendInstances+1)
	for index := 1; index <= g.profile.Infrastructure.InitialBackendInstances; index++ {
		rotationTargets = append(rotationTargets, model.ServerID(fmt.Sprintf("server-initial-%d", index)))
	}
	rotationTargets = append(rotationTargets, model.ServerID("db-server-initial"))
	for index := int64(1); index <= g.profile.Credentials.RotationEvents; index++ {
		namespace := fmt.Sprintf("credentials:rotation:%d", index)
		occursAt := startsAt.Add(time.Duration(random.int64Range(g.profile.Credentials.RotationAfterSeconds, namespace+":at")) * time.Second)
		incidents = append(incidents, events.ScheduledWorldEvent{OccursAt: occursAt, Event: events.ServerCredentialRotationRequested{
			RotationID: fmt.Sprintf("credential-rotation-%03d", index),
			ServerID:   rotationTargets[(index-1)%int64(len(rotationTargets))], RequestedAt: occursAt,
		}})
	}
	sort.SliceStable(incidents, func(i, j int) bool { return scheduledBefore(incidents[i], incidents[j]) })
	if len(incidents) > 0 {
		merged := make(events.EventSchedule, 0, len(schedule)+len(incidents))
		visitorIndex, incidentIndex := 0, 0
		for visitorIndex < len(schedule) && incidentIndex < len(incidents) {
			if scheduledBefore(incidents[incidentIndex], schedule[visitorIndex]) {
				merged = append(merged, incidents[incidentIndex])
				incidentIndex++
			} else {
				merged = append(merged, schedule[visitorIndex])
				visitorIndex++
			}
		}
		merged = append(merged, schedule[visitorIndex:]...)
		merged = append(merged, incidents[incidentIndex:]...)
		schedule = merged
	}
	if int64(len(schedule)) > g.profile.Limits.MaxScheduledEvents {
		return nil, fmt.Errorf("%w: events exceed %d", ErrGenerationLimit, g.profile.Limits.MaxScheduledEvents)
	}
	if int64(len(schedule)) != g.profile.Traffic.TargetScheduledEvents {
		return nil, fmt.Errorf("%w: generated %d scheduled events, target is %d", ErrGenerationLimit, len(schedule), g.profile.Traffic.TargetScheduledEvents)
	}
	for index := range schedule {
		schedule[index].Sequence = uint64(index + 1)
	}
	return schedule, nil
}

func scheduledBefore(left, right events.ScheduledWorldEvent) bool {
	if !left.OccursAt.Equal(right.OccursAt) {
		return left.OccursAt.Before(right.OccursAt)
	}
	leftType, leftPayload, _ := simulation.EncodeEvent(left.Event)
	rightType, rightPayload, _ := simulation.EncodeEvent(right.Event)
	if leftType != rightType {
		return leftType < rightType
	}
	return string(leftPayload) < string(rightPayload)
}

func deterministicVisitorRoll(seed int64, visitorID model.VisitorID, step string) uint32 {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", seed, visitorID, step)))
	return uint32(binary.BigEndian.Uint64(digest[:8]) % uint64(ProbabilityScale))
}

type deterministicRandom struct {
	seed             int64
	generatorVersion string
}

func (r deterministicRandom) uint64(namespace string) uint64 {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", r.seed, r.generatorVersion, namespace)))
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
func (r deterministicRandom) token(namespace string, length int) string {
	result := ""
	for index := 0; len(result) < length; index++ {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s:%d", r.seed, r.generatorVersion, namespace, index)))
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
func scaleFixedPPM(value int64, multiplier uint32) int64 {
	scale := int64(ProbabilityScale)
	return value/scale*int64(multiplier) + value%scale*int64(multiplier)/scale
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
