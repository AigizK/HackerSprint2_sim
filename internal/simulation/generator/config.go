package generator

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

const ProbabilityScale uint32 = 1_000_000

var ErrInvalidProfile = errors.New("invalid world generation profile")

type IntRange struct {
	Min int64 `yaml:"min"`
	Max int64 `yaml:"max"`
}

type PPMRange struct {
	Min uint32 `yaml:"min_ppm"`
	Max uint32 `yaml:"max_ppm"`
}

type WorldGenerationProfile struct {
	Version        string               `yaml:"version"`
	Clock          ClockConfig          `yaml:"clock"`
	Traffic        TrafficConfig        `yaml:"traffic"`
	Journey        JourneyConfig        `yaml:"journey"`
	Catalog        CatalogConfig        `yaml:"catalog"`
	Infrastructure InfrastructureConfig `yaml:"infrastructure"`
	Bugs           BugsConfig           `yaml:"bugs"`
	Deployments    DeploymentsConfig    `yaml:"deployments"`
	DDoS           DDoSConfig           `yaml:"ddos"`
	Economy        EconomyConfig        `yaml:"economy"`
	Limits         LimitsConfig         `yaml:"limits"`
}

type ClockConfig struct {
	StartsAt                 string `yaml:"starts_at"`
	EndsAt                   string `yaml:"ends_at"`
	Timezone                 string `yaml:"timezone"`
	RandomStartMonth         bool   `yaml:"random_start_month"`
	SimulationDurationMonths int    `yaml:"simulation_duration_months"`
}

type TrafficConfig struct {
	BaseArrivalsPerHour   int64              `yaml:"base_arrivals_per_hour"`
	TargetScheduledEvents int64              `yaml:"target_scheduled_events"`
	HourlyMultipliers     []uint32           `yaml:"hourly_multipliers_ppm"`
	WeekdayMultipliers    map[string]uint32  `yaml:"weekday_multipliers_ppm"`
	SpecialDays           []SpecialDayConfig `yaml:"special_days"`
	NoiseMultiplier       PPMRange           `yaml:"noise_multiplier"`
	MaxArrivalsPerHour    int64              `yaml:"max_arrivals_per_hour"`
}

type SpecialDayConfig struct {
	ID                   string `yaml:"id"`
	Rule                 string `yaml:"rule"`
	Month                int    `yaml:"month,omitempty"`
	Day                  int    `yaml:"day,omitempty"`
	DurationHours        int    `yaml:"duration_hours"`
	TrafficMultiplierPPM uint32 `yaml:"traffic_multiplier_ppm"`
}

type JourneyConfig struct {
	CatalogToProductProbabilityPPM uint32   `yaml:"catalog_to_product_probability_ppm"`
	ProductToPurchaseProbability   PPMRange `yaml:"product_to_purchase_probability"`
	CatalogToProductDelaySeconds   IntRange `yaml:"catalog_to_product_delay_seconds"`
	ProductToPurchaseDelaySeconds  IntRange `yaml:"product_to_purchase_delay_seconds"`
}

type CatalogConfig struct {
	ProductCount     IntRange `yaml:"product_count"`
	PriceMinor       IntRange `yaml:"price_minor"`
	PopularityWeight IntRange `yaml:"popularity_weight"`
	NameTemplates    []string `yaml:"name_templates"`
}

type InfrastructureConfig struct {
	InitialBackendInstances    int                   `yaml:"initial_backend_instances"`
	MinBackendInstances        int                   `yaml:"min_backend_instances"`
	MaxBackendInstances        int                   `yaml:"max_backend_instances"`
	ServerCapacityUnits        int64                 `yaml:"server_capacity_units"`
	ServerCostPerHourMinor     int64                 `yaml:"server_cost_per_hour_minor"`
	ServerProvisioningSeconds  int64                 `yaml:"server_provisioning_seconds"`
	ServerGracefulDrainSeconds int64                 `yaml:"server_graceful_drain_seconds"`
	Pages                      map[string]PageConfig `yaml:"pages"`
}

type PageConfig struct {
	LoadUnits           IntRange `yaml:"load_units"`
	ResourceHoldSeconds IntRange `yaml:"resource_hold_seconds"`
}

type BugsConfig struct {
	InitialBugCount             IntRange          `yaml:"initial_bug_count"`
	ProductHasBugProbabilityPPM uint32            `yaml:"product_has_bug_probability_ppm"`
	TriggerProbability          PPMRange          `yaml:"trigger_probability"`
	PageWeights                 map[string]uint32 `yaml:"page_weights"`
	FixTokenLength              int               `yaml:"fix_token_length"`
}

type DeploymentsConfig struct {
	Count                             IntRange          `yaml:"count"`
	CostMinor                         IntRange          `yaml:"cost_minor"`
	DurationSeconds                   IntRange          `yaml:"duration_seconds"`
	FailureProbability                PPMRange          `yaml:"failure_probability"`
	EffectsPerDeployment              IntRange          `yaml:"effects_per_deployment"`
	LoadReduction                     PPMRange          `yaml:"load_reduction"`
	RequestHoldReduction              PPMRange          `yaml:"request_hold_reduction"`
	FutureDeploymentDurationReduction PPMRange          `yaml:"future_deployment_duration_reduction"`
	MinimumFutureDeploymentSeconds    int64             `yaml:"minimum_future_deployment_seconds"`
	OldBugProbabilityReduction        PPMRange          `yaml:"old_bug_probability_reduction"`
	NewBugProbabilityPPM              uint32            `yaml:"new_bug_probability_ppm"`
	NewBugCount                       IntRange          `yaml:"new_bug_count"`
	NewBugTriggerProbability          PPMRange          `yaml:"new_bug_trigger_probability"`
	NewBugPageWeights                 map[string]uint32 `yaml:"new_bug_page_weights"`
}

type DDoSConfig struct {
	Enabled           bool              `yaml:"enabled"`
	AttackCount       IntRange          `yaml:"attack_count"`
	TargetPageWeights map[string]uint32 `yaml:"target_page_weights"`
	Kinds             []DDoSKindConfig  `yaml:"kinds"`
}

type DDoSKindConfig struct {
	ID                  string   `yaml:"id"`
	Weight              uint32   `yaml:"weight"`
	Resolution          string   `yaml:"resolution"`
	DurationSeconds     IntRange `yaml:"duration_seconds"`
	RequestsPerMinute   IntRange `yaml:"requests_per_minute"`
	LoadUnitsPerRequest IntRange `yaml:"load_units_per_request"`
	FixTokenLength      int      `yaml:"fix_token_length,omitempty"`
}

type EconomyConfig struct {
	InitialBalanceMinor          int64  `yaml:"initial_balance_minor"`
	StopRunWhenBalanceIsNegative bool   `yaml:"stop_run_when_balance_is_negative"`
	ServerBillingPeriodSeconds   int64  `yaml:"server_billing_period_seconds"`
	ServerBillingRounding        string `yaml:"server_billing_rounding"`
}

type LimitsConfig struct {
	MaxScheduledEvents int64 `yaml:"max_scheduled_events"`
	MaxVisitors        int64 `yaml:"max_visitors"`
}

func LoadProfile(reader io.Reader) (WorldGenerationProfile, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	var profile WorldGenerationProfile
	if err := decoder.Decode(&profile); err != nil {
		return WorldGenerationProfile{}, fmt.Errorf("%w: decode yaml: %v", ErrInvalidProfile, err)
	}
	if err := profile.Validate(); err != nil {
		return WorldGenerationProfile{}, err
	}
	return profile, nil
}

func LoadProfileFile(path string) (WorldGenerationProfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return WorldGenerationProfile{}, err
	}
	defer file.Close()
	return LoadProfile(file)
}

func (p WorldGenerationProfile) Validate() error {
	if p.Version == "" {
		return invalid("version is required")
	}
	start, err := time.Parse(time.RFC3339, p.Clock.StartsAt)
	if err != nil {
		return invalid("clock.starts_at must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, p.Clock.EndsAt)
	if err != nil {
		return invalid("clock.ends_at must be RFC3339")
	}
	if !end.After(start) || p.Clock.SimulationDurationMonths <= 0 {
		return invalid("clock selection window and simulation duration must be positive")
	}
	if p.Clock.RandomStartMonth && end.Sub(start) < 365*24*time.Hour {
		return invalid("random month selection requires at least a one-year selection window")
	}
	if start.AddDate(0, p.Clock.SimulationDurationMonths, 0).After(end) {
		return invalid("simulation duration does not fit clock selection window")
	}
	if _, err := time.LoadLocation(p.Clock.Timezone); err != nil {
		return invalid("clock.timezone is invalid")
	}
	if p.Traffic.BaseArrivalsPerHour <= 0 || p.Traffic.TargetScheduledEvents <= 0 ||
		p.Traffic.TargetScheduledEvents > p.Limits.MaxScheduledEvents || len(p.Traffic.HourlyMultipliers) != 24 {
		return invalid("traffic needs a positive base rate and exactly 24 hourly multipliers")
	}
	for _, multiplier := range p.Traffic.HourlyMultipliers {
		if !validMultiplierPPM(multiplier) {
			return invalid("hourly traffic multipliers must be in [0, 10_000_000]")
		}
	}
	for _, day := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		if !validMultiplierPPM(p.Traffic.WeekdayMultipliers[day]) || p.Traffic.WeekdayMultipliers[day] == 0 {
			return invalid("weekday multiplier %q must be positive", day)
		}
	}
	if !validMultiplierRange(p.Traffic.NoiseMultiplier) || p.Traffic.MaxArrivalsPerHour <= 0 {
		return invalid("invalid traffic noise or maximum arrivals")
	}
	for _, special := range p.Traffic.SpecialDays {
		if special.ID == "" || special.DurationHours <= 0 || special.TrafficMultiplierPPM == 0 || !validMultiplierPPM(special.TrafficMultiplierPPM) {
			return invalid("invalid special day")
		}
		if special.Rule == "fixed_date" && (special.Month < 1 || special.Month > 12 || special.Day < 1 || special.Day > 31) {
			return invalid("special day %q has an invalid fixed date", special.ID)
		}
		if special.Rule != "fixed_date" && special.Rule != "black_friday" {
			return invalid("special day %q has unsupported rule %q", special.ID, special.Rule)
		}
	}
	if !validPPM(p.Journey.CatalogToProductProbabilityPPM) || !validPPMRange(p.Journey.ProductToPurchaseProbability) ||
		!validNonNegativeRange(p.Journey.CatalogToProductDelaySeconds) || !validNonNegativeRange(p.Journey.ProductToPurchaseDelaySeconds) {
		return invalid("invalid visitor journey probabilities or delays")
	}
	if !validPositiveRange(p.Catalog.ProductCount) || !validPositiveRange(p.Catalog.PriceMinor) ||
		!validPositiveRange(p.Catalog.PopularityWeight) || len(p.Catalog.NameTemplates) == 0 {
		return invalid("invalid catalog configuration")
	}
	if p.Infrastructure.InitialBackendInstances < p.Infrastructure.MinBackendInstances ||
		p.Infrastructure.InitialBackendInstances > p.Infrastructure.MaxBackendInstances ||
		p.Infrastructure.MinBackendInstances < 0 || p.Infrastructure.ServerCapacityUnits <= 0 ||
		p.Infrastructure.ServerCostPerHourMinor < 0 || p.Infrastructure.ServerProvisioningSeconds <= 0 {
		return invalid("invalid backend infrastructure configuration")
	}
	for _, page := range []string{"product_list", "product_page", "purchase"} {
		config, exists := p.Infrastructure.Pages[page]
		if !exists || !validPositiveRange(config.LoadUnits) || !validPositiveRange(config.ResourceHoldSeconds) {
			return invalid("invalid or missing page configuration for %q", page)
		}
		if config.LoadUnits.Max > p.Infrastructure.ServerCapacityUnits {
			return invalid("page %q load cannot fit on one server", page)
		}
	}
	if !validNonNegativeRange(p.Bugs.InitialBugCount) || !validPPM(p.Bugs.ProductHasBugProbabilityPPM) ||
		!validPPMRange(p.Bugs.TriggerProbability) || !validWeights(p.Bugs.PageWeights) || p.Bugs.FixTokenLength < 8 {
		return invalid("invalid bug configuration")
	}
	if !validNonNegativeRange(p.Deployments.Count) || !validNonNegativeRange(p.Deployments.CostMinor) ||
		!validPositiveRange(p.Deployments.DurationSeconds) || !validPPMRange(p.Deployments.FailureProbability) ||
		!validNonNegativeRange(p.Deployments.EffectsPerDeployment) || !validPPMRange(p.Deployments.LoadReduction) ||
		!validPPMRange(p.Deployments.RequestHoldReduction) || !validPPMRange(p.Deployments.FutureDeploymentDurationReduction) ||
		p.Deployments.FutureDeploymentDurationReduction.Max >= ProbabilityScale || p.Deployments.MinimumFutureDeploymentSeconds <= 0 ||
		!validPPMRange(p.Deployments.OldBugProbabilityReduction) || !validPPM(p.Deployments.NewBugProbabilityPPM) ||
		!validNonNegativeRange(p.Deployments.NewBugCount) || !validPPMRange(p.Deployments.NewBugTriggerProbability) ||
		!validWeights(p.Deployments.NewBugPageWeights) {
		return invalid("invalid deployment configuration")
	}
	if p.DDoS.Enabled {
		if !validNonNegativeRange(p.DDoS.AttackCount) || !validWeights(p.DDoS.TargetPageWeights) || len(p.DDoS.Kinds) == 0 {
			return invalid("invalid DDoS configuration")
		}
		if p.Traffic.TargetScheduledEvents <= 2*p.DDoS.AttackCount.Max {
			return invalid("target scheduled events must leave room for visitors after DDoS events")
		}
		for _, kind := range p.DDoS.Kinds {
			if kind.ID == "" || kind.Weight == 0 ||
				(kind.Resolution != "scale_or_expiry" && kind.Resolution != "fix_or_expiry" && kind.Resolution != "expiry_only") ||
				!validPositiveRange(kind.DurationSeconds) || !validPositiveRange(kind.RequestsPerMinute) ||
				!validPositiveRange(kind.LoadUnitsPerRequest) || (kind.Resolution == "fix_or_expiry" && kind.FixTokenLength < 8) {
				return invalid("invalid DDoS kind %q", kind.ID)
			}
		}
	}
	if p.Economy.InitialBalanceMinor <= 0 || !p.Economy.StopRunWhenBalanceIsNegative ||
		p.Economy.ServerBillingPeriodSeconds <= 0 || p.Economy.ServerBillingRounding != "ceil" {
		return invalid("invalid economy configuration")
	}
	if p.Limits.MaxScheduledEvents <= 0 || p.Limits.MaxVisitors <= 0 || p.Limits.MaxVisitors > p.Limits.MaxScheduledEvents {
		return invalid("invalid generation limits")
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidProfile, fmt.Sprintf(format, args...))
}
func validPPM(value uint32) bool                { return value <= ProbabilityScale }
func validPPMRange(value PPMRange) bool         { return value.Min <= value.Max && validPPM(value.Max) }
func validPositiveRange(value IntRange) bool    { return value.Min > 0 && value.Min <= value.Max }
func validNonNegativeRange(value IntRange) bool { return value.Min >= 0 && value.Min <= value.Max }
func validMultiplierPPM(value uint32) bool      { return value <= 10*ProbabilityScale }
func validMultiplierRange(value PPMRange) bool {
	return value.Min <= value.Max && validMultiplierPPM(value.Max)
}
func validWeights(weights map[string]uint32) bool {
	var total uint64
	for _, weight := range weights {
		total += uint64(weight)
	}
	return len(weights) > 0 && total > 0
}
