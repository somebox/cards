export const meta = {
  name: 'sprint-plan',
  description: 'Survey the project, align to vision, propose + investigate next steps, then produce a reviewed, ready-for-dev sprint plan.',
  whenToUse: 'Start-of-sprint planning: turn "where do we stand?" into a refined, reviewed plan of 3-5 related topics.',
  phases: [
    { title: 'Survey',      detail: 'parallel discovery: git, backlog, tests/experiments, docs/plans, tech-debt', model: 'sonnet' },
    { title: 'Align',       detail: 'mission / vision / roadmap alignment (product thinking)', model: 'opus' },
    { title: 'Strategy',    detail: 'propose N candidate next steps (strategic review)', model: 'opus' },
    { title: 'Investigate', detail: 'one architecture analysis per candidate (deps, domain model, debt)', model: 'sonnet' },
    { title: 'Issues',      detail: 'cross-cutting debt / process / domain-model evolution (strategic analysis)', model: 'fable' },
    { title: 'Draft',       detail: 'draft the grouped sprint plan (expert dev)', model: 'opus' },
    { title: 'Review',      detail: 'three lenses in parallel: design/UX, architecture/data, reliability/maintainability', model: 'opus' },
    { title: 'Revise',      detail: 'incorporate reviewer feedback pragmatically (high value / effort) — runs after the lenses', model: 'fable' },
    { title: 'Refine',      detail: 'final mechanical polish: obvious mistakes, ordering, naming, precise outcomes', model: 'opus' },
  ],
}

// ---- args (all optional) — parameterize per run ---------------------------
const focus      = (args && args.focus) || ''          // optional steer, e.g. "prioritise the integration surface"
const candidateN = (args && args.candidates) || 4      // how many next-step candidates to propose (3-5)
const sprintSize = (args && args.sprintSize) || '3-5 related topics'
const repoHint   = (args && args.repo) || 'this repository'

// ---- per-step model + effort policy ---------------------------------------
// Each agent() can override the model (sonnet | opus | haiku | fable, or a full
// model id) and reasoning effort (low | medium | high | xhigh | max). Omitting
// either inherits the session default. Override per run via args.models /
// args.effort, e.g. Workflow({ name:'sprint-plan', args:{ models:{ draft:'opus', survey:'haiku' } } }).
// Default policy: cheap+fast for broad parallel reading, top-tier for the
// reasoning-heavy synthesis / strategy / review / refine steps.
const M = Object.assign({
  survey:      'sonnet', // 5 parallel discovery agents — lots of reading, cost-sensitive
  synthesize:  'opus',   // merge + resolve disagreements
  align:       'opus',   // product/vision judgment
  strategy:    'opus',   // propose the candidate set
  investigate: 'sonnet', // grounded code reading, one per candidate (parallel)
  issues:      'fable',  // HARDEST step: cross-cutting strategic analysis + novel
                         // domain-model evolution — most reasoning / out-of-box thinking
  draft:       'opus',   // author the plan
  review:      'opus',   // adversarial review panel (3 lenses)
  revise:      'fable',  // incorporate reviewer feedback pragmatically — high value / effort judgment
  refine:      'opus',   // final mechanical polish (obvious mistakes, ordering, naming)
}, (args && args.models) || {})

const E = Object.assign({
  survey:      'low',    // discovery is breadth, not deep reasoning
  investigate: 'medium',
  issues:      'high',   // pair Fable with high effort on the hardest step
  review:      'high',   // reviewers should push hard
  revise:      'high',   // Fable making the pragmatic value/effort tradeoffs
  // others omitted → inherit the session effort
}, (args && args.effort) || {})

// ---- shared schema fragments ----------------------------------------------
const strList = { type: 'array', items: { type: 'string' } }
const findings = {
  type: 'array',
  items: {
    type: 'object', additionalProperties: false, required: ['point', 'evidence'],
    properties: {
      point: { type: 'string', description: 'one specific finding' },
      evidence: { type: 'string', description: 'file/commit/card/test that grounds it' },
      momentum: { type: 'string', enum: ['rising', 'steady', 'stalled', 'abandoned', 'n/a'] },
    },
  },
}

// A candidate next-step and its later investigation share a stable slug so the
// pipeline can label work without threading context through every stage.
const CANDIDATES_SCHEMA = {
  type: 'object', additionalProperties: false, required: ['candidates'],
  properties: {
    candidates: {
      type: 'array', minItems: 3, maxItems: 6,
      items: {
        type: 'object', additionalProperties: false,
        required: ['slug', 'title', 'rationale', 'userValue'],
        properties: {
          slug: { type: 'string', description: 'kebab-case id' },
          title: { type: 'string' },
          rationale: { type: 'string' },
          userValue: { type: 'string', description: 'why it matters to the user / product' },
          alignment: { type: 'string', description: 'how it serves the vision' },
        },
      },
    },
  },
}

const PLAN_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['title', 'theme', 'phases', 'outOfScope'],
  properties: {
    title: { type: 'string' },
    theme: { type: 'string', description: 'the through-line: what foundation + user value this sprint builds' },
    phases: {
      type: 'array', minItems: 3, maxItems: 5,
      items: {
        type: 'object', additionalProperties: false,
        required: ['name', 'goal', 'steps', 'demo', 'exitCriteria'],
        properties: {
          name: { type: 'string' },
          goal: { type: 'string' },
          dependsOn: strList,
          steps: {
            type: 'array',
            items: {
              type: 'object', additionalProperties: false, required: ['do', 'outcome'],
              properties: {
                do: { type: 'string', description: 'a concrete, testable action' },
                outcome: { type: 'string', description: 'observable result / acceptance check' },
                files: { type: 'string', description: 'likely files/areas touched' },
              },
            },
          },
          demo: { type: 'string', description: 'something to try/see at the end of the phase' },
          exitCriteria: strList,
        },
      },
    },
    risks: strList,
    outOfScope: strList,
  },
}

// ===========================================================================
// Step 1 — Review recent work & summarize (parallel discovery -> synthesis)
// ===========================================================================
phase('Survey')
const FACETS = [
  { key: 'git',       what: 'recent git history: `git log --oneline -40`, what shipped, the arc of the last few milestones, and what each commit cluster was really about' },
  { key: 'backlog',   what: 'the planning board / backlog (query the live cards server if running, else definitions + docs): top backlog + todo items, what is queued vs stale' },
  { key: 'tests',     what: 'test suite + experiments + dogfood state: coverage gaps, skipped/flaky tests, TODO/FIXME markers, cleanup tasks' },
  { key: 'docs',      what: 'docs/ plans, ROADMAP.md, NOTES.md, and any *-plan.md files: stated plans, open TODOs, [proposed]/[deferred] markers' },
  { key: 'tech-debt', what: 'tech debt: debt ledgers, code review notes, known limitations, and drift between docs and code' },
]
log(`Survey: dispatching ${FACETS.length} discovery agents in parallel — ${FACETS.map(f => f.key).join(', ')} (${M.survey}, effort ${E.survey}).`)
const surveys = await parallel(FACETS.map(f => () =>
  agent(
    `You are surveying ${repoHint} to establish where the project stands. Investigate ONLY this facet: ${f.what}.\n` +
    `Read real files/commits/cards — cite specifics. Rate momentum per finding (rising/steady/stalled/abandoned).` +
    (focus ? `\nThe human is especially interested in: ${focus}.` : ''),
    { label: `survey:${f.key}`, phase: 'Survey', model: M.survey, effort: E.survey, schema: {
        type: 'object', additionalProperties: false, required: ['facet', 'headline', 'findings'],
        properties: { facet: { type: 'string' }, headline: { type: 'string' }, findings } } }
  ).then(r => ({ ...r, facet: f.key }))
))
const state = await agent(
  `Synthesize these facet surveys into one grounded "state of the project" for ${repoHint}. ` +
  `De-duplicate, resolve disagreements, and separate: what has momentum, what's been left behind, and the next major features that are within reach.\n\n` +
  `FACET SURVEYS:\n${JSON.stringify(surveys.filter(Boolean), null, 1)}`,
  { label: 'synthesize-state', phase: 'Survey', model: M.synthesize, schema: {
      type: 'object', additionalProperties: false,
      required: ['summary', 'momentum', 'leftBehind', 'candidateFeatures'],
      properties: {
        summary: { type: 'string' },
        momentum: strList,
        leftBehind: strList,
        candidateFeatures: strList,
        openDebt: strList,
      } } }
)

// ===========================================================================
// Step 2 — Vision/roadmap alignment (product thinking)
// ===========================================================================
phase('Align')
log(`Survey synthesized: ${(state.momentum || []).length} momentum items, ${(state.leftBehind || []).length} left-behind, ${(state.candidateFeatures || []).length} candidate features. Assessing vision alignment (${M.align})…`)
const alignment = await agent(
  `Read the project's mission/vision/philosophy/roadmap docs (e.g. docs/concepts/PHILOSOPHY.md, docs/ROADMAP.md, README). ` +
  `Given the state of the project below, assess how well the momentum + candidate features align with the stated goals, ` +
  `what they mean for the USER, and where the project risks drifting from its vision.\n\nSTATE:\n${JSON.stringify(state, null, 1)}`,
  { label: 'vision-alignment', phase: 'Align', model: M.align, schema: {
      type: 'object', additionalProperties: false, required: ['visionSummary', 'alignment', 'tensions'],
      properties: {
        visionSummary: { type: 'string' },
        alignment: { type: 'string', description: 'how the candidate direction serves the vision + the user' },
        tensions: strList,
        northStar: { type: 'string', description: 'the single most vision-advancing direction' },
      } } }
)

// ===========================================================================
// Step 3 — Strategic review: propose candidate next steps
// ===========================================================================
phase('Strategy')
log(`North star: ${alignment.northStar || 'n/a'}. Proposing ${candidateN} candidate next steps (${M.strategy})…`)
const candidates = await agent(
  `As a strategic reviewer, propose the ${candidateN} strongest candidate next steps for ${repoHint}. ` +
  `Ground them in the state + alignment. Each must state its user/product value and how it serves the vision. ` +
  `Prefer steps that build a technical foundation AND deliver user value.\n\n` +
  `STATE:\n${JSON.stringify(state, null, 1)}\n\nALIGNMENT:\n${JSON.stringify(alignment, null, 1)}`,
  { label: 'candidates', phase: 'Strategy', model: M.strategy, schema: CANDIDATES_SCHEMA }
)

// ===========================================================================
// Step 4 — Investigate each candidate (architecture analysis, one per agent)
// ===========================================================================
phase('Investigate')
log(`Candidates: ${(candidates.candidates || []).map(c => c.title).join(' · ')}. Investigating ${(candidates.candidates || []).length} in parallel (${M.investigate}, effort ${E.investigate})…`)
const investigations = await parallel((candidates.candidates || []).map(c => () =>
  agent(
    `Do a deep architecture analysis of this candidate next step for ${repoHint}:\n${JSON.stringify(c, null, 1)}\n\n` +
    `Cover: (a) architecture dependencies + which components/interfaces it touches, (b) domain-model impact (new/changed concepts), ` +
    `(c) effect on tech debt (pays down / adds), (d) how it constrains or enables future directions, (e) rough effort + main risks. ` +
    `Read real code to ground each claim.`,
    { label: `investigate:${c.slug}`, phase: 'Investigate', model: M.investigate, effort: E.investigate, schema: {
        type: 'object', additionalProperties: false,
        required: ['slug', 'dependencies', 'domainImpact', 'debtImpact', 'futureFit', 'effort', 'risks'],
        properties: {
          slug: { type: 'string' },
          dependencies: strList,
          domainImpact: { type: 'string' },
          debtImpact: { type: 'string', description: 'pays down or adds debt, specifically' },
          futureFit: { type: 'string', description: 'enables or constrains future directions' },
          effort: { type: 'string', enum: ['S', 'M', 'L', 'XL'] },
          risks: strList,
        } } }
    ).then(r => ({ ...r, slug: c.slug }))
))

// ===========================================================================
// Step 5 — Cross-cutting strategic analysis (what to tackle first)
// ===========================================================================
phase('Issues')
log(`Investigations in. HARDEST STEP → cross-cutting strategic analysis + domain-model evolution on ${M.issues} (effort ${E.issues})…`)
const issues = await agent(
  `Given the candidate investigations, step back: what broader architecture / code / data / process issues are worth tackling FIRST? ` +
  `Which tech debts should be cleaned up to unblock the rest? What new concepts or domain-model evolution should be considered? ` +
  `Identify foundations that make several candidates cheaper.\n\n` +
  `INVESTIGATIONS:\n${JSON.stringify(investigations.filter(Boolean), null, 1)}\n\nOPEN DEBT:\n${JSON.stringify(state.openDebt || [], null, 1)}`,
  { label: 'cross-cutting-issues', phase: 'Issues', model: M.issues, effort: E.issues, schema: {
      type: 'object', additionalProperties: false, required: ['foundationsFirst', 'debtToClear', 'domainEvolution', 'sequencing'],
      properties: {
        foundationsFirst: strList,
        debtToClear: strList,
        domainEvolution: strList,
        sequencing: { type: 'string', description: 'what unblocks what' },
      } } }
)

// ===========================================================================
// Step 6 — Draft the sprint plan (expert dev)
// ===========================================================================
phase('Draft')
log(`Foundations first: ${(issues.foundationsFirst || []).slice(0, 2).join(' · ') || 'n/a'}. Drafting the sprint plan (${M.draft})…`)
let plan = await agent(
  `As an expert developer, draft a sprint plan for ${repoHint}: a curated set of ${sprintSize} that BUILD ON EACH OTHER — ` +
  `foundation first, then user value on top — advancing the project's vision. Group into 3-5 phases with test/commit cycles and ` +
  `something to demo per phase. Prefer the foundations flagged as unblocking. Be concrete.\n\n` +
  `STATE:\n${JSON.stringify(state, null, 1)}\n\nALIGNMENT:\n${JSON.stringify(alignment, null, 1)}\n\n` +
  `CANDIDATES:\n${JSON.stringify(candidates, null, 1)}\n\nINVESTIGATIONS:\n${JSON.stringify(investigations.filter(Boolean), null, 1)}\n\n` +
  `CROSS-CUTTING:\n${JSON.stringify(issues, null, 1)}`,
  { label: 'draft-plan', phase: 'Draft', model: M.draft, schema: PLAN_SCHEMA }
)

// ===========================================================================
// Step 7 — Review panel: three parallel lenses (barrier), then a pragmatic
// revise that consumes all three (its own Revise phase, below).
// ===========================================================================
phase('Review')
log(`Draft ready: "${plan.title}" — ${(plan.phases || []).length} phases. Review panel: ${LENSES.length} lenses (${LENSES.map(l => l.key).join(', ')}) on ${M.review}@${E.review}…`)
const LENSES = [
  { key: 'design-ux',      brief: 'design & user experience: does each phase deliver visible user value, in a sensible order, with demos that prove it?' },
  { key: 'arch-data',      brief: 'architecture & data model: are the seams right, migrations/compat handled, domain concepts coherent, no hidden coupling?' },
  { key: 'reliability',    brief: 'reliability & maintainability: testability, failure modes, operational risk, and whether it adds or pays down debt' },
]
const reviews = await parallel(LENSES.map(l => () =>
  agent(
    `Review this DRAFT sprint plan through the lens of ${l.brief}\n\nPLAN:\n${JSON.stringify(plan, null, 1)}\n\n` +
    `Return concrete, actionable feedback — the key topics that must change, not praise.`,
    { label: `review:${l.key}`, phase: 'Review', model: M.review, effort: E.review, schema: {
        type: 'object', additionalProperties: false, required: ['lens', 'mustAddress', 'suggestions'],
        properties: {
          lens: { type: 'string' },
          mustAddress: { type: 'array', items: { type: 'object', additionalProperties: false, required: ['issue', 'fix'],
            properties: { issue: { type: 'string' }, fix: { type: 'string' }, severity: { type: 'string', enum: ['blocker', 'major', 'minor'] } } } },
          suggestions: strList,
        } } }
    ).then(r => ({ ...r, lens: l.key }))
))
// ---------------------------------------------------------------------------
// Revise runs AFTER the three lenses (it awaits their barrier and takes them as
// input) — its own phase so that ordering is explicit in the progress view.
// This is the judgment-heavy step: incorporate the feedback pragmatically.
// ---------------------------------------------------------------------------
phase('Revise')
log(`Reviewers returned ${reviews.filter(Boolean).reduce((n, rv) => n + ((rv.mustAddress || []).length), 0)} must-address items. Revising pragmatically on ${M.revise} (effort ${E.revise})…`)
plan = await agent(
  `Revise the plan to incorporate the reviewers' feedback. Address every blocker and major item; you may decline a suggestion but say why.\n\n` +
  `Be PRAGMATIC: optimize for high value gained per unit of effort. Prefer the cheapest change that captures most of a reviewer's point; ` +
  `cut or defer gold-plating; don't add scope, ceremony, or phases that don't earn their keep. If the reviews pull in conflicting directions, ` +
  `choose the option a shipping team would actually take. Keep the schema.\n\n` +
  `PLAN:\n${JSON.stringify(plan, null, 1)}\n\nREVIEWS:\n${JSON.stringify(reviews.filter(Boolean), null, 1)}`,
  { label: 'revise-plan', phase: 'Revise', model: M.revise, effort: E.revise, schema: PLAN_SCHEMA }
)

// ===========================================================================
// Step 8 — Final mechanical polish: ready-for-dev (expert dev)
// ===========================================================================
phase('Refine')
log(`Revision done. Final mechanical polish on ${M.refine} — obvious mistakes, ordering, naming, precise outcomes…`)
const finalPlan = await agent(
  `Final light polish so the plan passes a "ready for dev" review. The pragmatic re-think already happened — do NOT re-architect, re-prioritize, ` +
  `or change scope here. Only: fix obvious mistakes and contradictions, correct ordering and naming inconsistencies, and tighten any remaining ` +
  `vague outcome into an observable acceptance check with precise, non-overlapping dependencies/exit criteria.\n\nPLAN:\n${JSON.stringify(plan, null, 1)}`,
  { label: 'refine-plan', phase: 'Refine', model: M.refine, schema: PLAN_SCHEMA }
)

log(`Done: "${finalPlan.title}" — ${(finalPlan.phases || []).length} phases ready for dev.`)

// The workflow returns structured data; the main loop writes it to a dated
// planning doc (timestamps aren't available inside the script).
return { state, alignment, candidates, investigations: investigations.filter(Boolean), issues, plan: finalPlan, reviews: reviews.filter(Boolean) }
