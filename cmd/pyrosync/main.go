package main

import (
	"fmt"
	"os"
)

const version = "0.1.0-phase0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println("pyrosync", version)
	case "serve", "ingest-file", "ingest-dir", "ingest-api", "recompute", "calibrate":
		fmt.Fprintf(os.Stderr, "subcommand %q will be implemented in Phase 1-3\n", os.Args[1])
		os.Exit(1)
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: pyrosync <command> [flags]")
	fmt.Println("Commands: serve, ingest-file, ingest-dir, ingest-api, recompute, calibrate, version")
}
