package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/application"
	"github.com/aigizk/hackersprint2-sim/internal/debugui"
	"github.com/aigizk/hackersprint2-sim/internal/httpapi"
	"github.com/aigizk/hackersprint2-sim/internal/persistence"
	"github.com/aigizk/hackersprint2-sim/internal/persistence/journal"
	"github.com/aigizk/hackersprint2-sim/internal/playerui"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "simulation server:", err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("addr", ":8080", "HTTP listen address")
	dataPath := flag.String("data", "data", "storage root")
	profilePath := flag.String("config", "config/world-generation.v2.yaml", "world generation profile")
	maxCachedRuns := flag.Int("max-cached-runs", 8, "maximum number of active runs cached in memory")
	flag.Parse()

	profile, err := generator.LoadProfileFile(*profilePath)
	if err != nil {
		return fmt.Errorf("load world profile: %w", err)
	}
	worldGenerator, err := generator.New(profile)
	if err != nil {
		return fmt.Errorf("create world generator: %w", err)
	}
	storage, err := persistence.Open(*dataPath, journal.WithMaxCachedRuns(*maxCachedRuns))
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer storage.Close()

	manager := application.NewRunManager(storage.Catalog, storage.Journal, storage.Journal, *maxCachedRuns)
	startService := application.NewStartRunService(storage.Catalog, storage.Journal, worldGenerator, storage.Journal)
	runService := application.NewRunService(manager, storage.Catalog)
	debugQuery := application.NewDebugQuery(storage.Catalog, storage.Journal, storage.Journal)
	debugHandler := debugui.New(debugQuery)
	playerHandler := playerui.New(debugQuery)
	rootHandler := http.NewServeMux()
	rootHandler.Handle("/debug", debugHandler)
	rootHandler.Handle("/debug/", debugHandler)
	rootHandler.Handle("/ui", playerHandler)
	rootHandler.Handle("/ui/", playerHandler)
	rootHandler.Handle("/", httpapi.New(startService, runService))

	server := &http.Server{
		Addr:              *address,
		Handler:           rootHandler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("simulation API listening on %s\n", *address)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
