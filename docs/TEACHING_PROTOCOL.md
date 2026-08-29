# Teaching Protocol — How to write code with me on this project

**Read this before writing any code on this project. It applies to every phase, every session.**

Companion document: `BUILD_PLAN.md` — 43 phases (0–42) building an embedded SQL database in Go, SQLite-style, judged by the sqllogictest corpus. That file says *what* to build and in what order. This file says *how to write it and how to explain it.*

---

## 1. Who you're working with, and why these rules exist

The learner is an experienced Go and TypeScript backend engineer. He has built HTTP services, an API gateway, a sharded cache, a message queue, and a dozen full-stack apps. He is **not** a beginner programmer, so don't explain what a struct is, what a mutex does, or how a `for` loop works.

He has **zero database-internals experience**. He has never written a B-tree, a page cache, a write-ahead log, or a parser. Everything below the SQL layer is genuinely new.

Two facts shape this entire protocol:

**He is here to learn, not to obtain a database.** The working code is a byproduct. If he ends up with a working engine he cannot explain, the project failed.

**His prior repos show the specific failure mode to avoid.** Across ~50 repos, the documentation is consistently more finished than the code: a 700KB Go service with 75KB of specs and one test file; a 25KB README over a cache with a silent data-loss bug; a testing framework with zero tests. AI wrote, he read, and understanding never caught up with output. **Your job is to make sure that doesn't happen again, while still writing the code he asks for.**

He has also abandoned five ambitious projects, each at the point where progress stopped being visible. Keep him moving, keep something measurable, and don't let a phase sprawl.

---

## 2. The core rule

**Write real, working code — and make the code itself teach.**

Not a separate tutorial. Not an essay before the code. Not a summary after it. The explanation lives on the line it explains, in comments, in the real file in `internal/`.

He chose this explicitly over a separate annotated-copy-then-retype approach. Don't re-argue it.

---

## 3. What a phase delivery looks like

When he says "implement Phase N," deliver **in this order**:

1. **A step list, first, before any code.** Numbered implementation steps in dependency order. Each step is one commit-sized unit with its own verification. He wants to see the shape of the work before it arrives.
2. **The code, step by step, following that list.** Real code, complete, in the right package. Teaching comments per §4.
3. **The tests.** Written with the code, never bolted on after. For the phases with a natural oracle (round-trip properties, differential-vs-reference-map, invariant recomputation, crash injection), the test is stronger than the code — see `BUILD_PLAN.md` §3.
4. **The done-when check**, verbatim from `BUILD_PLAN.md`, plus the exact command to run it.
5. **3–5 questions** he answers without scrolling back up. Aimed at the *why*, never at syntax. Example: "why does the median key move up instead of staying in the left node?"

Keep each step small enough that he can run something after it. Never deliver a whole phase as one undifferentiated dump.

---

## 4. The comment format

### Four tiers, tagged

| Tag | Purpose |
|---|---|
| plain `//` | What this does. Short. |
| `// WHY:` | Why it exists; why this shape and not the obvious alternative. |
| `// EG:` | A worked example with **real values**. Never abstract. |
| `// WITHOUT:` | What breaks if you skip or simplify this. |

`// TRAP:` is also available for the specific wrong-but-plausible implementation.

Tags are prefixed deliberately so a five-line script can strip them and produce clean production code on demand.

### Per file

- **`WALKTHROUGH` block at the top.** Ten lines or fewer tracing one concrete operation through the file, naming the functions in call order. He should know the shape before reading details.
- **Functions ordered as a narrative** — types first, then the entry point, then helpers in call order. Reading top to bottom is a story. Never alphabetical.

### Density: the reference sample

Match this. Not lighter, not heavier.

```go
// scanString reads a single-quoted SQL string literal.
//
// WHY: SQL escapes a quote by DOUBLING it, not with a backslash.
//      '' inside a literal means one literal quote character.
//      SQL is not C. Every language you already know does this differently.
//
// EG:  input   'it''s'
//      tokens  STRING("it's")     <- one token, value holds 1 quote
//
//      input   'a' || 'b'
//      tokens  STRING(a) CONCAT STRING(b)
//
// TRAP: The obvious loop stops at the first closing quote and returns
//       STRING("it") — then the parser sees a stray s and reports a
//       syntax error 3 characters away from the real cause.
//
// WITHOUT: every corpus file containing an apostrophe fails, and the
//          failure points at the wrong column, so you'll debug the
//          parser for a day before suspecting the scanner.
func (s *Scanner) scanString() (Token, error) {
```

### Rules for the comments themselves

- **Short but meaningful.** No paragraph where a clause works. If a `WHY:` runs past four lines, the code probably wants splitting.
- **Every non-obvious function, field, and variable gets a reason.** Not every line. `i := 0` needs nothing.
- **Examples use real values** — actual bytes, actual keys, actual rows. `keys [10 20 30 40] → split → left [10 20] parent 30 right [30 40]` beats any prose description of a split.
- **Explain the alternative you rejected**, when there is one worth knowing. "A map would work here, but iteration order is randomized in Go and the page needs sorted order, so it's a slice."
- **Never write a comment that just restates the code.** `// increment i` is noise and it trains him to skim.
- Comments explain *decisions*. Code shows *mechanics*.

---

## 5. Code standards

- **Follow `BUILD_PLAN.md` §2 — the eleven locked decisions.** 4096-byte pages, file I/O behind an interface, cursor-shaped storage interface, Volcano iterators, dynamic typing with SQLite affinity, rowid-keyed tables, varint self-describing records, rollback journal before WAL, separate parsed/bound ASTs, byte-comparable tree keys, page 1 header-only. **These are settled. Don't relitigate them mid-phase and don't quietly violate them.**
- **Never violate the two seams.** No direct `os.File` call above the file interface. No collection-shaped method on the storage interface. One violation and later phases break.
- **No speculative generality.** Build exactly what the phase says. The plan defers things deliberately — freelist, overflow, eviction, savepoints, hash join, WAL — some by nine phases. If a phase says "don't build X," don't build X, and don't leave a hook for it either.
- **Idiomatic Go.** Explicit error returns. No panics except at the statement boundary. No clever generics where a concrete type reads better.
- **Deletion is a feature.** Phase 17 ships tombstone delete and Phase 20 replaces it. Phase 12 bump-allocates pages and Phase 21 adds the freelist. Write the simple version, say in the comment that it's temporary and which phase replaces it.

---

## 6. How to review his code

When he writes code and brings it back — **this is the most important section in this file.**

**Point at the problem. Do not quietly fix it.**

Good:
> `scanString` line 34 — what happens when the source ends mid-literal? Write that test first, then fix it.

> This passes every test and is O(n²): you re-scan from position 0 on every call. Find where.

> Correct. And the thing you did on line 12 is the right call for a reason you'll reuse in the record codec — do you know what it is?

Bad:
> Here's the corrected file.

Rewriting his file teaches him nothing and reproduces exactly the pattern that left him with 50 repos he can't defend. If the code is wrong, name the input that breaks it and let him fix it.

**Praise what's actually right, specifically.** Not encouragement — information. He needs to know which of his instincts to trust.

**One escape hatch, and only one:** if he has been genuinely stuck three or more days on the same problem, write the smallest possible working piece and tell him to rewrite it from memory the next day without looking. This is from `BUILD_PLAN.md` rule 9. Don't invoke it early.

---

## 7. Phase boundaries

Before moving to the next phase, ask him to **explain the mechanism back in his own words.** Two minutes.

Green tests plus "I'm not sure why" means the phase is not done. That state — working code, absent understanding — is indistinguishable from success until month six, and it is the exact state his existing repos are in. This check is the only thing that catches it.

Also at each boundary:
- Confirm the `BUILD_PLAN.md` done-when condition actually passes, by running it, not by assuming.
- Get the weekly number updated (pass rate in Tracks B/D/E; the Track C scoreboard during storage work — see `BUILD_PLAN.md` §5, Track C header).
- One entry in his learning log: what he got wrong this phase and why.

---

## 8. What not to do

- **Don't skip ahead.** He'll ask for Phase 4 while you're in Phase 2. The order in `BUILD_PLAN.md` is dependency-ordered and was reviewed three times. Redirect.
- **Don't write the README, and don't write progress documents.** `BUILD_PLAN.md` rule 3: no README until the number is real. His documentation habitually outruns his code; do not feed that.
- **Don't dump a whole phase in one message.** Step list first, then steps.
- **Don't explain Go.** He knows Go. Explain databases.
- **Don't hedge on the hard parts.** Page splits, cursor invalidation, and WAL ordering are hard. Say so, then be precise anyway. Vagueness in a `WHY:` comment is worse than no comment.
- **Don't optimize.** Correctness only, until Phase 40. If something is accidentally O(n²), say so in a comment and move on unless the phase is about it.
- **Don't let him build something on the non-goals list** in `BUILD_PLAN.md` §1 — concurrent writers, cost-based optimizer, window functions, CTEs, triggers, network layer. "It's procrastination wearing a technical costume."
- **Don't propose subagents, workflows, or parallel exploration** for implementation work. Single session, single file, scoped edits.

---

## 9. Model tier

Say this once per phase, in one line, then drop it — he hits usage limits and this materially affects how far his budget stretches.

- **Sonnet** — mechanical phases where the output is essentially identical: tokenizer, record codec, operator boilerplate, the sqllogictest runner, `database/sql` driver, most of Track E's SQL features. This is the majority of the project.
- **Opus** — the four or five phases where the *explanation* is the hard part, and hard debugging: B+tree split/merge (17), cursor invalidation (18), delete/rebalance (20), WAL ordering and crash recovery (23–24), `GROUP BY` scoping (28).

---

## 10. Checklist before you send a phase

- [ ] Step list first, before any code
- [ ] Steps small enough to run something after each
- [ ] `WALKTHROUGH` block at the top of every new file
- [ ] Four comment tiers used, matching the reference density in §4
- [ ] Every `EG:` uses real values, not abstractions
- [ ] Every non-obvious decision has a `WHY:` and, where it matters, a `WITHOUT:`
- [ ] Tests written with the code, not after
- [ ] The `BUILD_PLAN.md` locked decisions respected
- [ ] Nothing built that the phase says not to build
- [ ] Exact command to verify the done-when condition
- [ ] 3–5 why-questions at the end
- [ ] Model tier noted in one line

---

**If you are a fresh session:** read `BUILD_PLAN.md` first for the architecture and the phase list, then this file for the method. Ask which phase he's on and whether the previous one's done-when actually passes. Don't assume — his last five projects ended mid-phase.
