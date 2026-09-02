// Command cards is the Cards binary. It has two modes:
//   - cards serve ...        : run the HTTP+UI server (see serveCmd)
//   - cards <cmd> ...        : CLI client against a running server (CARDS_URL)
//
// The CLI mirrors the HTTP API (docs/spec/api-surface.md). Global flags
// --url/--as/--json/--jsonl/--quiet may appear before the subcommand.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/somebox/cards/internal/cli"
	"github.com/somebox/cards/internal/httpapi"
)

func main() {
	httpapi.SetVersion(shortVersion()) // web UI nav shows the same version as --help
	if err := run(os.Args[1:]); err != nil {
		// A --help request is a successful, zero-exit outcome; the flags were
		// already printed to stdout by the command's FlagSet.
		if errors.Is(err, cli.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "cards:", err)
		os.Exit(cli.ExitCode(err))
	}
}

// Output convention (all subcommands): a command's product — listings, JSON,
// JSONL exports — goes to stdout; operational/progress messages go to stderr
// via log.Printf (serve, run-extensions) or fmt.Fprintf(os.Stderr, ...)
// (import/export summaries), so stdout stays pipeable.
func run(args []string) error {
	// Peel leading global flags (e.g. --url=... --as=... --workspace=... list ...).
	globals, rest := peelGlobals(args)
	if len(rest) == 0 {
		// A bare `cards` on an interactive terminal opens the TUI against the
		// resolved workspace. Non-interactive callers (piped streams, --json)
		// keep the original behavior — print usage and stay script-safe.
		if interactive(globals) {
			return tuiCmd(globals)
		}
		fmt.Print(usage)
		return nil
	}
	switch rest[0] {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	case "-v", "--version", "version":
		versionCmd()
		return nil
	}
	// --workspace is peeled as a global so it can precede the subcommand like
	// --url. Commands that open a workspace themselves (serve/export/import/mcp/
	// run-extensions/do/extensions) parse their own --workspace flag, so re-inject
	// the peeled value for them; `cards --workspace X serve` then works the same
	// as `cards serve --workspace X`. The client path reads globals.Workspace
	// directly (see runCLI).
	reinject := func(a []string) []string {
		if globals.Workspace == "" {
			return a
		}
		return append([]string{"--workspace", globals.Workspace}, a...)
	}
	// A leading flag (e.g. `cards --port 9000 --seed`) is serve with those
	// flags — the zero-config server, just customized.
	if len(rest[0]) > 0 && rest[0][0] == '-' {
		return serveCmd(reinject(rest))
	}
	switch rest[0] {
	case "serve":
		return serveCmd(reinject(rest[1:]))
	case "init":
		// --quiet is peeled as a global (so `cards --quiet init` and
		// `cards init --quiet` are equivalent); reinject it for init's own flag.
		initArgs := rest[1:]
		if globals.Quiet {
			initArgs = append([]string{"--quiet"}, initArgs...)
		}
		return initCmd(initArgs) // init takes a positional dir, not --workspace
	case "export":
		return exportCmd(reinject(rest[1:]))
	case "import":
		return importCmd(reinject(rest[1:]))
	case "mcp":
		return mcpCmd(reinject(rest[1:]))
	case "run-extensions":
		return runExtensionsCmd(reinject(rest[1:]))
	case "do":
		return doCmd(reinject(rest[1:]))
	case "extensions":
		return extensionsCmd(reinject(rest[1:]))
	default:
		return runCLI(globals, rest)
	}
}

// peelGlobals extracts --url/--as/--json/--jsonl/--quiet (and their env
// fallbacks) from anywhere in args — `cards list --json` and
// `cards --json list` are equivalent — returning the merged config + the
// remaining args (subcommand + its flags).
func peelGlobals(args []string) (cli.Config, []string) {
	cfg := cli.DefaultConfig()
	rest := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			cfg.JSON = true
		case a == "--jsonl":
			cfg.JSONL = true
		case a == "--quiet", a == "-q":
			cfg.Quiet = true
		case hasPrefix(a, "--url"):
			cfg.URL = val(a, args, &i)
		case hasPrefix(a, "--as"):
			cfg.As = val(a, args, &i)
		case hasPrefix(a, "--workspace"):
			cfg.Workspace = val(a, args, &i)
		default:
			rest = append(rest, a)
		}
	}
	return cfg, rest
}

func hasPrefix(a, p string) bool {
	return a == p || len(a) > len(p) && a[:len(p)+1] == p+"="
}

// val returns the value for "--flag" or "--flag=val", advancing i.
func val(a string, args []string, i *int) string {
	_, v, ok := splitEq(a)
	if ok {
		return v
	}
	if *i+1 < len(args) {
		*i++
		return args[*i]
	}
	return ""
}

func splitEq(a string) (string, string, bool) {
	for j := 0; j < len(a); j++ {
		if a[j] == '=' {
			return a[:j], a[j+1:], true
		}
	}
	return a, "", false
}

func runCLI(cfg cli.Config, rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("%s", usage)
	}
	name := rest[0]
	var cmd *cli.Command
	cmds := cli.Commands()
	for i := range cmds {
		if cmds[i].Name == name {
			cmd = &cmds[i]
			break
		}
	}
	if cmd == nil {
		return fmt.Errorf("unknown command %q\n%s", name, usage)
	}

	// Backend selection: an explicit CARDS_URL/--url talks to a running server
	// (preserving its event bus/SSE/hooks); otherwise run the router in-process
	// against the resolved workspace ($CARDS_WORKSPACE or --workspace, else the
	// nearest .cards/) — no server required. --workspace targets the serverless
	// backend only; it is meaningless against a running server (somebox/cards#17).
	if cfg.URL != "" {
		if cfg.Workspace != "" {
			return fmt.Errorf("--workspace has no effect with --url/CARDS_URL (the server owns its workspace)")
		}
		return cmd.Run(cli.New(cfg), rest[1:])
	}
	backend, err := newDirectBackend(cfg.Workspace)
	if err != nil {
		return err
	}
	defer backend.Close()
	return cmd.Run(cli.NewWithTransport(cfg, backend), rest[1:])
}

// usage is built at init so the header carries the running build's version —
// the same string the web UI nav shows (httpapi.SetVersion in serve.go).
var usage = "Cards " + shortVersion() + " — typed-card coordination." + usageBody

const usageBody = `

Usage:
  cards                                Interactive TUI on a terminal; this help otherwise
  cards version                        Print version, commit, and build info
  cards init [dir] [--global]          Scaffold a new workspace
  cards serve [--workspace <dir>] [--port 8787] [--seed]
  cards <command> [flags]              (serverless by default; CARDS_URL targets a server)
  cards <command> --help               List a command's flags

A bare cards command opens the terminal UI when stdin/stdout are both TTYs
(and neither --json nor --jsonl is set); it prints this help in scripts
and pipes. Quit the TUI with q.

Client commands run in-process against the resolved workspace
($CARDS_WORKSPACE, else nearest .cards/, else ~/.cards) with no server. Set
CARDS_URL (or --url) to talk to a running 'cards serve' instead — preferred
when a server is up so its event stream and hooks stay correct.

Global flags (before the command):
  --url URL        API base ($CARDS_URL); unset runs serverless in-process.
                   A bare host is fine — /v1 is appended if missing.
  --as USER        actor for writes (default $CARDS_USER)
  --workspace DIR  serverless workspace dir for client verbs — the .cards dir
                   or the project root holding it (overrides $CARDS_WORKSPACE;
                   ignored with --url)
  --json           pretty-print single object
  --jsonl          newline-delimited JSON (default for list/events)
  --quiet          ids only

Commands:
  list         List/search cards (--board/--owner/--status/--type/--q/--blocked)
               [--include links,comments] eager-loads relations (one call, no N+1)
  get <id>     Show one card
  create       --type T --title T [--status S] [--field k=v]... [--tag t]... [--dry-run]
  patch <id>   --version N [--title T] [--status S] [--owner U] [--field k=v]... [--dry-run]
  claim <id>   --version N [--status S]
  upgrade-schema <id>  [--target N] [--dry-run]
  take-next    [--type T] [--board B] [--assign-to U] [--status S] [--filter-file F]
  append <id> <field> --version N --entry-json '{...}'
  patch-entry <id> <field> <entry_id> --version N --entry-json '{...}'
  remove-entry <id> <field> <entry_id>
  link add <id> --type T --target ID [--note N]
  link remove <id> <type> <target>
  comment add <id> --body B
  comment <id> --body B        (alias for add)
  comment edit <id> <comment_id> --body B
  attach <id> <field> <file>   Upload a file to an artifact field
  events <id> [--types t1,t2] [--limit N]
  history <id>
  users register --id ID [--kind human|agent] [--display-name N]
  workspace show
  boards show [board_id]

  init         Scaffold a new workspace + install the agent skill (--no-skill to skip)
  serve        Run the HTTP + web UI server
  export       Dump all card data as JSONL (local; --workspace <dir>)
  import       Load a JSONL export into the workspace DB (local; --workspace <dir>)
  mcp          Run the stdio MCP server (--workspace <dir>; --print-instructions)
  run-extensions  Run the hook supervisor (--workspace <dir>)
  do <id>      Invoke a run extension (--param k=v)
  extensions   List/show declared extensions
`
