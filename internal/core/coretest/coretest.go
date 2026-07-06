// Package coretest holds shared test fixtures for exercising the core
// Service from any surface package (core, httpapi, mcp, cli, cmd). It has no
// non-test consumers.
//
// The colliding-card fixture exists because card ids are random
// (core.newCardID), so the ambiguous branch of short-id resolution cannot be
// forced through the public API. Inserting crafted ids at the store layer is
// the deliberate seam — deterministic, per-test (the store itself is
// in-memory or t.TempDir-scoped, so teardown is automatic), and requires no
// production code to know about tests.
package coretest

import (
	"context"
	"testing"
	"time"

	"github.com/somebox/cards/internal/core"
)

// CardInserter is the one store capability the fixtures need. *sqlite.Store
// and any core.Store implementation satisfy it.
type CardInserter interface {
	InsertCard(ctx context.Context, c *core.Card, ev *core.Event) error
}

// SeedCard inserts one minimal valid card with the given full id — crafted
// ids let tests control the short id, which is the FIRST 8 chars of the hex
// part (see CardID). The card is version 1, in "todo", with the supplied
// fields (nil → empty map).
func SeedCard(tb testing.TB, st CardInserter, wsID, typeID, id string, fields map[string]any) *core.Card {
	tb.Helper()
	if fields == nil {
		fields = map[string]any{}
	}
	now := time.Now().UTC()
	c := &core.Card{
		ID: id, WorkspaceID: wsID, TypeID: typeID, SchemaVersion: 1,
		Title: "Crafted " + id, Status: "todo", Fields: fields,
		Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: "coretest",
	}
	ev := &core.Event{CardID: id, Type: core.EventCardCreated, Actor: "coretest", At: now}
	if err := st.InsertCard(context.Background(), c, ev); err != nil {
		tb.Fatalf("coretest: insert crafted card %s: %v", id, err)
	}
	return c
}

// CardID builds a full crafted card id whose short id (the FIRST 8 chars of
// the hex part — sqlite matches substr(id, 6, 8)) is `short`. filler pads the
// remaining 24 chars deterministically.
func CardID(short, filler string) string {
	if len(filler) == 0 {
		filler = "a"
	}
	for len(filler) < 24 {
		filler += filler
	}
	return "card_" + short + filler[:24]
}

// SeedCollidingCards inserts two cards whose full ids share the same leading
// 8-char short id, making `short` deterministically ambiguous for
// ResolveCard. short must be exactly 8 chars (id charset); the first
// returned card is the older one. Both ids are returned so tests can assert
// the candidate list.
func SeedCollidingCards(tb testing.TB, st CardInserter, wsID, typeID, short string) (idA, idB string) {
	tb.Helper()
	if len(short) != 8 {
		tb.Fatalf("coretest: short must be 8 chars, got %q", short)
	}
	idA = CardID(short, "a")
	idB = CardID(short, "b")
	SeedCard(tb, st, wsID, typeID, idA, nil)
	SeedCard(tb, st, wsID, typeID, idB, nil)
	return idA, idB
}
