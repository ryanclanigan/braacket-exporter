package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{
			"playwright",
			"test",
			"e2e/region-ui.spec.ts",
			"--workers=1",
			"--timeout=120000",
		}
	}

	cmd := exec.Command("npx", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "failed to run npx %v: %v\n", args, err)
		os.Exit(1)
	}
}
