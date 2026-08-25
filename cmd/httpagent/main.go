package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

const (
	defaultAgentID      = "daily-http-agent"
	defaultAgentVersion = "v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "http agent:", err)
		os.Exit(1)
	}
}

func run() error {
	apiURL := flag.String("url", "http://127.0.0.1:8080", "simulator base URL")
	seed := flag.Int64("seed", 1, "world seed")
	agentID := flag.String("agent-id", defaultAgentID, "agent identifier")
	advance := flag.Duration("advance", 24*time.Hour, "simulation time advanced per iteration")
	maxSteps := flag.Int("max-steps", 400, "safety limit for agent iterations")
	timeout := flag.Duration("timeout", 5*time.Minute, "timeout for one HTTP request")
	flag.Parse()
	if *advance < 5*time.Minute {
		return fmt.Errorf("advance must be at least 5m")
	}
	if *maxSteps < 1 {
		return fmt.Errorf("max-steps must be positive")
	}
	client, err := newAPIClient(*apiURL, *timeout)
	if err != nil {
		return err
	}
	agent, err := newHTTPAgent(client, agentConfig{seed: *seed, agentID: *agentID, agentVersion: defaultAgentVersion, advance: *advance, maxSteps: *maxSteps})
	if err != nil {
		return err
	}
	return agent.run(context.Background())
}
