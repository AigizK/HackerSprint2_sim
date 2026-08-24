package simulation

import (
	"context"
	"errors"
	"sync"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

var ErrEngineClosed = errors.New("simulation engine closed")

type commandRequest struct {
	ctx     context.Context
	command Command
	result  chan commandResult
}

type commandResult struct {
	events []events.Event
	err    error
}

type runWorker struct {
	runID    string
	handler  *Handler
	commands chan commandRequest
	done     <-chan struct{}
}

// Engine runs different run streams concurrently while serializing every
// command for the same run through one channel and one goroutine.
type Engine struct {
	handler *Handler

	mu      sync.Mutex
	workers map[string]*runWorker
	closed  bool
	done    chan struct{}
}

func NewEngine(store EventStore) *Engine {
	return &Engine{
		handler: NewHandler(store),
		workers: make(map[string]*runWorker),
		done:    make(chan struct{}),
	}
}

func (e *Engine) Execute(ctx context.Context, runID string, command Command) ([]events.Event, error) {
	worker, err := e.worker(runID)
	if err != nil {
		return nil, err
	}
	request := commandRequest{
		ctx:     ctx,
		command: command,
		result:  make(chan commandResult, 1),
	}

	select {
	case worker.commands <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrEngineClosed
	}

	select {
	case result := <-request.result:
		return result.events, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrEngineClosed
	}
}

func (e *Engine) State(ctx context.Context, runID string) (State, error) {
	return e.handler.State(ctx, runID)
}

func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	close(e.done)
}

func (e *Engine) worker(runID string) (*runWorker, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrEngineClosed
	}
	if worker, exists := e.workers[runID]; exists {
		return worker, nil
	}
	worker := &runWorker{
		runID:    runID,
		handler:  e.handler,
		commands: make(chan commandRequest, 64),
		done:     e.done,
	}
	e.workers[runID] = worker
	go worker.run()
	return worker, nil
}

func (w *runWorker) run() {
	for {
		select {
		case request := <-w.commands:
			events, err := w.handler.Execute(request.ctx, w.runID, request.command)
			request.result <- commandResult{events: events, err: err}
		case <-w.done:
			return
		}
	}
}
