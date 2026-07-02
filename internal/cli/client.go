// Package cli implements the `cards` command-line client. It mirrors the
// HTTP API (docs/SPEC.md §11, docs/DEVELOPER-REFERENCE.md §9) over a small
// HTTP client, so the same paths/flags work against a `cards serve` sidecar.
//
// Output modes: --json (single object), --jsonl (newline-delimited, default
// for list/events), --quiet (ids only). Errors go to stderr as structured
// JSON per SPEC §10.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds the resolved global flags for a command invocation.
type Config struct {
	URL    string // CARDS_URL / --url ; default http://127.0.0.1:8787/v1
	As     string // CARDS_USER / --as ; actor for writes
	Quiet  bool   // ids only
	JSON   bool   // single JSON object
	JSONL  bool   // newline-delimited JSON
}

// DefaultConfig resolves from env vars. URL is left empty when CARDS_URL is
// unset: an empty URL selects the serverless (in-process) backend, a set URL
// targets a running `cards serve`.
func DefaultConfig() Config {
	return Config{
		URL: os.Getenv("CARDS_URL"),
		As:  os.Getenv("CARDS_USER"),
	}
}

// Transport performs one API round-trip. The HTTP implementation talks to a
// `cards serve` process; cmd/cards provides an in-process implementation that
// runs the same /v1 router directly against a workspace — no server required.
type Transport interface {
	Do(method, path string, body []byte, header http.Header) (status int, resp []byte, err error)
}

// httpTransport is the default Transport: a real HTTP client against cfg.URL.
type httpTransport struct {
	base string
	hc   *http.Client
}

func (t httpTransport) Do(method, path string, body []byte, header http.Header) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, t.base+path, r)
	if err != nil {
		return 0, nil, err
	}
	if header != nil {
		req.Header = header
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, nil
}

// Client is bound to a Config and a Transport (HTTP or in-process).
type Client struct {
	cfg Config
	t   Transport
}

// New returns a Client that talks HTTP to cfg.URL.
func New(cfg Config) *Client {
	return &Client{cfg: cfg, t: httpTransport{base: cfg.URL, hc: http.DefaultClient}}
}

// NewWithTransport returns a Client over a custom Transport (e.g. in-process).
func NewWithTransport(cfg Config, t Transport) *Client {
	return &Client{cfg: cfg, t: t}
}

// --- request helpers ---

// do performs a request and returns the raw body + status. On a non-2xx it
// returns a *cliError parsed from the JSON error body.
func (c *Client) do(method, path string, body any) ([]byte, int, error) {
	var raw []byte
	hdr := http.Header{}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		raw = b
		hdr.Set("Content-Type", "application/json")
	}
	if c.cfg.As != "" {
		hdr.Set("X-Work-Cards-Actor", c.cfg.As)
	}
	status, data, err := c.t.Do(method, path, raw, hdr)
	if err != nil {
		return nil, 0, err
	}
	if status >= 400 {
		return data, status, parseErr(data)
	}
	return data, status, nil
}

// get is a convenience for GET with query params.
func (c *Client) get(path string, q url.Values) ([]byte, int, error) {
	if len(q) > 0 {
		path = path + "?" + q.Encode()
	}
	return c.do(http.MethodGet, path, nil)
}

// cliError wraps a SPEC §10 structured error.
type cliError struct {
	Code         string   `json:"error"`
	Message      string   `json:"message"`
	Field        string   `json:"field,omitempty"`
	Value        any      `json:"value,omitempty"`
	ValidOptions []string `json:"valid_options,omitempty"`
	Hint         string   `json:"hint,omitempty"`
}

func (e *cliError) Error() string {
	var b strings.Builder
	b.WriteString(e.Code)
	if e.Field != "" {
		b.WriteString(" (" + e.Field + ")")
	}
	b.WriteString(": " + e.Message)
	if len(e.ValidOptions) > 0 {
		b.WriteString(" [valid: " + strings.Join(e.ValidOptions, ", ") + "]")
	}
	if e.Hint != "" {
		b.WriteString(" — " + e.Hint)
	}
	return b.String()
}

func parseErr(data []byte) error {
	var e cliError
	if err := json.Unmarshal(data, &e); err != nil || e.Code == "" {
		return fmt.Errorf("server error: %s", strings.TrimSpace(string(data)))
	}
	return &e
}

// --- output helpers ---

// Print writes output according to the configured mode.
//   - --quiet: only `item` field (id), one per line
//   - --json: pretty-printed single object
//   - --jsonl (default for collections): compact JSON per line
//
// `items` is the list of objects to print; `isCollection` hints the default.
func (c *Client) Print(data []byte, isCollection bool, idPath string) {
	switch {
	case c.cfg.Quiet:
		// Extract ids from a {"items":[...]} envelope.
		var env struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(data, &env); err == nil && len(env.Items) > 0 {
			for _, it := range env.Items {
				fmt.Println(idOf(it, idPath))
			}
			return
		}
		// Fallback: single object id.
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			if id := idOf(m, idPath); id != "" {
				fmt.Println(id)
				return
			}
		}
		fmt.Println(string(data))
	case c.cfg.JSON:
		// Pretty print.
		var v any
		if json.Unmarshal(data, &v) == nil {
			b, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(b))
			return
		}
		fmt.Println(string(data))
	default:
		// jsonl for collections, pretty for single objects.
		if isCollection {
			var env struct {
				Items []json.RawMessage `json:"items"`
			}
			if json.Unmarshal(data, &env) == nil {
				for _, it := range env.Items {
					fmt.Println(string(it))
				}
				return
			}
		}
		var v any
		if json.Unmarshal(data, &v) == nil {
			b, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(b))
			return
		}
		fmt.Println(string(data))
	}
}

// idOf extracts an id from a map. path may be dotted (e.g. "card.id") to
// descend nested envelopes like take-next's {"card": {...}}.
func idOf(m map[string]any, path string) string {
	if path == "" {
		path = "id"
	}
	cur := any(m)
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	if v, ok := cur.(string); ok {
		return v
	}
	return ""
}

// --- flag parsing helpers ---

// FlagSet is a minimal flag parser supporting --flag, --flag=val, --flag val,
// and boolean flags. It returns parsed flags + remaining positional args.
type FlagSet struct {
	bools  map[string]*bool
	strs   map[string]*string
	ints   map[string]*int
	strArr map[string]*[]string
	args   []string
}

func NewFlagSet() *FlagSet {
	return &FlagSet{
		bools: map[string]*bool{}, strs: map[string]*string{},
		ints: map[string]*int{}, strArr: map[string]*[]string{},
	}
}
func (f *FlagSet) Bool(name string, dflt bool) *bool    { v := dflt; f.bools[name] = &v; return &v }
func (f *FlagSet) String(name, dflt string) *string     { v := dflt; f.strs[name] = &v; return &v }
func (f *FlagSet) Int(name string, dflt int) *int        { v := dflt; f.ints[name] = &v; return &v }
func (f *FlagSet) StringArr(name string, dflt []string) *[]string { v := dflt; f.strArr[name] = &v; return &v }
func (f *FlagSet) Args() []string                        { return f.args }

// Parse consumes args (without the subcommand name).
func (f *FlagSet) Parse(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			f.args = append(f.args, a)
			continue
		}
		a = strings.TrimPrefix(a, "--")
		name, val, hasVal := strings.Cut(a, "=")
		if _, ok := f.bools[name]; ok {
			if hasVal {
				b, err := strconv.ParseBool(val)
				if err != nil {
					return fmt.Errorf("--%s: invalid bool", name)
				}
				*f.bools[name] = b
			} else {
				*f.bools[name] = true
			}
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return fmt.Errorf("--%s requires a value", name)
			}
			i++
			val = args[i]
		}
		switch {
		case f.strs[name] != nil:
			*f.strs[name] = val
		case f.ints[name] != nil:
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("--%s: invalid int", name)
			}
			*f.ints[name] = n
		case f.strArr[name] != nil:
			*f.strArr[name] = append(*f.strArr[name], val)
		default:
			return fmt.Errorf("unknown flag --%s", name)
		}
	}
	return nil
}
