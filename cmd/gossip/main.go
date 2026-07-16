// ABOUTME: GOssip entrypoint: a standalone CLI for sharing gossip at the agentic watercooler.
// ABOUTME: One SQLite file is one watercooler; identity is declared via env, never authenticated.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	root := newRootCmd(os.Getenv, time.Now, os.Stdout)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gossip:", err)
		os.Exit(1)
	}
}
