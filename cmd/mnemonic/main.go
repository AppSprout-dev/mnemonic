package main

import (
	"flag"
	"fmt"
	"os"
)

var Version = "dev"

const (
	defaultConfigPath = "config.yaml"
	dataDir           = "~/.mnemonic"
	bufferSize        = 1000
)

// Exit codes for structured error reporting.
const (
	exitOK         = 0
	exitGeneral    = 1  // general/unknown error
	exitConfig     = 2  // configuration error (user-fixable)
	exitDatabase   = 3  // database / data integrity error
	exitNetwork    = 4  // network / connectivity error (transient)
	exitPermission = 5  // permission / access error (user-fixable)
	exitUsage      = 64 // bad command-line usage (matches sysexits.h EX_USAGE)
)

// ANSI color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// die prints an error message with an optional hint and exits with the given code.
func die(code int, msg string, hint string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	if hint != "" {
		fmt.Fprintf(os.Stderr, "  Try: %s\n", hint)
	}
	os.Exit(code)
}

func main() {
	// Parse global flags
	configPath := flag.String("config", defaultConfigPath, "path to config.yaml")
	flag.Parse()

	// Get subcommand from remaining arguments
	args := flag.Args()
	subcommand := "serve"
	if len(args) > 0 {
		subcommand = args[0]
	}

	// Handle help
	if subcommand == "--help" || subcommand == "-h" || subcommand == "help" {
		printUsage()
		os.Exit(0)
	}

	// Route to appropriate subcommand
	switch subcommand {
	case "serve":
		serveCommand(*configPath)
	case "start":
		startCommand(*configPath)
	case "stop":
		stopCommand()
	case "restart":
		restartCommand(*configPath)
	case "ingest":
		if len(args) < 2 {
			die(exitUsage, "'ingest' requires directory argument", "mnemonic ingest <directory> [--dry-run] [--project NAME]")
		}
		ingestCommand(*configPath, args[1:])
	case "remember":
		if len(args) < 2 {
			die(exitUsage, "'remember' requires text argument", "mnemonic remember \"your text here\"")
		}
		rememberCommand(*configPath, args[1])
	case "recall":
		if len(args) < 2 {
			die(exitUsage, "'recall' requires query argument", "mnemonic recall \"your query\"")
		}
		recallCommand(*configPath, args[1])
	case "status":
		statusCommand(*configPath)
	case "consolidate":
		consolidateCommand(*configPath)
	case "watch":
		watchCommand(*configPath)
	case "install":
		installCommand(*configPath)
	case "uninstall":
		uninstallCommand()
	case "export":
		exportCommand(*configPath, args)
	case "import":
		if len(args) < 2 {
			die(exitUsage, "'import' requires file path argument", "mnemonic import <backup.json> [--mode merge|replace]")
		}
		importCommand(*configPath, args[1], args)
	case "backup":
		backupCommand(*configPath)
	case "restore":
		if len(args) < 2 {
			die(exitUsage, "'restore' requires a backup file path", "mnemonic restore <backup.db>")
		}
		restoreCommand(*configPath, args[1])
	case "insights":
		insightsCommand(*configPath)
	case "meta-cycle":
		metaCycleCommand(*configPath)
	case "dream-cycle":
		dreamCycleCommand(*configPath)
	case "purge":
		purgeCommand(*configPath)
	case "cleanup":
		cleanupCommand(*configPath, args)
	case "mcp":
		mcpCommand(*configPath)
	case "autopilot":
		autopilotCommand(*configPath)
	case "diagnose":
		diagnoseCommand(*configPath)
	case "dedup":
		dryRun := true
		for _, a := range args[1:] {
			if a == "--apply" {
				dryRun = false
			}
		}
		dedupCommand(*configPath, dryRun)
	case "reset-patterns":
		dryRun := true
		for _, a := range args[1:] {
			if a == "--apply" {
				dryRun = false
			}
		}
		resetPatternsCommand(*configPath, dryRun)
	case "generate-token":
		generateTokenCommand()
	case "check-update":
		checkUpdateCommand()
	case "update":
		updateCommand(*configPath)
	case "version":
		fmt.Printf("mnemonic v%s\n", Version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", subcommand)
		printUsage()
		os.Exit(exitUsage)
	}
}

// printUsage prints the command usage.
func printUsage() {
	usage := `mnemonic v%s - A semantic memory system daemon

USAGE:
  mnemonic [OPTIONS] [COMMAND]

OPTIONS:
  --config PATH    Path to config.yaml (default: "config.yaml")
  --help          Show this help message

DAEMON COMMANDS:
  start           Start the mnemonic daemon (background)
  stop            Stop the running daemon
  restart         Restart the daemon
  serve           Run in foreground (for debugging)

MEMORY COMMANDS:
  remember TEXT   Store text in memory
  recall QUERY    Retrieve memories matching query
  consolidate     Run memory consolidation cycle

DATA MANAGEMENT:
  ingest DIR      Bulk-ingest a directory (--dry-run, --project NAME)
  export          Export memories (--format json|sqlite, --output path)
  import FILE     Import from JSON export (--mode merge|replace)
  backup          Timestamped backup with retention (keeps last 5)
  restore FILE    Restore database from a SQLite backup file
  cleanup         Remove noise: mark excluded-path raw events as processed (--yes)
  purge           Stop daemon and delete all data (fresh start)
  insights        Show metacognition observations (memory health)
  meta-cycle      Run a single metacognition analysis cycle
  dream-cycle     Run a single dream replay cycle

AI AGENT INTEGRATION:
  mcp             Run MCP server on stdin/stdout (for AI agents)

MONITORING COMMANDS:
  status          Show comprehensive system status
  diagnose        Run health checks (config, DB, LLM, disk)
  watch           Live stream of daemon events

UPDATE COMMANDS:
  check-update    Check if a newer version is available
  update          Download and install the latest version

SETUP COMMANDS:
  install         Install as system service (auto-start on login)
  uninstall       Remove system service
  generate-token  Generate a random API authentication token
  version         Show version

EXAMPLES:
  mnemonic start                                    Start daemon
  mnemonic status                                   Check everything
  mnemonic watch                                    Live event stream
  mnemonic remember "I learned something today"     Store a memory
  mnemonic recall "important lessons"               Retrieve memories
  mnemonic ingest ~/Projects/myapp --project myapp   Ingest a project
  mnemonic export --format json                     Export all data
  mnemonic backup                                   Quick backup
  mnemonic insights                                 Memory health report
  mnemonic dream-cycle                              Run dream replay
  mnemonic mcp                                      Start MCP server (stdio)
  mnemonic install                                  Auto-start on boot
  mnemonic autopilot                                Autonomous activity log
  mnemonic restore ~/.mnemonic/backups/backup.db    Restore from backup

EXIT CODES:
  0     Success
  1     General error
  2     Configuration error (check config.yaml)
  3     Database error (run 'mnemonic diagnose')
  4     Network/connectivity error (transient)
  5     Permission error (check file permissions)
  64    Bad command-line usage
`
	fmt.Printf(usage, Version)
}
