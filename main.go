package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/agentfactory/gateway/config"
	"github.com/agentfactory/gateway/gateway"
	"github.com/agentfactory/gateway/state"
	"github.com/agentfactory/gateway/worker"

	"github.com/slack-go/slack"
)

func main() {
	cfg := config.Load()

	if cfg.SlackBotToken == "" || cfg.SlackAppToken == "" {
		log.Fatal("SLACK_BOT_TOKEN and SLACK_APP_TOKEN must be set")
	}

	sm, err := state.NewStateManager("gateway_state.json")
	if err != nil {
		log.Fatalf("Failed to initialize state manager: %v", err)
	}

	w := worker.NewPythonWorker(cfg.PythonBin)
	if cfg.AFCLIBin != "" {
		w.WithAFCLI(cfg.AFCLIBin)
	}
	sw := worker.NewStreamWorker(cfg.PythonBin)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	// Crash recovery: reconcile any tasks left in "running" state from a previous run.
	log.Println("Running crash recovery...")
	gatewayClient := slack.New(cfg.SlackBotToken, slack.OptionAppLevelToken(cfg.SlackAppToken))
	if err := gateway.RecoverActiveTasks(ctx, sm, w, gatewayClient); err != nil {
		log.Printf("Recovery encountered errors: %v", err)
	}

	g := gateway.NewSlackGateway(cfg.SlackBotToken, cfg.SlackAppToken, w, sw, sm)

	// Run gateway in a goroutine so we can wait for shutdown signal.
	errCh := make(chan error, 1)
	go func() {
		log.Println("Starting AgentFactory Gateway...")
		errCh <- g.Start(ctx)
	}()

	// Wait for context cancellation (signal received) or gateway error.
	select {
	case <-ctx.Done():
		log.Println("Shutdown signal received, draining workers...")
		// Gateway's Start will call Stop() when ctx is cancelled.
	case err := <-errCh:
		if err != nil {
			log.Printf("Gateway exited with error: %v", err)
		}
		return
	}

	// Drain: wait for the gateway goroutine to finish.
	log.Println("Waiting for gateway to finish shutdown...")
	if err := <-errCh; err != nil {
		log.Printf("Gateway shutdown error: %v", err)
	}

	log.Println("Gateway shut down cleanly.")
}
