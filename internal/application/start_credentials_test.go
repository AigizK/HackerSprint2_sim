package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

func TestStartCredentialsSurviveRestartAndStayOutOfPublicStateAndAudit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	storage, err := persistence.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if storage != nil {
			_ = storage.Close()
		}
	}()
	startsAt := time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RegisterManualWorld(ctx, storage.Catalog, ManualWorldInput{
		Seed: -1, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	service := NewStartRunService(storage.Catalog, storage.Journal, nil, storage.Journal)
	request := StartRunRequest{Seed: -1, AgentID: "agent", AgentVersion: "1.0", RequestID: "start-001"}
	first, err := service.StartAudited(ctx, request, AgentRequest{
		AuditID: "audit-001", Method: http.MethodPost, Path: "/v2/start",
		Headers: http.Header{"Authorization": {"Basic must-not-be-recorded"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ControlPanelAuth.Username) < 24 || len(first.ControlPanelAuth.Password) < 40 {
		t.Fatal("start must issue a nonempty, high-entropy access pair")
	}
	if !first.Created {
		t.Fatal("first start must create a run")
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	storage, err = persistence.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	service = NewStartRunService(storage.Catalog, storage.Journal, nil)
	repeated, err := service.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Created || repeated.Run.RunID != first.Run.RunID || repeated.ControlPanelAuth != first.ControlPanelAuth {
		t.Fatal("restart/retry must preserve run and credentials")
	}
	request.RequestID = "start-002"
	other, err := service.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if other.Run.RunID == first.Run.RunID || other.ControlPanelAuth.Username == first.ControlPanelAuth.Username ||
		other.ControlPanelAuth.Password == first.ControlPanelAuth.Password {
		t.Fatal("independent runs of the same world must have independent credentials")
	}
	records, err := storage.Journal.Load(ctx, first.Run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	audits, err := storage.Journal.LoadAgentRequests(ctx, first.Run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatal("start audit is missing")
	}
	for _, value := range []any{first.Run, first.State, first, records, audits} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{first.ControlPanelAuth.Username, first.ControlPanelAuth.Password, "must-not-be-recorded"} {
			if bytes.Contains(encoded, []byte(secret)) {
				t.Fatal("credentials leaked into a public record, state or journal")
			}
		}
	}
	if _, err := storage.Catalog.GetOrCreateControlPanelCredentials(ctx, "missing-run"); !errors.Is(err, simulation.ErrRunNotFound) {
		t.Fatalf("missing run credentials error: %v", err)
	}
	var count int
	if err := storage.Catalog.DB().QueryRow(`SELECT COUNT(*) FROM run_control_credentials`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("credentials count = %d, error = %v", count, err)
	}
	if err := storage.Catalog.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 3`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration count = %d, error = %v", count, err)
	}
}

func TestConcurrentStartsReturnTheSameRunAndCredentials(t *testing.T) {
	ctx := context.Background()
	storage, err := persistence.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	startsAt := time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RegisterManualWorld(ctx, storage.Catalog, ManualWorldInput{Seed: -1, StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	service := NewStartRunService(storage.Catalog, storage.Journal, nil)
	request := StartRunRequest{Seed: -1, AgentID: "agent", AgentVersion: "1.0", RequestID: "concurrent"}
	const workers = 8
	results := make([]StartRunResult, workers)
	errorsByWorker := make([]error, workers)
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-gate
			results[index], errorsByWorker[index] = service.Start(ctx, request)
		}(index)
	}
	close(gate)
	wg.Wait()
	created := 0
	for index, result := range results {
		if errorsByWorker[index] != nil {
			t.Fatal(errorsByWorker[index])
		}
		if result.Created {
			created++
		}
		if result.Run.RunID != results[0].Run.RunID || result.ControlPanelAuth != results[0].ControlPanelAuth {
			t.Fatal("concurrent starts returned different credentials or runs")
		}
	}
	if created != 1 {
		t.Fatalf("created %d runs, want one", created)
	}
	formatted := fmt.Sprintf("%v %+v %#v", results[0].ControlPanelAuth, results[0].ControlPanelAuth, results[0].ControlPanelAuth)
	if bytes.Contains([]byte(formatted), []byte(results[0].ControlPanelAuth.Password)) {
		t.Fatal("diagnostic formatting leaked credentials")
	}
}

func TestGeneratedStartRetryRestoresWorldAndCredentials(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	storage, err := persistence.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if storage != nil {
			_ = storage.Close()
		}
	}()
	profile, err := generator.LoadProfileFile("../../config/world-generation.v2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profile.Traffic.TargetScheduledEvents = 42
	profile.DDoS.Enabled = false
	profile.Infrastructure.BackendSurgeVisitors = 0
	profile.Infrastructure.DatabaseConnectionSurgeVisitors = 0
	worldGenerator, err := generator.New(profile)
	if err != nil {
		t.Fatal(err)
	}
	service := NewStartRunService(storage.Catalog, storage.Journal, worldGenerator)
	request := StartRunRequest{Seed: 42, AgentID: "agent", AgentVersion: "1.0", RequestID: "generated"}
	first, err := service.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			if err := storage.Close(); err != nil {
				t.Fatal(err)
			}
			storage, err = persistence.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			service = NewStartRunService(storage.Catalog, storage.Journal, worldGenerator)
		}
		repeated, err := service.Start(ctx, request)
		if err != nil {
			t.Fatalf("generated retry (restart=%t): %v", restart, err)
		}
		if repeated.Created || repeated.Run.RunID != first.Run.RunID || repeated.ControlPanelAuth != first.ControlPanelAuth || len(repeated.State.Schedule) != 42 {
			t.Fatal("generated retry must preserve run, schedule and credentials")
		}
	}
}
