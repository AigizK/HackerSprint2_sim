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
	s.path = filepath.Join(filepath.Dir(file), "..", "config", "world-generation.v1.yaml")
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
	if s.profile.Version != "world-generation.v1" {
		s.t.Fatalf("version = %q", s.profile.Version)
	}
	if len(s.profile.Traffic.HourlyMultipliers) != 24 {
		s.t.Fatalf("hour multipliers = %d", len(s.profile.Traffic.HourlyMultipliers))
	}
	if !s.profile.Economy.StopRunWhenBalanceIsNegative {
		s.t.Fatal("negative balance does not stop the run")
	}
	if !s.profile.DDoS.Enabled || len(s.profile.DDoS.Kinds) != 3 {
		s.t.Fatal("DDoS strategies are not configured")
	}
	if s.profile.Deployments.FutureDeploymentDurationReduction.Max == 0 {
		s.t.Fatal("future deployment acceleration is not configured")
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

func TestWorldGenerationProfileRejectsWorldShorterThanOneYear(t *testing.T) {
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
