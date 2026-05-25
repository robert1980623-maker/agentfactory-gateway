package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/agentfactory/gateway/config"
	"github.com/agentfactory/gateway/gateway"
	"github.com/agentfactory/gateway/state"
	"github.com/agentfactory/gateway/worker"
)

// closer is the interface for types that have a Close method.
type closer interface {
	Close() error
}

func main() {
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}

	// Resolve worker script path.
	workerScript := "worker.py"
	if cfg.AFWorkDir != "" {
		workerScript = filepath.Join(cfg.AFWorkDir, "worker.py")
	}

	var sm state.StateManager
	var err error
	sm, err = state.NewSQLiteStore("gateway_state.db")
	if err != nil {
		log.Fatalf("Failed to initialize SQLite state store: %v", err)
	}
	if c, ok := sm.(state.ClosableStateManager); ok {
		defer c.Close()
	}

	w := worker.NewPythonWorker(cfg.PythonBin)
	w.Script = workerScript
	if cfg.AFCLIBin != "" {
		w.WithAFCLI(cfg.AFCLIBin)
	}
	sw := worker.NewStreamWorker(cfg.PythonBin)
	sw.Script = workerScript

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill, syscall.SIGTERM)
	defer cancel()

	g := gateway.NewSlackGateway(cfg.SlackBotToken, cfg.SlackAppToken, w, sw, sm)

	// Crash recovery: reconcile any tasks left in "running" state from a previous run.
	// Recovery runs after Gateway creation so results can be synchronised back
	// into the in-memory TaskQueue.
	log.Println("[startup] Running crash recovery...")
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 30*time.Second)
	results, err := gateway.RecoverActiveTasks(recoveryCtx, sm, w, g.Client())
	recoveryCancel()
	if err != nil {
		log.Printf("[startup] Recovery encountered errors: %v", err)
	}
	// Sync recovery results into the Gateway's TaskQueue so that:
	//  - terminal tasks free their channel slots and trigger dequeue of waiting tasks
	//  - still-running tasks are tracked so new requests don't bypass them
	g.ReconcileAfterRecovery(results)

	// Run gateway in a goroutine so we can wait for shutdown signal.
	errCh := make(chan error, 1)
	go func() {
		log.Println("[startup] Starting AgentFactory Gateway...")
		errCh <- g.Start(ctx)
	}()

	// Wait for context cancellation (signal received) or gateway error.
	select {
	case <-ctx.Done():
		log.Println("[shutdown] Signal received, initiating graceful shutdown...")
		// Gateway's Start will call Stop() when ctx is cancelled.
	case err := <-errCh:
		if err != nil {
			log.Printf("[shutdown] Gateway exited with error: %v", err)
		}
		return
	}

	// Phase 1: Wait for gateway to finish draining workers.
	log.Println("[shutdown] Phase 1: Waiting for gateway to drain workers...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Use a goroutine to unblock on timeout.
	done := make(chan struct{})
	go func() {
		if err := <-errCh; err != nil {
			log.Printf("[shutdown] Gateway shutdown error: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
		log.Println("[shutdown] Phase 1 complete: workers drained.")
	case <-shutdownCtx.Done():
		log.Println("[shutdown] Phase 1 timed out after 10s, proceeding anyway.")
	}

	// Phase 2: Close state manager backend (e.g. SQLiteStore) if available.
	log.Println("[shutdown] Phase 2: Closing state manager...")
	if c, ok := sm.(state.ClosableStateManager); ok {
		if err := c.Close(); err != nil {
			log.Printf("[shutdown] Failed to close state manager: %v", err)
		} else {
			log.Println("[shutdown] Phase 2 complete: state manager closed.")
		}
	} else {
		log.Println("[shutdown] Phase 2 complete: state manager has no Close method, skipping.")
	}

	log.Println("[shutdown] Graceful shutdown complete.")
}
