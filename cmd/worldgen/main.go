package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/persistence/sqlite"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/generator"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "generate world:", err)
		os.Exit(1)
	}
}

func run() error {
	seed := flag.Int64("seed", 0, "positive deterministic world seed")
	profilePath := flag.String("config", "config/world-generation.v2.yaml", "world generation profile")
	databasePath := flag.String("db", "data/catalog.db", "SQLite catalog database path")
	flag.Parse()
	if *seed <= 0 {
		return fmt.Errorf("seed must be positive")
	}

	profile, err := generator.LoadProfileFile(*profilePath)
	if err != nil {
		return err
	}
	worldGenerator, err := generator.New(profile)
	if err != nil {
		return err
	}
	if directory := filepath.Dir(*databasePath); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create database directory: %w", err)
		}
	}
	store, err := sqlite.Open(*databasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	world, created, err := worldGenerator.GetOrCreate(context.Background(), store, *seed)
	if err != nil {
		return err
	}
	fmt.Printf("seed=%d\n", world.Key.Seed)
	fmt.Printf("world_id=%s\n", world.WorldID)
	fmt.Printf("starts_at=%s\n", world.StartsAt.Format(time.RFC3339))
	fmt.Printf("ends_at=%s\n", world.EndsAt.Format(time.RFC3339))
	fmt.Printf("created=%t\n", created)
	fmt.Printf("bootstrap_events=%d\n", len(world.Bootstrap))
	fmt.Printf("scheduled_events=%d\n", len(world.Events))
	fmt.Printf("schedule_hash=%s\n", world.ScheduleHash)
	minimum, maximum, average := dailyVisitors(world.StartsAt, world.EndsAt, world.Events)
	fmt.Printf("daily_visitors_min=%d\n", minimum)
	fmt.Printf("daily_visitors_max=%d\n", maximum)
	fmt.Printf("daily_visitors_average=%.2f\n", average)
	return nil
}

func dailyVisitors(startsAt, endsAt time.Time, schedule events.EventSchedule) (int, int, float64) {
	counts := make(map[string]int)
	for _, scheduled := range schedule {
		if _, ok := scheduled.Event.(events.VisitorArrived); ok {
			counts[scheduled.OccursAt.UTC().Format(time.DateOnly)]++
		}
	}
	minimum, maximum, total, days := int(^uint(0)>>1), 0, 0, 0
	for day := startsAt.UTC(); day.Before(endsAt.UTC()); day = day.AddDate(0, 0, 1) {
		count := counts[day.Format(time.DateOnly)]
		if count < minimum {
			minimum = count
		}
		if count > maximum {
			maximum = count
		}
		total += count
		days++
	}
	if days == 0 {
		return 0, 0, 0
	}
	return minimum, maximum, float64(total) / float64(days)
}
