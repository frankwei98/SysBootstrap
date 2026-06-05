package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/FrankWiZe/sys-bootstrap/internal/app"
	"github.com/FrankWiZe/sys-bootstrap/internal/cli"
)

func main() {
	registry := app.NewRegistry()

	args := os.Args[1:]

	if len(args) == 0 {
		// Default: run doctor, then offer run
		fmt.Println("sys-bootstrap — Server Provisioning Tool")
		fmt.Println("========================================")
		fmt.Println()

		fmt.Println("Environment Check:")
		result, err := cli.DoctorCmd()
		if err != nil {
			fmt.Printf("\n⚠ Critical issues detected. Fix them before running modules.\n")
			if result != nil && result.HasFatal {
				os.Exit(1)
			}
		}
		fmt.Println()

		// Ask whether to proceed into provisioning
		var proceed bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Enter provisioning?").
					Description("Run interactive module setup").
					Value(&proceed),
			),
		).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if !proceed {
			return
		}

		if err := cli.RunCmd(registry); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "run":
		if err := cli.RunCmd(registry); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "plan":
		jsonOutput := len(args) > 1 && args[1] == "--json"
		if err := cli.PlanCmd(registry, jsonOutput); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "doctor":
		_, err := cli.DoctorCmd()
		if err != nil {
			os.Exit(1)
		}

	case "module":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: sys-bootstrap module <id>\n")
			fmt.Fprintf(os.Stderr, "Available modules: %v\n", registry.IDs())
			os.Exit(1)
		}
		if err := cli.ModuleCmd(registry, args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "version":
		cli.VersionCmd()

	case "help", "--help", "-h":
		printUsage()
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: sys-bootstrap [command]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  run           Interactive provisioning (default)\n")
	fmt.Fprintf(os.Stderr, "  plan          Show execution plan\n")
	fmt.Fprintf(os.Stderr, "  plan --json   Show execution plan as JSON\n")
	fmt.Fprintf(os.Stderr, "  doctor        Check system compatibility\n")
	fmt.Fprintf(os.Stderr, "  module <id>   Run a single module\n")
	fmt.Fprintf(os.Stderr, "  version       Show version info\n")
	fmt.Fprintf(os.Stderr, "  help          Show this help message\n")
}
