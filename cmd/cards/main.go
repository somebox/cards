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

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	// Peel leading global flags (e.g. --url=... --as=... list ...).
	globals, rest := peelGlobals(args)
	if len(rest) == 0 {
		return fmt.Errorf("%s\n\nmissing subcommand", usage)
	}
	switch rest[0] {
	case "serve":
		return serveCmd(rest[1:])
	case "mcp":
		return mcpCmd(rest[1:])
	case "run-extensions":
		return runExtensionsCmd(rest[1:])
	case "do":
		return doCmd(rest[1:])
	case "extensions":
		return extensionsCmd(rest[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
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
	for _, cmd := range cli.Commands() {
		if cmd.Name == name {
			return cmd.Run(cli.New(cfg), rest[1:])
		}
	}
	return fmt.Errorf("unknown command %q\n%s", name, usage)
}

const usage = `Work Cards — typed-card coordination.

Usage:
  cards serve --workspace <dir> [--port 8787] [--seed]
  cards <command> [flags]              (client; set CARDS_URL or --url)

Global flags (before the command):
  --url URL    API base (default $CARDS_URL or http://127.0.0.1:8787/v1)
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

  serve        Run the HTTP + web UI server
  mcp          Run the stdio MCP server (--workspace <dir>)
  run-extensions  Run the hook supervisor (--workspace <dir>)
  do <id>      Invoke a run extension (--param k=v)
  extensions   List/show declared extensions
`
