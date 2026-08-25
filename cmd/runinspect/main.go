package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

func main() {
	runID := flag.String("run-id", "", "run id to replay")
	dataPath := flag.String("data", "data", "storage root")
	flag.Parse()
	if *runID == "" {
		fmt.Fprintln(os.Stderr, "run-id is required")
		os.Exit(2)
	}
	storage, err := persistence.Open(*dataPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer storage.Close()
	session, err := simulation.OpenRunSession(context.Background(), storage.Journal, *runID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	state := session.State()
	fmt.Printf("run_id=%s\nstatus=%s\nsimulation_time=%s\nversion=%d\npurchases=%d\n",
		state.RunID, state.Status, state.Clock.CurrentTime.Format("2006-01-02T15:04:05Z07:00"), session.Version(), state.Economy.SuccessfulPurchases)
}
