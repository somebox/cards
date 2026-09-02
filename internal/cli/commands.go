// Package cli — commands.go contains the subcommand implementations. Each
// command mirrors an HTTP path from SPEC.md §11 / DEVELOPER-REFERENCE.md §9.
package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Command is a CLI subcommand.
type Command struct {
	Name  string
	Short string
	Run   func(c *Client, args []string) error
}

// Commands returns all registered subcommands (excluding `serve`, which is
// dispatched separately in cmd/cards since it doesn't use the HTTP client).
func Commands() []Command {
	return []Command{
		{Name: "list", Short: "List/search cards", Run: cmdList},
		{Name: "get", Short: "Show one card", Run: cmdGet},
		{Name: "create", Short: "Create a card", Run: cmdCreate},
		{Name: "patch", Short: "Patch a card (status/owner/tags/fields)", Run: cmdPatch},
		{Name: "release", Short: "Release a claimed card", Run: cmdRelease},
		{Name: "claim", Short: "Atomically claim a card", Run: cmdClaim},
		{Name: "delete", Short: "Delete a card (tombstone event)", Run: cmdDelete},
		{Name: "upgrade-schema", Short: "Upgrade a card to a newer schema version", Run: cmdUpgradeSchema},
		{Name: "take-next", Short: "Pick + claim the next matching unowned card", Run: cmdTakeNext},
		{Name: "append", Short: "Append a repeating entry", Run: cmdAppend},
		{Name: "patch-entry", Short: "Update a repeating entry by entry_id", Run: cmdPatchEntry},
		{Name: "remove-entry", Short: "Remove a repeating entry by entry_id", Run: cmdRemoveEntry},
		{Name: "link", Short: "Manage links (add/remove)", Run: cmdLink},
		{Name: "comment", Short: "Manage comments (add/edit)", Run: cmdComment},
		{Name: "attach", Short: "Attach a file to an artifact field", Run: cmdAttach},
		{Name: "reload", Short: "Reload workspace definitions on a running server", Run: cmdReload},
		{Name: "events", Short: "Show events for a card", Run: cmdEvents},
		{Name: "feed", Short: "Show the workspace event feed (durable, cursor-paged)", Run: cmdFeed},
		{Name: "history", Short: "Show resumption history for a card", Run: cmdHistory},
		{Name: "breaches", Short: "Show current breaching conditions (WIP/lane/blocked/status_timeout/card_idle)", Run: cmdBreaches},
		{Name: "users", Short: "Manage users (register)", Run: cmdUsers},
		{Name: "workspace", Short: "Show workspace introspection", Run: cmdWorkspace},
		{Name: "boards", Short: "Show a board", Run: cmdBoards},
	}
}

// --- cards ---

func cmdList(c *Client, args []string) error {
	fs := NewFlagSet()
	board := fs.String("board", "")
	owner := fs.String("owner", "")
	status := fs.String("status", "")
	typ := fs.String("type", "")
	q := fs.String("q", "")
	blocked := fs.Bool("blocked", false)
	hasLink := fs.String("has-link", "")
	linkTarget := fs.String("link-target", "")
	include := fs.String("include", "")
	limit := fs.Int("limit", 50)
	cursor := fs.String("cursor", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v := url.Values{}
	add := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	add("board_id", *board)
	add("owner", *owner)
	add("status", *status)
	add("type_id", *typ)
	add("q", *q)
	add("has_link", *hasLink)
	add("link_target", *linkTarget)
	add("include", *include)
	if *blocked {
		v.Set("blocked", "true")
	}
	if *limit != 50 {
		v.Set("limit", strconv.Itoa(*limit))
	}
	add("cursor", *cursor)
	data, _, err := c.get("/cards", v)
	if err != nil {
		return err
	}
	c.Print(data, true, "id")
	return nil
}

func cmdGet(c *Client, args []string) error {
	fs := NewFlagSet()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards get <id>")
	}
	data, _, err := c.get("/cards/"+fs.Args()[0], nil)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdCreate(c *Client, args []string) error {
	fs := NewFlagSet()
	typ := fs.String("type", "")
	title := fs.String("title", "")
	status := fs.String("status", "")
	fields := fs.StringArr("field", nil)
	tags := fs.StringArr("tag", nil)
	dryRun := fs.Bool("dry-run", false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *typ == "" || *title == "" {
		return fmt.Errorf("--type and --title are required")
	}
	body := map[string]any{"type_id": *typ, "title": *title}
	if *status != "" {
		body["status"] = *status
	}
	if len(*fields) > 0 {
		fm, err := parseFields(*fields)
		if err != nil {
			return err
		}
		body["fields"] = fm
	}
	if len(*tags) > 0 {
		body["tags"] = *tags
	}
	if *dryRun {
		body["dry_run"] = true
	}
	data, _, err := c.do("POST", "/cards", body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdPatch(c *Client, args []string) error {
	fs := NewFlagSet()
	title := fs.String("title", "")
	status := fs.String("status", "")
	owner := fs.String("owner", "")
	fields := fs.StringArr("field", nil)
	tags := fs.StringArr("tag", nil)
	version := fs.Int("version", 0)
	dryRun := fs.Bool("dry-run", false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards patch <id> [--version N]")
	}
	if *version == 0 {
		return fmt.Errorf("--version is required (optimistic concurrency)")
	}
	body := map[string]any{"version": *version}
	if *title != "" {
		body["title"] = *title
	}
	if *status != "" {
		body["status"] = *status
	}
	if *owner != "" {
		body["owner"] = *owner
	}
	if len(*fields) > 0 {
		fm, err := parseFields(*fields)
		if err != nil {
			return err
		}
		body["fields"] = fm
	}
	// Mirror cmdCreate: an unset --tag must not send "tags" at all (StringArr
	// always returns a non-nil pointer, so a nil-check alone always passes).
	if len(*tags) > 0 {
		body["tags"] = *tags
	}
	if *dryRun {
		body["dry_run"] = true
	}
	data, _, err := c.do("PATCH", "/cards/"+fs.Args()[0], body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdUpgradeSchema(c *Client, args []string) error {
	fs := NewFlagSet()
	target := fs.Int("target", 0)
	dryRun := fs.Bool("dry-run", false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards upgrade-schema <id> [--target N] [--dry-run]")
	}
	body := map[string]any{}
	if *target != 0 {
		body["target_version"] = *target
	}
	if *dryRun {
		body["dry_run"] = true
	}
	data, _, err := c.do("POST", "/cards/"+fs.Args()[0]+"/upgrade-schema", body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdClaim(c *Client, args []string) error {
	fs := NewFlagSet()
	status := fs.String("status", "")
	version := fs.Int("version", 0)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards claim <id> [--version N]")
	}
	if *version == 0 {
		return fmt.Errorf("--version is required")
	}
	body := map[string]any{"version": *version}
	if *status != "" {
		body["status"] = *status
	}
	data, _, err := c.do("POST", "/cards/"+fs.Args()[0]+"/claim", body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdRelease(c *Client, args []string) error {
	fs := NewFlagSet()
	status := fs.String("status", "")
	version := fs.Int("version", 0)
	force := fs.Bool("force", false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards release <id> --version N [--status S] [--force]")
	}
	if *version == 0 {
		return fmt.Errorf("--version is required")
	}
	body := map[string]any{"version": *version}
	if *status != "" {
		body["status"] = *status
	}
	if *force {
		body["force"] = true
	}
	data, _, err := c.do("POST", "/cards/"+fs.Args()[0]+"/release", body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdDelete(c *Client, args []string) error {
	fs := NewFlagSet()
	// --version is an optional optimistic-concurrency guard; omitted → delete
	// unconditionally (convenient for board hygiene / bulk cleanup).
	version := fs.Int("version", 0)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards delete <id> [--version N]")
	}
	body := map[string]any{}
	if *version != 0 {
		body["version"] = *version
	}
	data, _, err := c.do("DELETE", "/cards/"+fs.Args()[0], body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdTakeNext(c *Client, args []string) error {
	fs := NewFlagSet()
	assignTo := fs.String("assign-to", "")
	status := fs.String("status", "")
	typ := fs.String("type", "")
	board := fs.String("board", "")
	filterFile := fs.String("filter-file", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body := map[string]any{}
	if *assignTo != "" {
		body["assign_to"] = *assignTo
	}
	if *status != "" {
		body["status"] = *status
	}
	if *typ != "" {
		body["type_id"] = *typ
	}
	if *board != "" {
		body["board_id"] = *board
	}
	if *filterFile != "" {
		raw, err := os.ReadFile(*filterFile)
		if err != nil {
			return err
		}
		var flt map[string]any
		if err := json.Unmarshal(raw, &flt); err != nil {
			return fmt.Errorf("filter-file: %w", err)
		}
		body["filter"] = flt
	}
	data, _, err := c.do("POST", "/cards/take-next", body)
	if err != nil {
		return err
	}
	// take-next returns {"card": {...}} or {"card": null}
	var env struct {
		Card map[string]any `json:"card"`
	}
	if json.Unmarshal(data, &env) == nil && env.Card == nil {
		fmt.Fprintln(os.Stderr, "no matching card")
		return nil
	}
	c.Print(data, false, "card.id")
	return nil
}

func cmdAppend(c *Client, args []string) error {
	fs := NewFlagSet()
	entryJSON := fs.String("entry-json", "")
	version := fs.Int("version", 0)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 || *version == 0 {
		return fmt.Errorf("usage: cards append <id> <field> --version N --entry-json '{...}'")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(*entryJSON), &entry); err != nil {
		return fmt.Errorf("--entry-json: %w", err)
	}
	body := map[string]any{"version": *version, "entry": entry}
	data, _, err := c.do("POST", "/cards/"+fs.Args()[0]+"/fields/"+fs.Args()[1]+"/append", body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdPatchEntry(c *Client, args []string) error {
	fs := NewFlagSet()
	entryJSON := fs.String("entry-json", "")
	version := fs.Int("version", 0)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 3 || *version == 0 {
		return fmt.Errorf("usage: cards patch-entry <id> <field> <entry_id> --version N --entry-json '{...}'")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(*entryJSON), &entry); err != nil {
		return fmt.Errorf("--entry-json: %w", err)
	}
	body := map[string]any{"version": *version, "entry": entry}
	data, _, err := c.do("PATCH", "/cards/"+fs.Args()[0]+"/fields/"+fs.Args()[1]+"/"+fs.Args()[2], body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdRemoveEntry(c *Client, args []string) error {
	fs := NewFlagSet()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 3 {
		return fmt.Errorf("usage: cards remove-entry <id> <field> <entry_id>")
	}
	data, _, err := c.do("DELETE", "/cards/"+fs.Args()[0]+"/fields/"+fs.Args()[1]+"/"+fs.Args()[2], nil)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdLink(c *Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cards link add <id> --type T --target ID | cards link remove <id> <type> <target>")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "add":
		fs := NewFlagSet()
		typ := fs.String("type", "")
		target := fs.String("target", "")
		note := fs.String("note", "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if len(fs.Args()) == 0 || *typ == "" || *target == "" {
			return fmt.Errorf("usage: cards link add <id> --type T --target ID [--note N]")
		}
		body := map[string]any{"type_id": *typ, "target": *target}
		if *note != "" {
			body["note"] = *note
		}
		data, _, err := c.do("POST", "/cards/"+fs.Args()[0]+"/links", body)
		if err != nil {
			return err
		}
		c.Print(data, false, "id")
	case "remove":
		if len(rest) < 3 {
			return fmt.Errorf("usage: cards link remove <id> <type> <target>")
		}
		data, _, err := c.do("DELETE", "/cards/"+rest[0]+"/links/"+rest[1]+"/"+rest[2], nil)
		if err != nil {
			return err
		}
		c.Print(data, false, "id")
	default:
		return fmt.Errorf("unknown link subcommand %q", sub)
	}
	return nil
}

func cmdComment(c *Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", commentUsage)
	}
	switch args[0] {
	case "-h", "--help", "--h", "help":
		fmt.Println(commentUsage)
		return ErrHelp
	case "add":
		return commentAdd(c, args[1:])
	case "edit":
		return commentEdit(c, args[1:])
	default:
		// `cards comment <id> --body B` is an alias for add. A card id as the
		// first token used to hit "unknown comment subcommand".
		return commentAdd(c, args)
	}
}

const commentUsage = `usage:
  cards comment add <id> --body B
  cards comment edit <id> <comment_id> --body B
  cards comment <id> --body B`

func commentAdd(c *Client, args []string) error {
	fs := NewFlagSet()
	body := fs.String("body", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 || *body == "" {
		return fmt.Errorf("%s", commentUsage)
	}
	data, _, err := c.do("POST", "/cards/"+fs.Args()[0]+"/comments", map[string]any{"body": *body})
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func commentEdit(c *Client, args []string) error {
	fs := NewFlagSet()
	body := fs.String("body", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 || *body == "" {
		return fmt.Errorf("%s", commentUsage)
	}
	data, _, err := c.do("PATCH", "/cards/"+fs.Args()[0]+"/comments/"+fs.Args()[1], map[string]any{"body": *body})
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

// cmdAttach uploads a local file as the bytes for an artifact field, printing
// the updated card. The file is sent as the raw request body (the server
// content-addresses and confines it); works serverless or against a server.
// cmdReload asks a running server to re-run the definitions loader and swap
// the workspace (POST /v1/workspace/reload). Server-only by nature: the
// serverless backend re-loads definitions on every invocation anyway.
func cmdReload(c *Client, args []string) error {
	fs := NewFlagSet()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if c.cfg.URL == "" {
		return fmt.Errorf("reload needs a running server (set CARDS_URL or --url); serverless runs reload definitions on every invocation")
	}
	resp, _, err := c.do("POST", "/workspace/reload", nil)
	if err != nil {
		return err
	}
	c.Print(resp, false, "reloaded")
	return nil
}

func cmdAttach(c *Client, args []string) error {
	fs := NewFlagSet()
	version := fs.Int("version", 0) // optional optimistic-concurrency guard (0 = none)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 3 {
		return fmt.Errorf("usage: cards attach <id> <field> <file>")
	}
	id, field, path := fs.Args()[0], fs.Args()[1], fs.Args()[2]
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	route := "/cards/" + id + "/artifacts/" + field
	if *version != 0 {
		route += "?version=" + strconv.Itoa(*version)
	}
	resp, _, err := c.doRaw("POST", route, data)
	if err != nil {
		return err
	}
	c.Print(resp, false, "id")
	return nil
}

func cmdEvents(c *Client, args []string) error {
	fs := NewFlagSet()
	types := fs.String("types", "")
	limit := fs.Int("limit", 50)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards events <id>")
	}
	v := url.Values{}
	if *types != "" {
		v.Set("types", *types)
	}
	if *limit != 50 {
		v.Set("limit", strconv.Itoa(*limit))
	}
	data, _, err := c.get("/cards/"+fs.Args()[0]+"/events", v)
	if err != nil {
		return err
	}
	c.Print(data, true, "id")
	return nil
}

// cmdFeed shows the durable workspace event feed (GET /v1/events) — the
// cross-card counterpart to `cards events <id>`. Replay missed facts by
// passing --since/--cursor (an event-id floor); the page's next_cursor is
// printed to stderr so stdout stays clean JSONL.
func cmdFeed(c *Client, args []string) error {
	fs := NewFlagSet()
	types := fs.String("types", "")
	board := fs.String("board", "")
	actor := fs.String("actor", "")
	owner := fs.String("owner", "")
	since := fs.String("since", "")
	cursor := fs.String("cursor", "")
	limit := fs.Int("limit", 50)
	if err := fs.Parse(args); err != nil {
		return err
	}
	v := url.Values{}
	add := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	add("types", *types)
	add("board_id", *board)
	add("actor", *actor)
	add("owner", *owner)
	add("since", *since)
	add("cursor", *cursor)
	if *limit != 50 {
		v.Set("limit", strconv.Itoa(*limit))
	}
	data, _, err := c.get("/events", v)
	if err != nil {
		return err
	}
	c.Print(data, true, "id")
	// Surface the continuation cursor on stderr (stdout stays valid JSONL).
	var env struct {
		NextCursor string `json:"next_cursor"`
	}
	if json.Unmarshal(data, &env) == nil && env.NextCursor != "" {
		fmt.Fprintf(os.Stderr, "next_cursor: %s\n", env.NextCursor)
	}
	return nil
}

// cmdBreaches shows the current breaching conditions (GET /v1/breaches):
// which board columns exceed their WIP limit, which watched lanes are
// drained, which cards are blocked, and which monitored cards are past a
// status/idle deadline — the catch-up counterpart to the ephemeral condition
// signals on the event stream. Item scans cap at 500 (see truncated/limit).
func cmdBreaches(c *Client, args []string) error {
	fs := NewFlagSet()
	board := fs.String("board", "")
	types := fs.String("type", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v := url.Values{}
	if *board != "" {
		v.Set("board_id", *board)
	}
	if *types != "" {
		v.Set("type", *types)
	}
	data, _, err := c.get("/breaches", v)
	if err != nil {
		return err
	}
	c.Print(data, true, "card_id")
	return nil
}

func cmdHistory(c *Client, args []string) error {
	fs := NewFlagSet()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: cards history <id>")
	}
	data, _, err := c.get("/cards/"+fs.Args()[0]+"/history", nil)
	if err != nil {
		return err
	}
	c.Print(data, true, "")
	return nil
}

func cmdUsers(c *Client, args []string) error {
	if len(args) == 0 || args[0] != "register" {
		return fmt.Errorf("usage: cards users register --id ID [--kind human|agent] [--display-name N]")
	}
	fs := NewFlagSet()
	id := fs.String("id", "")
	kind := fs.String("kind", "human")
	dn := fs.String("display-name", "")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	body := map[string]any{"id": *id, "kind": *kind}
	if *dn != "" {
		body["display_name"] = *dn
	}
	data, _, err := c.do("POST", "/users", body)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

func cmdWorkspace(c *Client, args []string) error {
	fs := NewFlagSet()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 && fs.Args()[0] == "show" {
		data, _, err := c.get("/workspace", nil)
		if err != nil {
			return err
		}
		c.Print(data, false, "")
		return nil
	}
	return fmt.Errorf("usage: cards workspace show")
}

func cmdBoards(c *Client, args []string) error {
	fs := NewFlagSet()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 || fs.Args()[0] != "show" {
		return fmt.Errorf("usage: cards boards show [board_id]")
	}
	if len(fs.Args()) < 2 {
		data, _, err := c.get("/workspace", nil)
		if err != nil {
			return err
		}
		c.Print(data, false, "")
		return nil
	}
	// Single board via GET /v1/boards/{id}.
	data, _, err := c.get("/boards/"+fs.Args()[1], nil)
	if err != nil {
		return err
	}
	c.Print(data, false, "id")
	return nil
}

// --- helpers ---

// parseFields turns ["k=v","k2=v2"] into a map, coercing numeric values. A
// value that looks like a JSON array is decoded as one — the shape multi-value
// (multiple:true) fields expect: --field 'platforms=["desktop","mobile"]'.
// An empty array unsets the field (the multi-value unset contract).
func parseFields(pairs []string) (map[string]any, error) {
	m := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("bad --field %q (want key=value)", p)
		}
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			var arr []any
			if err := json.Unmarshal([]byte(v), &arr); err != nil {
				return nil, fmt.Errorf("bad --field %q: value looks like a JSON array but does not parse: %v", k, err)
			}
			m[k] = arr
			continue
		}
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			m[k] = n
		} else {
			m[k] = v
		}
	}
	return m, nil
}
