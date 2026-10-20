package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/eeyung1/multi-agent-orchestrator/internal/activation"
)

func main() {
	var workspace, agent, role string
	flag.StringVar(&workspace, "workspace", ".", "target project workspace")
	flag.StringVar(&agent, "agent", "", "agent identity prefix")
	flag.StringVar(&role, "role", "coding-agent", "agent role")
	flag.Parse()

	if agent == "" {
		fmt.Fprintln(os.Stderr, "--agent is required")
		os.Exit(2)
	}

	result, err := activation.Activate(workspace, agent, role)
	if err != nil {
		fmt.Fprintln(os.Stderr, "activation failed:", err)
		os.Exit(1)
	}

	fmt.Printf("agent=%s state=%s coordination=%s\n", result.SessionID, result.State, result.CoordinationDir)
}
