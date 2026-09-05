package spec_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func assertRunSummaryMatchesReplay(t *testing.T, summary *simulation.RunSummaryProjection, records []simulation.StoredEvent) {
	t.Helper()
	for _, record := range records[summary.View(0).EventCount:] {
		summary.Apply(record)
	}
	state, err := simulation.Rehydrate(records)
	if err != nil {
		t.Fatal(err)
	}
	full := simulation.NewProjection(records, state).Overview()
	want := simulation.RunSummary{Seed: state.Seed, EventCount: uint64(len(records)), AgentRequestCount: 7,
		Overview: simulation.RunListOverview{RunStatus: full.RunStatus, SiteStatus: full.SiteStatus,
			SimulationTime: full.SimulationTime, SimulationEndsAt: full.SimulationEndsAt,
			Costs: full.Costs, Availability: full.Availability}}
	if got := summary.View(7); !reflect.DeepEqual(got, want) {
		t.Fatalf("summary differs from replay at event %d:\ngot:  %+v\nwant: %+v", len(records), got, want)
	}
}

func TestRunSummaryMatchesGeneratedWorldsThroughoutWeek(t *testing.T) {
	for _, seed := range []int64{42, 43, 44} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			world := newWorldGeneratorScenario(t)
			world.WhenWorldIsGenerated(seed)
			store := simulation.NewMemoryEventStore()
			bootstrap := append([]events.Event{events.WorldCreated{RunID: "summary", Seed: seed,
				StartedAt: world.world.StartsAt, EndsAt: world.world.EndsAt}}, world.world.Bootstrap...)
			bootstrap = append(bootstrap, events.WorldScheduleCreated{Schedule: world.world.Events, CreatedAt: world.world.StartsAt})
			ctx := context.Background()
			if _, err := store.Append(ctx, "summary", 0, bootstrap); err != nil {
				t.Fatal(err)
			}
			session, err := simulation.OpenRunSession(ctx, store, "summary")
			if err != nil {
				t.Fatal(err)
			}
			summary := simulation.NewRunSummaryProjection()
			assertRunSummaryMatchesReplay(t, summary, session.Records())
			for session.State().Status == simulation.RunRunning {
				if _, err := session.Execute(ctx, simulation.AdvanceTime{RequestedDuration: 6 * time.Hour}); err != nil {
					t.Fatal(err)
				}
				assertRunSummaryMatchesReplay(t, summary, session.Records())
			}
		})
	}
}

func TestRunSummaryMatchesDatabaseMigrationFirewallAndRecentErrors(t *testing.T) {
	h := newDatabaseHarness(t, model.InstanceDBSmall)
	summary := simulation.NewRunSummaryProjection()
	check := func() {
		t.Helper()
		records, err := h.store.Load(h.ctx, h.runID)
		if err != nil {
			t.Fatal(err)
		}
		assertRunSummaryMatchesReplay(t, summary, records)
	}
	check()
	for range 101 {
		h.open()
	}
	check()
	h.execute(simulation.AdvanceTime{RequestedDuration: 5 * time.Minute})
	check() // errors exactly five minutes old still affect site status
	h.execute(simulation.AdvanceTime{RealElapsed: time.Second})
	check()
	upsertFirewall(h, firewallRule("deny", 1, model.FirewallDeny, model.FirewallMatch{RegionCode: "RU"}))
	openAs(h, browserRU)
	h.grow(1*gib, 10*gib) // overlap firewall and disk downtime
	check()
	h.execute(simulation.AdvanceTime{RequestedDuration: 5 * time.Minute})
	deleteFirewall(h, "deny")
	check()
	h.execute(simulation.CleanupDatabaseLogs{CommandID: h.id("clean"), ServerID: "db-server-1"})
	check()
	h.activeServer("db-server-2", model.InstanceDBMedium)
	h.execute(simulation.CreateDatabase{CommandID: h.id("create-db"), DatabaseID: "db-new", ServerID: "db-server-2", Name: "new"})
	h.stop()
	h.execute(simulation.AdvanceTime{RequestedDuration: 5 * time.Minute})
	check()
	h.execute(simulation.BackupDatabase{CommandID: h.id("backup"), OperationID: h.operation("backup"), BackupID: "backup", DatabaseID: "db-main"})
	h.execute(simulation.RestoreDatabase{CommandID: h.id("restore"), OperationID: h.operation("restore"), BackupID: "backup", DatabaseID: "db-new"})
	h.execute(simulation.SetSiteDatabase{CommandID: h.id("set-db"), DatabaseID: "db-new", ExpectedCurrentDatabaseID: "db-main"})
	check()
	h.execute(simulation.StartSite{CommandID: h.id("start")})
	h.execute(simulation.AdvanceTime{RequestedDuration: time.Hour})
	check()
}
