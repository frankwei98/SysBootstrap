package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/huh"
	"github.com/frankwei98/sys-bootstrap/internal/app"
	"github.com/frankwei98/sys-bootstrap/internal/cli"
	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/settings"
)

func main() {
	// Create a signal-aware root context for all operations.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Load persistent settings first
	cfg, err := settings.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Initialize i18n: --lang flag > SYS_BOOTSTRAP_LANG env > settings > default en
	langFlag, filteredArgs := extractLangFlag(os.Args[1:])
	lang := i18n.DetectLang(langFlag, cfg.Lang)
	i18n.SetLang(lang)

	registry := app.NewRegistry()

	args := filteredArgs

	if len(args) == 0 {
		// Default: doctor → menu (provisioning / settings / exit)
		if err := mainMenu(ctx, registry); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "run":
		if err := cli.RunCmd(ctx, registry); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "plan":
		jsonOutput := len(args) > 1 && args[1] == "--json"
		if err := cli.PlanCmd(ctx, registry, jsonOutput); err != nil {
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
			fmt.Fprintf(os.Stderr, "%s\n", i18n.T("usage_module_need_id"))
			fmt.Fprintf(os.Stderr, "Available modules: %v\n", registry.IDs())
			os.Exit(1)
		}
		if err := cli.ModuleCmd(ctx, registry, args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "uninstall":
		if err := cli.UninstallCmd(ctx, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "config":
		if err := cli.ConfigCmd(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "version":
		cli.VersionCmd()

	case "help", "--help", "-h":
		printUsage()
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "%s\n\n", i18n.Tf("unknown_command", args[0]))
		printUsage()
		os.Exit(1)
	}
}

// mainMenu runs the doctor check then shows a menu with provisioning, settings, and exit.
func mainMenu(ctx context.Context, registry *modules.Registry) error {
	fmt.Println(i18n.T("app_title"))
	fmt.Println(i18n.T("app_separator"))
	fmt.Println()

	fmt.Println(i18n.T("env_check"))
	result, err := cli.DoctorCmd()
	if err != nil {
		fmt.Printf("\n%s\n", i18n.T("critical_issues"))
		if result != nil && result.HasFatal {
			os.Exit(1)
		}
	}
	fmt.Println()

	for {
		choice := "provisioning"
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("").
					Options(
						huh.NewOption(i18n.T("menu_enter_provisioning"), "provisioning"),
						huh.NewOption(i18n.T("menu_settings"), "settings"),
						huh.NewOption(i18n.T("menu_exit"), "exit"),
					).
					Value(&choice),
			),
		).Run(); err != nil {
			return err
		}

		switch choice {
		case "provisioning":
			return cli.RunCmd(ctx, registry)
		case "settings":
			if err := cli.ConfigCmd(nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			// Loop back to menu
		case "exit":
			return nil
		}
	}
}

// extractLangFlag looks for --lang in the given args without a full flag parser.
// Returns the language value (or "") and the args with --lang removed.
func extractLangFlag(args []string) (string, []string) {
	var lang string
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--lang" && i+1 < len(args) {
			lang = args[i+1]
			i++ // skip the value too
			continue
		}
		if len(args[i]) > 7 && args[i][:7] == "--lang=" {
			lang = args[i][7:]
			continue
		}
		filtered = append(filtered, args[i])
	}
	return lang, filtered
}

func printUsage() {
	fmt.Fprintf(os.Stderr, i18n.T("usage_title"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_commands"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_run"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_plan"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_plan_json"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_doctor"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_module"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_uninstall"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_config"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_version"))
	fmt.Fprintf(os.Stderr, i18n.T("usage_help"))
}
