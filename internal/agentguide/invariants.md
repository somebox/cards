- **Read the workspace first.** Call `workspace` at session start; its card types,
  required fields, columns, transitions, WIP limits and users are the contract.
  Never carry a status or field name over from another project.
- **Writes carry the current `version`.** A `version_conflict` response includes
  the current card — re-read from it and retry. Never blind-retry. Every write
  returns the updated card, so take the next version from that response instead
  of a separate read.
- **Validation errors are actionable.** They name the failing field and carry
  `valid_options`. Correct the value; do not work around the schema.
- **Record evidence as work lands.** Comments carry the narrative — what was
  done, verified, decided, or surprising. Repeating fields carry structured
  records: commits, sources, measurements. Reference commit SHAs and PR URLs,
  and attach a screenshot when a reviewer should see the change, not run it.
- **Move status honestly**, only when the work is really there. Where a review
  column exists, implementation ends there, and the card must make verification
  cheap: acceptance, a verify command, the commits, the evidence.
- **Discoveries become linked cards**, never silent scope creep. File a follow-up
  for anything blocking progress or release; leave speculative ideas for triage.
- **Never invent a local-time timestamp**; card dates are RFC3339 UTC.
- **Who owns the card:** by default you record on your own card. An orchestrator
  may instead own all card bookkeeping — if so it says that in your instructions,
  and you then do the repo work and touch no card.
- **At session end**, make card status reflect reality, then export
  `--state-only` to the project's established snapshot path. Never `import` over
  a non-empty database.
