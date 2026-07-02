// Package sqlite is the default storage implementation: cards, events, links,
// comments, users, idempotency keys, and an FTS5 virtual table for search.
//
// One workspace = one SQLite file. See docs/ARCHITECTURE.md (Storage).
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/somebox/cards/internal/core"
	_ "modernc.org/sqlite" // pure-Go driver
)

// Store implements core.Store using modernc.org/sqlite.
type Store struct {
	db *sql.DB
	ws *core.Workspace
}

// Open opens (or creates) the SQLite file at path and initializes schema.
func Open(path string, ws *core.Workspace) (*Store, error) {
	// WAL for concurrent readers; busy_timeout so a blocked writer waits rather
	// than failing; synchronous=NORMAL is the safe/fast WAL pairing; foreign_keys
	// on; _txlock=immediate makes every BeginTx grab the write lock up front, so
	// read-then-write transactions (e.g. ClaimAtomic/take-next) can't hit
	// SQLITE_BUSY_SNAPSHOT — which busy_timeout cannot retry away. This matters
	// across processes too (a `cards serve` and a serverless CLI on one DB).
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Serialize access to the single DB file through one connection. At this
	// scale queries are sub-millisecond, so the simplicity (no in-process write
	// contention, and a stable handle for :memory:) outweighs lost read
	// parallelism. A separate read pool is a possible future optimization.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db, ws: ws}
	if err := s.Init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Init creates tables/indexes if missing.
func (s *Store) Init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cards (
			id              TEXT PRIMARY KEY,
			workspace_id    TEXT NOT NULL,
			type_id         TEXT NOT NULL,
			schema_version  INTEGER NOT NULL,
			title           TEXT NOT NULL,
			status          TEXT NOT NULL,
			owner           TEXT,
			tags            TEXT,
			fields          TEXT NOT NULL,
			version         INTEGER NOT NULL,
			created_at      TEXT NOT NULL,
			updated_at      TEXT NOT NULL,
			created_by      TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cards_status_updated ON cards(status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cards_type ON cards(type_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cards_owner ON cards(owner)`,
		`CREATE INDEX IF NOT EXISTS idx_cards_updated ON cards(updated_at)`,
		// card_id is nullable (board-scoped events have none); scope defaults to
		// 'card'. Old DBs are migrated by migrateEventsScope below. (seam 2a)
		`CREATE TABLE IF NOT EXISTS events (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			card_id  TEXT,
			board_id TEXT,
			scope    TEXT NOT NULL DEFAULT 'card',
			type     TEXT NOT NULL,
			actor    TEXT NOT NULL,
			at       TEXT NOT NULL,
			diff     TEXT
		)`,
		// events indexes are created after migrateEventsScope below — the
		// migration rebuilds the table, which would drop them.
		`CREATE TABLE IF NOT EXISTS users (
			id           TEXT PRIMARY KEY,
			display_name TEXT,
			kind         TEXT,
			created_at   TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS links (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id   TEXT NOT NULL,
			type_id     TEXT NOT NULL,
			target      TEXT NOT NULL,
			note        TEXT,
			created_by  TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			UNIQUE(source_id, type_id, target)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_links_source ON links(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_links_target ON links(target)`,
		`CREATE TABLE IF NOT EXISTS comments (
			id         TEXT PRIMARY KEY,
			card_id    TEXT NOT NULL,
			author     TEXT NOT NULL,
			body       TEXT NOT NULL,
			created_at TEXT NOT NULL,
			edited_at  TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_comments_card ON comments(card_id)`,
		`DROP TABLE IF EXISTS idempotency_keys`, // ephemeral cache; safe to clear on restart
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			key        TEXT NOT NULL,
			actor      TEXT NOT NULL,
			status     INTEGER NOT NULL,
			body       TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (key, actor)
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS fts_cards USING fts5(card_id UNINDEXED, title, body)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("init schema: %w (stmt: %s)", err, q)
		}
	}
	// Migrate old DBs BEFORE indexing events: an old events table is rebuilt
	// (rename → copy → drop), which discards its indexes, and it has no scope
	// column until then. Init owns all events indexes; the migration owns none.
	if err := s.migrateEventsScope(ctx); err != nil {
		return err
	}
	eventIdx := []string{
		`CREATE INDEX IF NOT EXISTS idx_events_card ON events(card_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_at ON events(at)`,
		`CREATE INDEX IF NOT EXISTS idx_events_scope ON events(scope, board_id, id)`,
	}
	for _, q := range eventIdx {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("init schema (events index): %w", err)
		}
	}
	return nil
}

// migrateEventsScope upgrades a pre-scope events table (card_id NOT NULL, no
// board_id/scope) to the seam-2a shape: card_id nullable, board_id + scope
// columns, existing rows backfilled to scope='card'. Gated on the presence of
// the scope column, so it's a no-op for fresh DBs (Init already made the new
// table) and idempotent on re-run. (Events seam 2a)
func (s *Store) migrateEventsScope(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(events)`)
	if err != nil {
		return err
	}
	hasScope := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "scope" {
			hasScope = true
		}
	}
	rows.Close()
	if hasScope {
		return nil // fresh new-schema table, or already migrated
	}
	// Rebuild: SQLite can't relax card_id's NOT NULL in place. Copy through a new
	// table, backfilling scope='card', then swap and rebuild indexes.
	migration := []string{
		`ALTER TABLE events RENAME TO events_pre_scope`,
		`CREATE TABLE events (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			card_id  TEXT,
			board_id TEXT,
			scope    TEXT NOT NULL DEFAULT 'card',
			type     TEXT NOT NULL,
			actor    TEXT NOT NULL,
			at       TEXT NOT NULL,
			diff     TEXT
		)`,
		`INSERT INTO events (id, card_id, scope, type, actor, at, diff)
			SELECT id, card_id, 'card', type, actor, at, diff FROM events_pre_scope`,
		`DROP TABLE events_pre_scope`,
		// No index statements here: Init recreates all events indexes after
		// this migration runs (DEBT-32 — single ownership).
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range migration {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("migrate events scope: %w", err)
		}
	}
	return tx.Commit()
}

// Close releases the DB handle.
func (s *Store) Close() error { return s.db.Close() }

// --- Cards ---

// buildCardWhere constructs the WHERE clause + args from a CardQuery,
// compiling q.Filter (the §9 DSL) along the way. Used by both ListCards and
// ClaimAtomic. Returns a validation error on malformed filter DSL.
func buildCardWhere(q core.CardQuery) (string, []any, error) {
	var b strings.Builder
	b.WriteString(" WHERE 1=1")
	args := []any{}
	if q.TypeID != "" {
		b.WriteString(" AND c.type_id = ?")
		args = append(args, q.TypeID)
	}
	if len(q.TypeIDIn) > 0 {
		b.WriteString(" AND c.type_id IN (" + placeholders(len(q.TypeIDIn)) + ")")
		args = append(args, toAny(q.TypeIDIn)...)
	}
	if q.Status != "" {
		b.WriteString(" AND c.status = ?")
		args = append(args, q.Status)
	}
	if len(q.StatusIn) > 0 {
		b.WriteString(" AND c.status IN (" + placeholders(len(q.StatusIn)) + ")")
		args = append(args, toAny(q.StatusIn)...)
	}
	if q.Owner != "" {
		b.WriteString(" AND c.owner = ?")
		args = append(args, q.Owner)
	}
	if q.Unowned {
		b.WriteString(" AND (c.owner IS NULL OR c.owner = '')")
	}
	// Q (FTS title/body) and IDLike (id/short-id) are ORed so a card matches
	// if it hits EITHER path — ANDing them would drop id matches not in FTS.
	if q.Q != "" && q.IDLike != "" {
		pat := "%" + likeEscape(q.IDLike) + "%"
		b.WriteString(" AND (c.id IN (SELECT card_id FROM fts_cards WHERE fts_cards MATCH ?) OR c.id LIKE ? OR substr(c.id, 6, 8) LIKE ?)")
		args = append(args, ftsQuery(q.Q), pat, pat)
	} else if q.Q != "" {
		b.WriteString(" AND c.id IN (SELECT card_id FROM fts_cards WHERE fts_cards MATCH ?)")
		args = append(args, ftsQuery(q.Q))
	} else if q.IDLike != "" {
		pat := "%" + likeEscape(q.IDLike) + "%"
		b.WriteString(" AND (c.id LIKE ? OR substr(c.id, 6, 8) LIKE ?)")
		args = append(args, pat, pat)
	}
	if q.HasLink != "" {
		b.WriteString(" AND EXISTS (SELECT 1 FROM links l WHERE l.source_id = c.id AND l.type_id = ?)")
		args = append(args, q.HasLink)
	}
	if q.LinkTarget != "" {
		b.WriteString(" AND EXISTS (SELECT 1 FROM links l WHERE l.source_id = c.id AND l.target = ?)")
		args = append(args, q.LinkTarget)
	}
	if q.Blocked {
		b.WriteString(` AND EXISTS (SELECT 1 FROM links l JOIN cards t ON l.target = t.id
			WHERE l.source_id = c.id AND l.type_id IN ('blocked-by','depends-on') AND t.status != 'done')`)
	}
	if len(q.Filter) > 0 {
		frag, fargs, err := compileFilter(q.Filter)
		if err != nil {
			return "", nil, err
		}
		if frag != "" {
			b.WriteString(" AND (" + frag + ")")
			args = append(args, fargs...)
		}
	}
	if q.Cursor != "" {
		updatedAt, id, err := core.DecodeCursor(q.Cursor)
		if err == nil {
			b.WriteString(" AND (c.updated_at, c.id) < (?, ?)")
			args = append(args, updatedAt.UTC().Format(time.RFC3339Nano), id)
		}
		// Bad cursors are rejected by the service layer before reaching the store.
	}
	return b.String(), args, nil
}

const cardCols = "id, workspace_id, type_id, schema_version, title, status, owner, tags, fields, version, created_at, updated_at, created_by"

func (s *Store) ListCards(ctx context.Context, q core.CardQuery) (*core.Page[core.Card], error) {
	where, args, err := buildCardWhere(q)
	if err != nil {
		return nil, err
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	query := "SELECT " + cardCols + " FROM cards c" + where + " ORDER BY c.updated_at DESC, c.id DESC LIMIT ?"
	args = append(args, q.Limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list cards: %w", err)
	}
	defer rows.Close()
	cards := []core.Card{}
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, *c)
	}
	next := ""
	if len(cards) > q.Limit {
		last := cards[q.Limit-1]
		next = core.EncodeCursor(last.UpdatedAt, last.ID)
		cards = cards[:q.Limit]
	}
	return &core.Page[core.Card]{Items: cards, NextCursor: next}, nil
}

func (s *Store) GetCard(ctx context.Context, id string) (*core.Card, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+cardCols+" FROM cards c WHERE c.id = ?", id)
	c, err := scanCard(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, core.ErrNotFound
		}
		return nil, fmt.Errorf("get card: %w", err)
	}
	c.Links, _ = s.ListLinks(ctx, id)
	c.Comments, _ = s.ListComments(ctx, id)
	return c, nil
}

// GetCardsByShortID returns cards whose full id equals short OR whose last-8
// hex suffix equals short. Ordered by updated_at DESC, id DESC for stable
// candidates. Used by Service.ResolveCard (1e). (1e)
func (s *Store) GetCardsByShortID(ctx context.Context, short string) ([]core.Card, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+cardCols+" FROM cards c WHERE c.id = ? OR substr(c.id, 6, 8) = ? "+
			"ORDER BY c.updated_at DESC, c.id DESC", short, short)
	if err != nil {
		return nil, fmt.Errorf("get cards by short id: %w", err)
	}
	defer rows.Close()
	var out []core.Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, fmt.Errorf("get cards by short id: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) InsertCard(ctx context.Context, c *core.Card, ev *core.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := execCardInsert(tx, c); err != nil {
		return err
	}
	if err := upsertFTS(tx, c); err != nil {
		return err
	}
	if ev != nil {
		if err := execEventInsert(tx, ev); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateCard(ctx context.Context, c *core.Card, evs []*core.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE cards SET title=?, status=?, owner=?, tags=?, fields=?, schema_version=?, version=?, updated_at=? WHERE id=? AND version=?`,
		c.Title, c.Status, nullableString(c.Owner), tagsJSON(c.Tags), fieldsJSON(c.Fields), c.SchemaVersion, c.Version, c.UpdatedAt.Format(time.RFC3339Nano), c.ID, c.Version-1)
	if err != nil {
		return fmt.Errorf("update card: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return core.VersionConflict(c) // concurrent mutation
	}
	if err := upsertFTS(tx, c); err != nil {
		return err
	}
	for _, ev := range evs {
		if err := execEventInsert(tx, ev); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClaimAtomic picks the oldest unowned card matching q (updated_at ASC, id ASC)
// and atomically sets owner (+optional status) via a CAS on owner IS NULL.
// Returns nil card when nothing matches. SPEC §11 take-next.
func (s *Store) ClaimAtomic(ctx context.Context, q core.CardQuery, owner, status, actor string, now time.Time) (*core.Card, []*core.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	where, args, err := buildCardWhere(q)
	if err != nil {
		return nil, nil, err
	}
	// Pick oldest matching unowned card.
	selectSQL := "SELECT " + cardCols + " FROM cards c" + where + " ORDER BY c.updated_at ASC, c.id ASC LIMIT 1"
	row := tx.QueryRowContext(ctx, selectSQL, args...)
	c, err := scanCard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil // nothing matched
	}
	if err != nil {
		return nil, nil, err
	}

	// CAS the claim: only succeeds if still unowned.
	var setStatus string
	if status != "" && status != c.Status {
		setStatus = status
	}
	var res sql.Result
	nowStr := now.Format(time.RFC3339Nano)
	if setStatus != "" {
		res, err = tx.ExecContext(ctx, `UPDATE cards SET owner=?, status=?, version=version+1, updated_at=? WHERE id=? AND (owner IS NULL OR owner='')`,
			owner, setStatus, nowStr, c.ID)
	} else {
		res, err = tx.ExecContext(ctx, `UPDATE cards SET owner=?, version=version+1, updated_at=? WHERE id=? AND (owner IS NULL OR owner='')`,
			owner, nowStr, c.ID)
	}
	if err != nil {
		return nil, nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Raced: another claimant got it. Return nil so caller retries.
		return nil, nil, nil
	}

	// Reload the claimed card. The row must exist (we just updated it), so any
	// error here — including ErrNoRows — is a real failure.
	row2 := tx.QueryRowContext(ctx, "SELECT "+cardCols+" FROM cards c WHERE c.id = ?", c.ID)
	claimed, err := scanCard(row2)
	if err != nil {
		return nil, nil, err
	}

	// Events: owner_changed (+ status_changed if moved). Use the shared
	// constructors so Version/Diff match every other mutation path.
	evs := []*core.Event{}
	oc := core.OwnerChanged(c.ID, c.Owner, owner)
	oc.Actor, oc.At = actor, now
	evs = append(evs, oc)
	if setStatus != "" {
		sc := core.StatusChanged(c.ID, c.Status, setStatus)
		sc.Actor, sc.At = actor, now
		evs = append(evs, sc)
	}
	// The audit row and FTS upsert are part of the claim: fail the whole
	// transaction rather than commit a mutation with missing side effects.
	for _, ev := range evs {
		if err := execEventInsert(tx, ev); err != nil {
			return nil, nil, err
		}
	}
	if err := upsertFTS(tx, claimed); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	claimed.Links, _ = s.ListLinks(ctx, claimed.ID)
	claimed.Comments, _ = s.ListComments(ctx, claimed.ID)
	return claimed, evs, nil
}

// --- Events ---

const eventCols = "e.id, e.card_id, e.board_id, e.scope, e.type, e.actor, e.at, e.diff"

// buildEventFromWhere assembles the FROM/JOIN/WHERE for an EventQuery. A JOIN
// to cards is added only when an owner or card-type filter is present, so the
// common card-scoped/replay path stays a plain events scan.
func buildEventFromWhere(q core.EventQuery) (string, []any) {
	needCards := q.Owner != "" || len(q.CardTypeIn) > 0
	sb := strings.Builder{}
	sb.WriteString(" FROM events e")
	if needCards {
		sb.WriteString(" JOIN cards c ON c.id = e.card_id")
	}
	sb.WriteString(" WHERE 1=1")
	args := []any{}
	if q.CardID != "" {
		sb.WriteString(" AND e.card_id = ?")
		args = append(args, q.CardID)
	}
	if q.AfterID > 0 {
		sb.WriteString(" AND e.id > ?")
		args = append(args, q.AfterID)
	}
	if q.Actor != "" {
		sb.WriteString(" AND e.actor = ?")
		args = append(args, q.Actor)
	}
	if len(q.Types) > 0 {
		sb.WriteString(" AND e.type IN (" + placeholders(len(q.Types)) + ")")
		args = append(args, toAny(q.Types)...)
	}
	if q.Owner != "" {
		sb.WriteString(" AND c.owner = ?")
		args = append(args, q.Owner)
	}
	if len(q.CardTypeIn) > 0 {
		sb.WriteString(" AND c.type_id IN (" + placeholders(len(q.CardTypeIn)) + ")")
		args = append(args, toAny(q.CardTypeIn)...)
	}
	if q.BoardID != "" { // board-scoped events for this board (seam 2c)
		sb.WriteString(" AND e.board_id = ?")
		args = append(args, q.BoardID)
	}
	if q.Scope != "" {
		sb.WriteString(" AND e.scope = ?")
		args = append(args, q.Scope)
	}
	return sb.String(), args
}

func scanEvents(rows *sql.Rows) ([]core.Event, error) {
	out := []core.Event{}
	for rows.Next() {
		var e core.Event
		var cardID, boardID, scope, at, diff sql.NullString
		if err := rows.Scan(&e.ID, &cardID, &boardID, &scope, &e.Type, &e.Actor, &at, &diff); err != nil {
			return nil, err
		}
		e.CardID = cardID.String
		e.BoardID = boardID.String
		if scope.String != "" && scope.String != "card" {
			e.Scope = scope.String // 'card' is the default; keep it off the wire
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at.String)
		if diff.Valid && diff.String != "" {
			var v any
			_ = json.Unmarshal([]byte(diff.String), &v)
			e.Diff = v
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) List(ctx context.Context, q core.EventQuery) ([]core.Event, error) {
	fromWhere, args := buildEventFromWhere(q)
	query := "SELECT " + eventCols + fromWhere + " ORDER BY e.id ASC LIMIT ?"
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// Append persists standalone events (board / persisted condition events),
// assigning each a monotonic id in one transaction. Card-mutation events are
// persisted via InsertCard/UpdateCard instead (atomic with the card).
func (s *Store) Append(ctx context.Context, evs ...*core.Event) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, ev := range evs {
		if ev == nil {
			continue
		}
		if err := execEventInsert(tx, ev); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Replay streams events with id > fromID in ascending order into fn, stopping
// on the first error fn returns. The primitive behind recovery and projection
// rebuilds; reads in batches so history of any size is bounded in memory.
func (s *Store) Replay(ctx context.Context, fromID int64, fn func(*core.Event) error) error {
	const batch = 500
	after := fromID
	for {
		evs, err := s.List(ctx, core.EventQuery{AfterID: after, Limit: batch})
		if err != nil {
			return err
		}
		for i := range evs {
			if err := fn(&evs[i]); err != nil {
				return err
			}
			after = evs[i].ID
		}
		if len(evs) < batch {
			return nil
		}
	}
}

// ListEventsPage is the cursor-paged catch-up feed. It fetches Limit+1 rows to
// detect a further page, trims to Limit, and sets NextCursor to the last event
// id (the client passes it back as cursor=/since= to continue).
func (s *Store) Page(ctx context.Context, q core.EventQuery) (*core.Page[core.Event], error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	fromWhere, args := buildEventFromWhere(q)
	query := "SELECT " + eventCols + fromWhere + " ORDER BY e.id ASC LIMIT ?"
	args = append(args, q.Limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evs, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(evs) > q.Limit {
		evs = evs[:q.Limit]
		next = strconv.FormatInt(evs[len(evs)-1].ID, 10)
	}
	return &core.Page[core.Event]{Items: evs, NextCursor: next}, nil
}

// InsertEventRaw appends a single event verbatim (preserving card_id, board_id,
// scope, type, actor, at, and diff). The events table assigns a fresh
// autoincrement id, so import preserves chronological order without forcing
// original ids. Used by the import command to restore the audit log.
func (s *Store) InsertEventRaw(ctx context.Context, ev *core.Event) error {
	diffB, _ := json.Marshal(ev.Diff)
	scope := ev.Scope
	if scope == "" {
		scope = "card"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(card_id, board_id, scope, type, actor, at, diff) VALUES(?,?,?,?,?,?,?)`,
		nullableString(ev.CardID), nullableString(ev.BoardID), scope, string(ev.Type), ev.Actor, ev.At.Format(time.RFC3339Nano), string(diffB))
	return err
}

// --- Links ---

func (s *Store) ListLinks(ctx context.Context, cardID string) ([]core.Link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT type_id, target, note, created_by, created_at FROM links WHERE source_id = ? ORDER BY id`, cardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Link{}
	for rows.Next() {
		var l core.Link
		var note, at sql.NullString
		if err := rows.Scan(&l.TypeID, &l.Target, &note, &l.CreatedBy, &at); err != nil {
			return nil, err
		}
		l.Note = note.String
		if at.Valid {
			l.CreatedAt, _ = time.Parse(time.RFC3339Nano, at.String)
		}
		out = append(out, l)
	}
	return out, nil
}

func (s *Store) InsertLink(ctx context.Context, cardID string, l core.Link) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO links(source_id, type_id, target, note, created_by, created_at) VALUES(?,?,?,?,?,?)`,
		cardID, l.TypeID, l.Target, nullableString(l.Note), l.CreatedBy, l.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) DeleteLink(ctx context.Context, cardID, typeID, target string) (core.Link, error) {
	_, err := s.db.ExecContext(ctx, `DELETE FROM links WHERE source_id=? AND type_id=? AND target=?`, cardID, typeID, target)
	return core.Link{TypeID: typeID, Target: target}, err
}

// AllLinks returns the whole link graph as source→target edges (one query).
func (s *Store) AllLinks(ctx context.Context) ([]core.LinkEdge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_id, type_id, target FROM links ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.LinkEdge{}
	for rows.Next() {
		var e core.LinkEdge
		if err := rows.Scan(&e.Source, &e.TypeID, &e.Target); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Comments ---

func (s *Store) ListComments(ctx context.Context, cardID string) ([]core.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, author, body, created_at, edited_at FROM comments WHERE card_id = ? ORDER BY created_at`, cardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Comment{}
	for rows.Next() {
		var c core.Comment
		var created, edited sql.NullString
		if err := rows.Scan(&c.ID, &c.Author, &c.Body, &created, &edited); err != nil {
			return nil, err
		}
		if created.Valid {
			c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
		}
		if edited.Valid {
			c.EditedAt, _ = time.Parse(time.RFC3339Nano, edited.String)
		}
		out = append(out, c)
	}
	return out, nil
}

// CommentCounts returns card_id→comment count for every card (one query).
func (s *Store) CommentCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT card_id, COUNT(*) FROM comments GROUP BY card_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Store) InsertComment(ctx context.Context, cardID string, c core.Comment) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO comments(id, card_id, author, body, created_at, edited_at) VALUES(?,?,?,?,?,?)`,
		c.ID, cardID, c.Author, c.Body, c.CreatedAt.Format(time.RFC3339Nano), nil)
	return err
}

func (s *Store) UpdateComment(ctx context.Context, cardID, commentID, body string, editedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE comments SET body=?, edited_at=? WHERE id=? AND card_id=?`,
		body, editedAt.Format(time.RFC3339Nano), commentID, cardID)
	return err
}

// --- Idempotency ---

func (s *Store) GetIdempotency(ctx context.Context, key, actor string) (*core.IdempotencyRecord, error) {
	var rec core.IdempotencyRecord
	var body string
	var created sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT key, actor, status, body, created_at FROM idempotency_keys WHERE key=? AND actor=?`, key, actor).
		Scan(&rec.Key, &rec.Actor, &rec.Status, &body, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec.Body = []byte(body)
	return &rec, nil
}

func (s *Store) PutIdempotency(ctx context.Context, rec core.IdempotencyRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO idempotency_keys(key, actor, status, body, created_at) VALUES(?,?,?,?,?)`,
		rec.Key, rec.Actor, rec.Status, string(rec.Body), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// --- Users ---

func (s *Store) ListUsers(ctx context.Context) ([]core.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, display_name, kind, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []core.User{}
	for rows.Next() {
		var u core.User
		var dn, kind, created sql.NullString
		if err := rows.Scan(&u.ID, &dn, &kind, &created); err != nil {
			return nil, err
		}
		u.DisplayName = dn.String
		u.Kind = kind.String
		if created.Valid {
			t, _ := time.Parse(time.RFC3339Nano, created.String)
			u.CreatedAt = t
		}
		users = append(users, u)
	}
	return users, nil
}

func (s *Store) InsertUser(ctx context.Context, u core.User) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO users(id, display_name, kind, created_at) VALUES(?,?,?,?)`,
		u.ID, nullableString(u.DisplayName), u.Kind, u.CreatedAt.Format(time.RFC3339Nano))
	return err
}

// --- helpers ---

type scanner interface {
	Scan(dest ...any) error
}

func scanCard(r scanner) (*core.Card, error) {
	var c core.Card
	var owner, tags sql.NullString
	var fieldsB string
	var created, updated string
	err := r.Scan(&c.ID, &c.WorkspaceID, &c.TypeID, &c.SchemaVersion, &c.Title, &c.Status, &owner, &tags, &fieldsB, &c.Version, &created, &updated, &c.CreatedBy)
	if err != nil {
		return nil, err
	}
	c.Owner = owner.String
	if tags.Valid && tags.String != "" && tags.String != "null" {
		_ = json.Unmarshal([]byte(tags.String), &c.Tags)
	}
	if fieldsB != "" {
		_ = json.Unmarshal([]byte(fieldsB), &c.Fields)
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &c, nil
}

func execCardInsert(tx *sql.Tx, c *core.Card) error {
	_, err := tx.Exec(`INSERT INTO cards(id, workspace_id, type_id, schema_version, title, status, owner, tags, fields, version, created_at, updated_at, created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.WorkspaceID, c.TypeID, c.SchemaVersion, c.Title, c.Status, nullableString(c.Owner), tagsJSON(c.Tags), fieldsJSON(c.Fields), c.Version, c.CreatedAt.Format(time.RFC3339Nano), c.UpdatedAt.Format(time.RFC3339Nano), c.CreatedBy)
	return err
}

func execEventInsert(tx *sql.Tx, ev *core.Event) error {
	diffB, _ := json.Marshal(ev.Diff)
	scope := ev.Scope
	if scope == "" {
		scope = "card"
	}
	res, err := tx.Exec(`INSERT INTO events(card_id, board_id, scope, type, actor, at, diff) VALUES(?,?,?,?,?,?,?)`,
		nullableString(ev.CardID), nullableString(ev.BoardID), scope, string(ev.Type), ev.Actor, ev.At.Format(time.RFC3339Nano), string(diffB))
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil {
		ev.ID = id
	}
	return nil
}

// upsertFTS maintains the FTS5 index for a card. The indexed body is the title
// plus searchable field values (best-effort).
func upsertFTS(tx *sql.Tx, c *core.Card) error {
	if _, err := tx.Exec(`DELETE FROM fts_cards WHERE card_id = ?`, c.ID); err != nil {
		return err
	}
	body := c.Title
	if m, ok := c.Fields.(map[string]any); ok {
		for _, v := range m {
			body += " " + fmt.Sprint(v)
		}
	}
	_, err := tx.Exec(`INSERT INTO fts_cards(card_id, title, body) VALUES(?,?,?)`, c.ID, c.Title, body)
	return err
}

// ftsQuery sanitizes a free-text query into an FTS5 MATCH expression.
func ftsQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	// Quote each token to avoid FTS5 operator interpretation.
	parts := strings.Fields(q)
	for i, p := range parts {
		parts[i] = "\"" + strings.ReplaceAll(p, "\"", "") + "\""
	}
	return strings.Join(parts, " ")
}

// likeEscape escapes % and _ so a user-typed id-like pattern can't inject
// LIKE wildcards. (1e/1d)
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

func fieldsJSON(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func tagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}


func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func toAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
