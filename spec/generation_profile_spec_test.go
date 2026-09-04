package spec_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

type profileScenario struct {
	t       *testing.T
	path    string
	content string
	profile generator.WorldGenerationProfile
	err     error
}

func newProfileScenario(t *testing.T) *profileScenario { return &profileScenario{t: t} }

func (s *profileScenario) GivenDefaultProfile() {
	s.t.Helper()
	_, file, _, _ := runtime.Caller(0)
	s.path = filepath.Join(filepath.Dir(file), "..", "config", "world-generation.v2.yaml")
}

func (s *profileScenario) GivenProfile(content string) { s.content = content }

func (s *profileScenario) WhenProfileIsLoaded() {
	s.t.Helper()
	if s.path != "" {
		s.profile, s.err = generator.LoadProfileFile(s.path)
		return
	}
	s.profile, s.err = generator.LoadProfile(strings.NewReader(s.content))
}

func (s *profileScenario) ThenProfileIsValid() {
	s.t.Helper()
	if s.err != nil {
		s.t.Fatalf("profile is invalid: %v", s.err)
	}
	if s.profile.Version != "world-generation.v2" {
		s.t.Fatalf("version = %q", s.profile.Version)
	}
	if len(s.profile.Traffic.HourlyMultipliers) != 24 {
		s.t.Fatalf("hour multipliers = %d", len(s.profile.Traffic.HourlyMultipliers))
	}
	if !s.profile.DDoS.Enabled || len(s.profile.DDoS.Kinds) != 2 {
		s.t.Fatal("DDoS strategies are not configured")
	}
	if !s.profile.Clock.RandomStartWeek || s.profile.Clock.SimulationDurationDays != 7 {
		s.t.Fatalf("random weekly clock = %#v", s.profile.Clock)
	}
	if s.profile.Credentials.RotationEvents < 1 {
		s.t.Fatalf("credential rotations = %#v", s.profile.Credentials)
	}
	if s.profile.Infrastructure.BackendSurgeVisitors < 1 {
		s.t.Fatalf("backend surge is not configured: %#v", s.profile.Infrastructure)
	}
	if s.profile.Limits.MaxScheduledEvents != 10_000_000 || s.profile.Limits.MaxVisitors != 10_000_000 {
		s.t.Fatalf("generation limits = %#v", s.profile.Limits)
	}
	if s.profile.Traffic.TargetScheduledEvents != 100_000 {
		s.t.Fatalf("target scheduled events = %d", s.profile.Traffic.TargetScheduledEvents)
	}
}

func (s *profileScenario) ThenProfileIsRejected() {
	s.t.Helper()
	if !errors.Is(s.err, generator.ErrInvalidProfile) {
		s.t.Fatalf("error = %v, want ErrInvalidProfile", s.err)
	}
}

func TestDefaultWorldGenerationProfileIsLoadable(t *testing.T) {
	s := newProfileScenario(t)
	s.GivenDefaultProfile()
	s.WhenProfileIsLoaded()
	s.ThenProfileIsValid()
}

func TestWorldGenerationProfileRejectsUnknownFields(t *testing.T) {
	s := newProfileScenario(t)
	s.GivenDefaultProfile()
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	s.path = ""
	s.GivenProfile(string(data) + "\nunknown_setting: true\n")
	s.WhenProfileIsLoaded()
	s.ThenProfileIsRejected()
}

func TestRandomWeekProfileRejectsSelectionWindowShorterThanOneYear(t *testing.T) {
	s := newProfileScenario(t)
	s.GivenDefaultProfile()
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	s.path = ""
	s.GivenProfile(strings.Replace(string(data), "2031-01-01T00:00:00Z", "2030-02-01T00:00:00Z", 1))
	s.WhenProfileIsLoaded()
	s.ThenProfileIsRejected()
}

func TestProfileRejectsBackendSurgeThatStillFitsOneBackend(t *testing.T) {
	s := newProfileScenario(t)
	s.GivenDefaultProfile()
	s.WhenProfileIsLoaded()
	if s.err != nil {
		t.Fatal(s.err)
	}
	s.profile.Infrastructure.BackendSurgeVisitors = 1
	if err := s.profile.Validate(); !errors.Is(err, generator.ErrInvalidProfile) {
		t.Fatalf("undersized backend surge accepted: %v", err)
	}
}

func TestProfileRejectsRemovedPageAndFixResolution(t *testing.T) {
	for _, kind := range []string{"page", "attack"} {
		t.Run(kind, func(t *testing.T) {
			s := newProfileScenario(t)
			s.GivenDefaultProfile()
			s.WhenProfileIsLoaded()
			if s.err != nil {
				t.Fatal(s.err)
			}
			if kind == "page" {
				s.profile.Infrastructure.Pages["purchase"] = s.profile.Infrastructure.Pages["product_page"]
			} else {
				s.profile.DDoS.Kinds[0].Resolution = "fix_or_expiry"
			}
			if err := s.profile.Validate(); !errors.Is(err, generator.ErrInvalidProfile) {
				t.Fatalf("removed setting accepted: %v", err)
			}
		})
	}
}
