package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/eeyung1/multi-agent-orchestrator/internal/operations"
)

// Preserve all existing flat-flag workflows; operator commands are an explicit
// separate entry path and never fall through into activation or execution.
func dispatchOperations() bool {
	if len(os.Args) < 2 || !operations.IsCommand(os.Args[1]) {
		return false
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := operations.Run(ctx, os.Args[1:], os.Stdout); err != nil {
		fail(err)
	}
	return true
}
