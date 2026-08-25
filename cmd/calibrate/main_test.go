package main

import (
	"os"
	"strings"
	"testing"
)

func TestProductionCalibrationTargetsOneCompletedRun(t *testing.T) {
	config, err := loadCalibration("../../config/calibration.v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if config.Runs != 10 || config.TargetCompletedRuns != 1 || config.TargetEarlyNegative != 9 {
		t.Fatalf("calibration targets = %#v", config)
	}
	if config.TargetScheduledEvents != 100_000 || config.BadAgent.AdvanceStepHours != 1 {
		t.Fatalf("calibration scale = %#v", config)
	}
}

func TestCalibrationRejectsUnclassifiedRuns(t *testing.T) {
	raw, err := os.ReadFile("../../config/calibration.v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "target_completed_runs: 1", "target_completed_runs: 0", 1))
	if _, err := decodeCalibration(strings.NewReader(string(raw))); err == nil {
		t.Fatal("calibration accepted targets that do not classify every run")
	}
}
