package simulation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func serviceSession(t *testing.T) *RunSession {
	t.Helper()
	at := time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewMemoryEventStore()
	bootstrap := []events.Event{
		events.WorldCreated{RunID: "cleanup", Seed: 42, StartedAt: at, EndsAt: at.Add(3 * time.Hour)},
		events.CostsConfigured{Currency: "USD", ConfiguredAt: at},
		events.InfrastructureConfigured{ServerProvisioningDuration: time.Minute, ConfiguredAt: at},
		events.ServerProvisioningStarted{OperationID: "initial", ServerID: "server-1", CapacityUnits: 100, CostPerHourMinor: 100, StartedAt: at, ReadyAt: at},
		events.ServerActivated{OperationID: "initial", ServerID: "server-1", ActivatedAt: at},
		events.PageConfigured{Page: model.PageProductList, LoadUnits: 10, HoldDuration: time.Minute, ConfiguredAt: at},
		events.PageConfigured{Page: model.PageProduct, LoadUnits: 20, HoldDuration: time.Minute, ConfiguredAt: at},
		events.ProductAdded{ProductID: "product-1", Name: "Read-only product", PriceMinor: 1000, Available: true, ViewProbabilityPPM: ProbabilityScale, AddedAt: at},
	}
	if _, err := store.Append(context.Background(), "cleanup", 0, bootstrap); err != nil {
		t.Fatal(err)
	}
	session, err := OpenRunSession(context.Background(), store, "cleanup")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestReadOnlyVisitorNeverChangesCatalog(t *testing.T) {
	session := serviceSession(t)
	before := session.State().Products["product-1"]
	decided, err := session.Execute(context.Background(), SimulateVisitor{VisitorID: "visitor-1"})
	if err != nil {
		t.Fatal(err)
	}
	var pages []model.PageType
	for _, event := range decided {
		if event, ok := event.(events.PageRequestStarted); ok {
			pages = append(pages, event.Page)
		}
	}
	if !reflect.DeepEqual(pages, []model.PageType{model.PageProductList, model.PageProduct}) {
		t.Fatalf("pages: %v", pages)
	}
	if session.State().Products["product-1"] != before {
		t.Fatal("visitor changed catalog")
	}
	if _, err := session.Execute(context.Background(), ProbePage{RequestID: "invalid", Page: "purchase", ProductID: "product-1"}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("purchase allowed: %v", err)
	}
}

func TestServerReceiptsAndCostsSurviveReplayWithoutRevenue(t *testing.T) {
	session := serviceSession(t)
	ctx := context.Background()
	execute := func(command Command) []events.Event {
		t.Helper()
		result, err := session.Execute(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	create := AddServer{CommandID: "create-2", OperationID: "operation-2", ServerID: "server-2", CapacityUnits: 200, CostPerHourMinor: 50}
	execute(create)
	if len(execute(create)) != 0 {
		t.Fatal("duplicate created another server")
	}
	changed := create
	changed.CapacityUnits++
	if _, err := session.Execute(ctx, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("payload conflict: %v", err)
	}
	execute(AdvanceTime{RequestedDuration: 5 * time.Minute})
	execute(AdvanceTime{RequestedDuration: time.Hour})
	if session.State().Costs.ServerCostMinor != 250 {
		t.Fatalf("cost: %d", session.State().Costs.ServerCostMinor)
	}
	remove := RemoveServer{CommandID: "delete-2", OperationID: "operation-delete-2", ServerID: "server-2"}
	execute(remove)
	if len(execute(remove)) != 0 {
		t.Fatal("duplicate repeated deletion")
	}
	if session.State().Costs.ServerCostMinor != 250 {
		t.Fatal("deleting server refunded spent costs")
	}
	execute(AdvanceTime{RequestedDuration: time.Hour})
	if session.State().Costs.ServerCostMinor != 350 {
		t.Fatalf("cost after deletion: %d", session.State().Costs.ServerCostMinor)
	}
	if session.State().Status != RunRunning {
		t.Fatal("cost ended run early")
	}
	execute(AdvanceTime{RequestedDuration: 55 * time.Minute})
	if session.State().Status != RunCompleted || session.State().EndReason != "world_completed" {
		t.Fatal("run did not complete full horizon")
	}
	records := session.Records()
	for index, record := range records {
		kind, payload, err := EncodeEvent(record.Event)
		if err != nil {
			t.Fatal(err)
		}
		records[index].Event, err = DecodeEvent(kind, payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	replayed, err := Rehydrate(records)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Costs != session.State().Costs || !reflect.DeepEqual(replayed.Commands, session.State().Commands) {
		t.Fatal("replay lost costs or receipts")
	}
}

func TestRemovedEventTypesAreRejected(t *testing.T) {
	for _, kind := range []string{"ProductPurchased", "PurchaseIntentCreated", "RevenueLost", "EconomyConfigured",
		"ProductUpserted", "ProductDeleted", "StoreCredentialsConfigured", "StoreCredentialsRotated", "ManagerPermissionGranted", "ManagerPermissionRevoked",
		"ScenarioPurchaseAttempted", "ProductExternallyChanged", "CatalogLossEvaluated", "StoreOutageChanged", "MiniScenarioCheckpointReached",
		"PageBugActivated", "PageBugTriggered", "BugFixSubmitted", "PageBugFixed", "BugFixRejected", "TrafficAttackMitigated",
		"DeploymentDefined", "DeploymentStarted", "DeploymentCompleted", "DeploymentFailed", "DeploymentCostAccrued",
		"BackendScaleRequested", "ExternalProviderDegraded", "ExternalProviderRecovered", "CapacityAllocationReleased"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := DecodeEvent(kind, []byte(`{}`)); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("removed event is still registered: %v", err)
			}
		})
	}
}
