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
		{Name: "claim", Short: "Atomically claim a card", Run: cmdClaim},
		{Name: "take-next", Short: "Pick + claim the next matching unowned card", Run: cmdTakeNext},
		{Name: "append", Short: "Append a repeating entry", Run: cmdAppend},
		{Name: "patch-entry", Short: "Update a repeating entry by entry_id", Run: cmdPatchEntry},
		{Name: "remove-entry", Short: "Remove a repeating entry by entry_id", Run: cmdRemoveEntry},
		{Name: "link", Short: "Manage links (add/remove)", Run: cmdLink},
		{Name: "comment", Short: "Manage comments (add/edit)", Run: cmdComment},
		{Name: "events", Short: "Show events for a card", Run: cmdEvents},
		{Name: "history", Short: "Show resumption history for a card", Run: cmdHistory},
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
	limit := fs.Int("limit", 50)
	cursor := fs.String("cursor", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v := url.Values{}
	add := func(k, val string) { if val != "" { v.Set(k, val) } }
	add("board_id", *board)
	add("owner", *owner)
	add("status", *status)
	add("type_id", *typ)
	add("q", *q)
	add("has_link", *hasLink)
	add("link_target", *linkTarget)
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
	if tags != nil {
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
		return fmt.Errorf("usage: cards comment add <id> --body B | cards comment edit <id> <comment_id> --body B")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "add":
		fs := NewFlagSet()
		body := fs.String("body", "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if len(fs.Args()) == 0 || *body == "" {
			return fmt.Errorf("usage: cards comment add <id> --body B")
		}
		data, _, err := c.do("POST", "/cards/"+fs.Args()[0]+"/comments", map[string]any{"body": *body})
		if err != nil {
			return err
		}
		c.Print(data, false, "id")
	case "edit":
		fs := NewFlagSet()
		body := fs.String("body", "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if len(fs.Args()) < 2 || *body == "" {
			return fmt.Errorf("usage: cards comment edit <id> <comment_id> --body B")
		}
		data, _, err := c.do("PATCH", "/cards/"+fs.Args()[0]+"/comments/"+fs.Args()[1], map[string]any{"body": *body})
		if err != nil {
			return err
		}
		c.Print(data, false, "id")
	default:
		return fmt.Errorf("unknown comment subcommand %q", sub)
	}
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
	// Single board: introspection doesn't have a per-board endpoint in POC,
	// so show the workspace and let the user find it.
	data, _, err := c.get("/workspace", nil)
	if err != nil {
		return err
	}
	c.Print(data, false, "")
	return nil
}

// --- helpers ---

// parseFields turns ["k=v","k2=v2"] into a map, coercing numeric values.
func parseFields(pairs []string) (map[string]any, error) {
	m := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("bad --field %q (want key=value)", p)
		}
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			m[k] = n
		} else {
			m[k] = v
		}
	}
	return m, nil
}
