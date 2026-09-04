package generator

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
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
	DDoS           DDoSConfig           `yaml:"ddos"`
	Credentials    CredentialsConfig    `yaml:"credentials"`
	Costs          CostsConfig          `yaml:"costs"`
	Limits         LimitsConfig         `yaml:"limits"`
}

type ClockConfig struct {
	StartsAt               string `yaml:"starts_at"`
	EndsAt                 string `yaml:"ends_at"`
	Timezone               string `yaml:"timezone"`
	RandomStartWeek        bool   `yaml:"random_start_week"`
	SimulationDurationDays int    `yaml:"simulation_duration_days"`
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
	CatalogToProductDelaySeconds   IntRange `yaml:"catalog_to_product_delay_seconds"`
}

type CatalogConfig struct {
	ProductCount     IntRange `yaml:"product_count"`
	PriceMinor       IntRange `yaml:"price_minor"`
	PopularityWeight IntRange `yaml:"popularity_weight"`
	NameTemplates    []string `yaml:"name_templates"`
}

type InfrastructureConfig struct {
	InitialBackendInstances         int                   `yaml:"initial_backend_instances"`
	MinBackendInstances             int                   `yaml:"min_backend_instances"`
	MaxBackendInstances             int                   `yaml:"max_backend_instances"`
	ServerCapacityUnits             int64                 `yaml:"server_capacity_units"`
	ServerCostPerHourMinor          int64                 `yaml:"server_cost_per_hour_minor"`
	ServerProvisioningSeconds       int64                 `yaml:"server_provisioning_seconds"`
	ServerGracefulDrainSeconds      int64                 `yaml:"server_graceful_drain_seconds"`
	DatabaseDiskXBytes              int64                 `yaml:"database_disk_x_bytes"`
	InitialDatabaseDataBytes        int64                 `yaml:"initial_database_data_bytes"`
	InitialDatabaseLogsBytes        int64                 `yaml:"initial_database_logs_bytes"`
	DatabaseGrowthEvents            int64                 `yaml:"database_growth_events"`
	DatabaseGrowthDataBytes         IntRange              `yaml:"database_growth_data_bytes"`
	DatabaseGrowthLogsBytes         IntRange              `yaml:"database_growth_logs_bytes"`
	BackendLogGrowthEvents          int64                 `yaml:"backend_log_growth_events"`
	BackendLogGrowthBytes           IntRange              `yaml:"backend_log_growth_bytes"`
	BackendSurgeVisitors            int64                 `yaml:"backend_surge_visitors"`
	DatabaseConnectionSurgeVisitors int64                 `yaml:"database_connection_surge_visitors"`
	Pages                           map[string]PageConfig `yaml:"pages"`
}

type PageConfig struct {
	LoadUnits           IntRange `yaml:"load_units"`
	ResourceHoldSeconds IntRange `yaml:"resource_hold_seconds"`
	BaseLatencyMS       IntRange `yaml:"base_latency_ms"`
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
	SourceCIDR          string   `yaml:"source_cidr"`
	UserAgent           string   `yaml:"user_agent"`
	RegionCode          string   `yaml:"region_code"`
}

type CredentialsConfig struct {
	RotationEvents       int64    `yaml:"rotation_events"`
	RotationAfterSeconds IntRange `yaml:"rotation_after_seconds"`
}

type CostsConfig struct {
	Currency                   string `yaml:"currency"`
	ServerBillingPeriodSeconds int64  `yaml:"server_billing_period_seconds"`
	ServerBillingRounding      string `yaml:"server_billing_rounding"`
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
	if !end.After(start) || p.Clock.SimulationDurationDays != 7 || !p.Clock.RandomStartWeek {
		return invalid("clock selection window and simulation duration must be positive")
	}
	if end.Sub(start) < 365*24*time.Hour {
		return invalid("random week selection requires at least a one-year selection window")
	}
	if start.Add(time.Duration(p.Clock.SimulationDurationDays) * 24 * time.Hour).After(end) {
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
	if !validPPM(p.Journey.CatalogToProductProbabilityPPM) ||
		!validNonNegativeRange(p.Journey.CatalogToProductDelaySeconds) {
		return invalid("invalid visitor journey probabilities or delays")
	}
	if !validPositiveRange(p.Catalog.ProductCount) || !validPositiveRange(p.Catalog.PriceMinor) ||
		!validPositiveRange(p.Catalog.PopularityWeight) || len(p.Catalog.NameTemplates) == 0 {
		return invalid("invalid catalog configuration")
	}
	if p.Infrastructure.InitialBackendInstances < p.Infrastructure.MinBackendInstances ||
		p.Infrastructure.InitialBackendInstances > p.Infrastructure.MaxBackendInstances ||
		p.Infrastructure.MinBackendInstances < 1 || p.Infrastructure.ServerCapacityUnits <= 0 ||
		p.Infrastructure.ServerCostPerHourMinor < 0 || p.Infrastructure.ServerProvisioningSeconds <= 0 {
		return invalid("invalid backend infrastructure configuration")
	}
	if p.Infrastructure.DatabaseDiskXBytes <= 0 || p.Infrastructure.InitialDatabaseDataBytes < 0 || p.Infrastructure.InitialDatabaseLogsBytes < 0 ||
		p.Infrastructure.InitialDatabaseDataBytes+p.Infrastructure.InitialDatabaseLogsBytes > p.Infrastructure.DatabaseDiskXBytes ||
		p.Infrastructure.DatabaseGrowthEvents <= 0 || !validNonNegativeRange(p.Infrastructure.DatabaseGrowthDataBytes) ||
		!validNonNegativeRange(p.Infrastructure.DatabaseGrowthLogsBytes) ||
		p.Infrastructure.InitialDatabaseDataBytes+p.Infrastructure.DatabaseGrowthEvents*p.Infrastructure.DatabaseGrowthDataBytes.Max > 4*p.Infrastructure.DatabaseDiskXBytes {
		return invalid("invalid database infrastructure configuration")
	}
	if p.Infrastructure.DatabaseGrowthDataBytes.Max+p.Infrastructure.DatabaseGrowthLogsBytes.Max <= 0 ||
		p.Infrastructure.DatabaseGrowthEvents >= p.Traffic.TargetScheduledEvents {
		return invalid("database growth must leave room for traffic")
	}
	if p.Infrastructure.BackendLogGrowthEvents <= 0 || !validPositiveRange(p.Infrastructure.BackendLogGrowthBytes) ||
		p.Infrastructure.BackendLogGrowthBytes.Min != p.Infrastructure.BackendLogGrowthBytes.Max ||
		p.Infrastructure.BackendLogGrowthEvents*p.Infrastructure.BackendLogGrowthBytes.Min != p.Infrastructure.DatabaseDiskXBytes {
		return invalid("backend log growth must fill exactly one backend disk")
	}
	if len(p.Infrastructure.Pages) != 2 {
		return invalid("only product_list and product_page are supported")
	}
	for _, page := range []string{"product_list", "product_page"} {
		config, exists := p.Infrastructure.Pages[page]
		if !exists || !validPositiveRange(config.LoadUnits) || !validPositiveRange(config.ResourceHoldSeconds) || !validPositiveRange(config.BaseLatencyMS) {
			return invalid("invalid or missing page configuration for %q", page)
		}
		if config.LoadUnits.Max > p.Infrastructure.ServerCapacityUnits {
			return invalid("page %q load cannot fit on one server", page)
		}
	}
	listPage := p.Infrastructure.Pages["product_list"]
	productPage := p.Infrastructure.Pages["product_page"]
	if p.Infrastructure.BackendSurgeVisitors < 0 ||
		(p.Infrastructure.BackendSurgeVisitors > 0 &&
			(p.Infrastructure.BackendSurgeVisitors*listPage.LoadUnits.Min <= p.Infrastructure.ServerCapacityUnits ||
				listPage.LoadUnits.Max+productPage.LoadUnits.Max > p.Infrastructure.ServerCapacityUnits ||
				p.Infrastructure.BackendSurgeVisitors > p.Traffic.TargetScheduledEvents)) {
		return invalid("backend surge must overload one backend and fit on two")
	}
	if p.Infrastructure.DatabaseConnectionSurgeVisitors < 0 ||
		(p.Infrastructure.DatabaseConnectionSurgeVisitors > 0 &&
			(p.Infrastructure.DatabaseConnectionSurgeVisitors <= model.DBSmallConnections ||
				p.Infrastructure.BackendSurgeVisitors+p.Infrastructure.DatabaseConnectionSurgeVisitors > p.Traffic.TargetScheduledEvents ||
				p.Infrastructure.DatabaseConnectionSurgeVisitors*(listPage.LoadUnits.Max+productPage.LoadUnits.Max) >
					int64(p.Infrastructure.MaxBackendInstances)*p.Infrastructure.ServerCapacityUnits)) {
		return invalid("database connection surge must exceed db.small and fit configured backends")
	}
	if p.DDoS.Enabled {
		for page := range p.DDoS.TargetPageWeights {
			if page != "product_list" && page != "product_page" {
				return invalid("unsupported DDoS page %q", page)
			}
		}
		if !validNonNegativeRange(p.DDoS.AttackCount) || !validWeights(p.DDoS.TargetPageWeights) || len(p.DDoS.Kinds) == 0 {
			return invalid("invalid DDoS configuration")
		}
		if p.Traffic.TargetScheduledEvents <= 2*p.DDoS.AttackCount.Max {
			return invalid("target scheduled events must leave room for visitors after DDoS events")
		}
		for _, kind := range p.DDoS.Kinds {
			prefix, prefixErr := netip.ParsePrefix(kind.SourceCIDR)
			if kind.ID == "" || kind.Weight == 0 ||
				(kind.Resolution != "scale_or_expiry") ||
				!validPositiveRange(kind.DurationSeconds) || !validPositiveRange(kind.RequestsPerMinute) ||
				!validPositiveRange(kind.LoadUnitsPerRequest) || strings.TrimSpace(kind.SourceCIDR) == "" ||
				strings.TrimSpace(kind.UserAgent) == "" || len(kind.RegionCode) != 2 || kind.RegionCode != strings.ToUpper(kind.RegionCode) ||
				prefixErr != nil || prefix != prefix.Masked() {
				return invalid("invalid DDoS kind %q", kind.ID)
			}
		}
	}
	reservedScenarioEvents := p.Credentials.RotationEvents + p.Infrastructure.DatabaseGrowthEvents + p.Infrastructure.BackendLogGrowthEvents
	if p.DDoS.Enabled {
		reservedScenarioEvents += 2 * p.DDoS.AttackCount.Max
	}
	if p.Credentials.RotationEvents <= 0 || !validPositiveRange(p.Credentials.RotationAfterSeconds) ||
		p.Credentials.RotationAfterSeconds.Max >= int64(p.Clock.SimulationDurationDays)*24*60*60 ||
		p.Traffic.TargetScheduledEvents <= reservedScenarioEvents {
		return invalid("invalid credential rotation configuration")
	}
	if len(p.Costs.Currency) != 3 || p.Costs.Currency != strings.ToUpper(p.Costs.Currency) ||
		p.Costs.ServerBillingPeriodSeconds != 3600 || p.Costs.ServerBillingRounding != "ceil" {
		return invalid("invalid costs configuration")
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
