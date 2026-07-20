#!/usr/bin/env node
// review-bot.mjs — a runnable cards-extension (kind: service) demonstrating
// philosophy §6 "extensions over plugins": an ordinary API client, supervised
// by `cards serve --run-extensions`, talking to the core only over the
// versioned HTTP + SSE contracts. It is the worker loop from
// docs/events/integration.md (§ Quickstart, "3. The worker loop", line 37)
// hardened into a long-running process:
//
//   subscribe /v1/events/stream?board_id=…&types=status_changed
//     → event with diff.after === "review"
//     → POST /v1/cards/take-next    (claim oldest unowned card in review)
//     → POST /v1/cards/:id/comments (as actor CARDS_USER)
//
// The demo workspace also fires the `review-notify` HOOK on the same
// transition — one event, two extension kinds: the hook receives bus JSON on
// stdin; this service dials the SSE stream itself. Node stdlib only — zero
// npm dependencies.
//
// Env (set by the supervisor): CARDS_URL (…/v1), CARDS_WORKSPACE, CARDS_USER.
// Optional overrides: REVIEW_BOT_BOARD (default "engineering"),
// REVIEW_BOT_TO_STATUS (default "review").

import { randomUUID } from "node:crypto";
import { setTimeout as sleep } from "node:timers/promises";

const BASE = (process.env.CARDS_URL || "http://127.0.0.1:8787/v1").replace(/\/+$/, "");
const API = BASE.endsWith("/v1") ? BASE : `${BASE}/v1`;
const ACTOR = process.env.CARDS_USER || "review-bot";
const BOARD = process.env.REVIEW_BOT_BOARD || "engineering";
const TO_STATUS = process.env.REVIEW_BOT_TO_STATUS || "review";

// Structured log: one JSON object per line with the `event` key first, so the
// supervisor-stability test has a stable {"event":"subscribed",…} grep target.
function log(event, fields = {}) {
  console.log(JSON.stringify({ event, ...fields, at: new Date().toISOString() }));
}

// Survives reconnects; sent as the SSE Last-Event-ID header so a dropped
// stream replays the gap instead of losing it (contract: sse.go replay).
let lastEventId = null;

async function api(method, path, body, idemKey) {
  const res = await fetch(`${API}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      "X-Work-Cards-Actor": ACTOR,
      "Idempotency-Key": idemKey || `review-bot-${randomUUID()}`,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path}: HTTP ${res.status} ${text.slice(0, 200)}`);
  return text ? JSON.parse(text) : {};
}

// React to one status_changed→review event. Idempotency keys are
// deterministic per triggering event id, so an event replayed after a
// reconnect cannot double-claim or double-comment — the server replays the
// first recorded response instead.
async function onReviewEvent(evt) {
  // Claim the oldest unowned card sitting in the target column. take-next's
  // own `status` field would MOVE the claimed card into that column, so the
  // lane scope goes through the filter DSL instead.
  const { card } = await api("POST", "/cards/take-next", {
    board_id: BOARD,
    filter: { status: { $eq: TO_STATUS } }, // filter DSL takes operator objects
  }, `review-bot-claim-${evt.id}`);
  if (!card) {
    log("no_claim", { trigger_event: evt.id, reason: `no unowned card in ${TO_STATUS}` });
    return;
  }
  log("claimed", { card: card.id, trigger_event: evt.id });
  await api("POST", `/cards/${card.id}/comments`, {
    body: `review-bot: claimed for review (trigger event #${evt.id}). ` +
      "Automated first pass by the review-bot service extension; a human reviewer still owns the verdict.",
  }, `review-bot-comment-${evt.id}`);
  log("commented", { card: card.id, trigger_event: evt.id });
}

// Serialize event handling: SSE frames keep arriving while a claim is in
// flight, so chain the work instead of interleaving take-next calls.
let pending = Promise.resolve();
function enqueue(evt) {
  pending = pending.then(() => onReviewEvent(evt)).catch((err) => {
    log("handle_error", { trigger_event: evt.id, error: String((err && err.message) || err) });
  });
}

// Hand-rolled SSE reader: frames are "field: value" lines terminated by a
// blank line; ":" lines are keepalive/dropped notices; data lines join "\n".
async function consumeStream(res) {
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let dataLines = [];
  const dispatch = () => {
    const data = dataLines.join("\n");
    dataLines = [];
    if (data === "") return;
    let evt;
    try {
      evt = JSON.parse(data);
    } catch {
      log("bad_frame", { data: data.slice(0, 120) });
      return;
    }
    if (typeof evt.id === "number") lastEventId = evt.id;
    if (evt.type === "status_changed" && evt.diff && evt.diff.after === TO_STATUS) enqueue(evt);
  };
  for (;;) {
    const { done, value } = await reader.read();
    if (done) throw new Error("stream closed by server");
    buf += decoder.decode(value, { stream: true });
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl).replace(/\r$/, "");
      buf = buf.slice(nl + 1);
      if (line === "") { dispatch(); continue; }
      if (line.startsWith(":")) continue; // keepalive / ": dropped, reconnect"
      if (line.startsWith("data:")) dataLines.push(line.slice(5).replace(/^ /, ""));
      // `id:` and `event:` lines are redundant — the JSON payload carries both.
    }
  }
}

async function connect() {
  const url = `${API}/events/stream?board_id=${encodeURIComponent(BOARD)}&types=status_changed`;
  const headers = {};
  if (lastEventId !== null) headers["Last-Event-ID"] = String(lastEventId);
  const res = await fetch(url, { headers });
  if (!res.ok || !res.body) throw new Error(`subscribe: HTTP ${res.status}`);
  log("subscribed", { lastEventId, pid: process.pid });
  await consumeStream(res);
}

// Reconnect with bounded backoff: the server may be down mid-restart, so keep
// dialing — replay via Last-Event-ID closes the gap once it accepts again.
async function main() {
  log("starting", { api: API, actor: ACTOR, board: BOARD, to_status: TO_STATUS });
  let failures = 0;
  for (;;) {
    try {
      await connect();
    } catch (err) {
      failures += 1;
      log("disconnected", { error: String((err && err.message) || err), failures });
    }
    await sleep(Math.min(500 * 2 ** Math.min(failures, 5), 15000));
  }
}

main().catch((err) => {
  log("fatal", { error: String((err && err.message) || err) });
  process.exit(1); // non-zero: the supervisor restarts per restart_policy
});
