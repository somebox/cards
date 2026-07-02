// Command cards is the Work Cards binary. It has two modes:
//   - cards serve ...        : run the HTTP+UI server (see serveCmd)
//   - cards <cmd> ...        : CLI client against a running server (CARDS_URL)
//
// The CLI mirrors the HTTP API (docs/DEVELOPER-REFERENCE.md §9). Global flags
// --url/--as/--json/--jsonl/--quiet may appear before the subcommand.
package main

import (
	"fmt"
	"os"

	"github.com/somebox/cards/internal/cli"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cards:", err)
		os.Exit(1)
	}
}

// Output convention (all subcommands): a command's product — listings, JSON,
// JSONL exports — goes to stdout; operational/progress messages go to stderr
// via log.Printf (serve, run-extensions) or fmt.Fprintf(os.Stderr, ...)
// (import/export summaries), so stdout stays pipeable.
func run(args []string) error {
	// Peel leading global flags (e.g. --url=... --as=... list ...).
	globals, rest := peelGlobals(args)
	if len(rest) == 0 {
		// Zero-config: `cards` with no subcommand serves the resolved
		// workspace (nearest .cards/ or the personal workspace).
		return serveCmd(nil)
	}
	switch rest[0] {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	}
	// A leading flag (e.g. `cards --port 9000 --seed`) is serve with those
	// flags — the zero-config server, just customized.
	if len(rest[0]) > 0 && rest[0][0] == '-' {
		return serveCmd(rest)
	}
	switch rest[0] {
	case "serve":
		return serveCmd(rest[1:])
	case "init":
		return initCmd(rest[1:])
	case "export":
		return exportCmd(rest[1:])
	case "import":
		return importCmd(rest[1:])
	case "mcp":
		return mcpCmd(rest[1:])
	case "run-extensions":
		return runExtensionsCmd(rest[1:])
	case "do":
		return doCmd(rest[1:])
	case "extensions":
		return extensionsCmd(rest[1:])
	default:
		return runCLI(globals, rest)
	}
}

// peelGlobals extracts --url/--as/--json/--jsonl/--quiet (and their env
// fallbacks) from the front of args, returning the merged config + the
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
	for i := range cli.Commands() {
		if c := cli.Commands()[i]; c.Name == name {
			cmd = &c
			break
		}
	}
	if cmd == nil {
		return fmt.Errorf("unknown command %q\n%s", name, usage)
	}

	// Backend selection: an explicit CARDS_URL/--url talks to a running server
	// (preserving its event bus/SSE/hooks); otherwise run the router in-process
	// against the resolved workspace — no server required.
	if cfg.URL != "" {
		return cmd.Run(cli.New(cfg), rest[1:])
	}
	backend, err := newDirectBackend()
	if err != nil {
		return err
	}
	defer backend.Close()
	return cmd.Run(cli.NewWithTransport(cfg, backend), rest[1:])
}

const usage = `Work Cards — typed-card coordination.

Usage:
  cards                                (serve nearest .cards/ or ~/.cards)
  cards init [dir] [--global]          Scaffold a new workspace
  cards serve [--workspace <dir>] [--port 8787] [--seed]
  cards <command> [flags]              (serverless by default; CARDS_URL targets a server)

Client commands run in-process against the resolved workspace
($CARDS_WORKSPACE, else nearest .cards/, else ~/.cards) with no server. Set
CARDS_URL (or --url) to talk to a running 'cards serve' instead — preferred
when a server is up so its event stream and hooks stay correct.

Global flags (before the command):
  --url URL    API base ($CARDS_URL); unset runs serverless in-process
  --as USER    actor for writes (default $CARDS_USER)
  --json       pretty-print single object
  --jsonl      newline-delimited JSON (default for list/events)
  --quiet      ids only

Commands:
  list         List/search cards (--board/--owner/--status/--type/--q/--blocked)
  get <id>     Show one card
  create       --type T --title T [--status S] [--field k=v]... [--tag t]... [--dry-run]
  patch <id>   --version N [--status S] [--owner U] [--field k=v]... [--dry-run]
  claim <id>   --version N [--status S]
  upgrade-schema <id>  [--target N] [--dry-run]
  take-next    [--type T] [--board B] [--assign-to U] [--status S] [--filter-file F]
  append <id> <field> --version N --entry-json '{...}'
  patch-entry <id> <field> <entry_id> --version N --entry-json '{...}'
  remove-entry <id> <field> <entry_id>
  link add <id> --type T --target ID [--note N]
  link remove <id> <type> <target>
  comment add <id> --body B
  comment edit <id> <comment_id> --body B
  events <id> [--types t1,t2] [--limit N]
  history <id>
  users register --id ID [--kind human|agent] [--display-name N]
  workspace show
  boards show [board_id]

  init         Scaffold a new workspace (./.cards or, with --global, ~/.cards)
  serve        Run the HTTP + web UI server
  export       Dump all card data as JSONL (local; --workspace <dir>)
  import       Load a JSONL export into the workspace DB (local; --workspace <dir>)
  mcp          Run the stdio MCP server (--workspace <dir>)
  run-extensions  Run the hook supervisor (--workspace <dir>)
  do <id>      Invoke a run extension (--param k=v)
  extensions   List/show declared extensions
`
