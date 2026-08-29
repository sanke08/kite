# Build Plan — An Embedded SQL Database in Go

**Codename:** TBD (pick one, it matters more than you think — you'll type it 10,000 times)
**Language:** Go
**Shape:** SQLite-style. Single file on disk, single writer, embedded as a library. No server, no network, no wire protocol.
**Judge:** SQLite's `sqllogictest` corpus. Pass rate is the only progress metric that counts.
**Phases:** 0 through 42, in five tracks.
**Estimated duration:** 6–9 months of evenings and weekends.

This document contains **no code**. It describes what to build, in what order, and how you know each step works. How to write the code is a separate conversation, per phase.

---

## 1. What we are building, and what we are not

### We are building

A real relational database engine: you type SQL into a REPL, it parses it, plans it, executes it against B+tree pages you laid out yourself in a single file, and the data survives `kill -9`.

Every hard part is yours. There is no Postgres underneath. There is no ORM. There is no library doing the interesting work.

### We are explicitly NOT building

Conscious cuts, decided now so they never become a debate later. Each costs something specific, and the cost is acceptable:

| Cut | What it costs |
|---|---|
| Multiple concurrent writers | Throughput ceiling. Buys months of avoided MVCC and locking bugs. |
| Cost-based query optimizer | Slow complex queries. Heuristic index selection is enough for correctness. |
| Window functions, CTEs, triggers, views, recursive queries | A small slice of the corpus. Very high effort per point. |
| `ALTER TABLE` beyond `ADD COLUMN` | Nothing important. Pure surface area. |
| Auto-vacuum, pointer maps | Nothing. SQLite historical baggage. |
| Multi-process file locking | Concurrent access from separate processes. Single-process only. |
| Unicode collations, i18n | Nothing — sqllogictest doesn't test it. |
| Client/server mode, network protocol, auth | Nothing. This is the part you already know how to build. |

If you find yourself building something on this list, stop. It is procrastination wearing a technical costume.

---

## 2. The eleven decisions that are locked

All expensive or impossible to reverse later. Decided **now**, before line one, so they never get relitigated at 1am in month four.

**1. Page size: 4096 bytes. Fixed. Not configurable.**
Matches OS and filesystem page size. SQLite's modern default. Configurability before anything works is optionality debt.

**2. File I/O sits behind an interface from the very first commit.**
Never call `os.File` directly from anywhere except one implementation of that interface. Cheapest decision in this document today, most expensive to retrofit. It's what makes deterministic crash testing possible in Phase 11 without a rewrite. Ben Johnson built exactly this mock filesystem seam within Bolt's first ~15 commits, *before* the B-tree.

**3. Table storage sits behind a cursor-shaped interface from the very first commit.**
`Open / Seek / Next / Insert / Update / Delete`. **Cursor-shaped, not collection-shaped** — never `GetAllRows()`, because that's an interface a B+tree cannot honestly implement, and the plan depends on the swaps in Phases 15 and 19 being honest.

**4. Execution model: Volcano / iterator. `Next()` pull model. Not a tree-walker, not a bytecode VM.**
Every physical operator is a struct with a `Next()` returning one row. Migrating from ad-hoc tree-walking to iterators later means rewriting every operator's control flow. A bytecode VM (SQLite's VDBE) is a rewrite justified by prepared-statement reuse across billions of devices, not by learning. Tree-walking is fine for *scalar expression evaluation inside* a `Next()` call — orthogonal and correct.

**5. Typing: dynamic values with SQLite-style type affinity. Not static column types.**
sqllogictest is derived from SQLite's own compatibility corpus. Matching SQLite's storage-class and coercion rules minimizes semantic mismatch. Retrofitting strict typing later is a full rewrite of the record codec, every comparison, and every aggregate accumulator.

**6. Rowid-keyed tables. Not clustered primary keys.**
The table B+tree is keyed by an implicit integer rowid. Secondary indexes map `(indexed columns..., rowid) → rowid`. `WITHOUT ROWID`-style clustered storage is a legitimate later optimization, never a prerequisite.

**7. Record format: self-describing, varint-encoded, type-tagged per value.**
Header describes each column's type and length, then values concatenated. Fixed-width columns are simpler to write and impossible to migrate away from once a real file exists.

**8. Durability: rollback journal first. WAL later, or never.**
Undo-log semantics: copy original pages out before modifying; journal deletion is the atomic commit point. No wal-index, no checkpoint scheduler, no reader-snapshot bookkeeping — three fewer subsystems before "does not lose data" is a meaningful claim. Its recovery invariant is one sentence, which makes the crash-test oracle trivial. WAL becomes an alternate pager backend much later if concurrent readers ever matter.

**9. Two AST representations: parsed and bound. Never one mutated in place.**
The parsed tree has raw names. The bound tree has resolved slot indices and types. Bolting resolved fields onto the parse tree leaks parser concerns into the planner and never un-leaks.

**10. The B+tree is keyed by opaque byte-comparable keys with a comparator. Never hardcoded `int64` rowids.**
The table tree keys on encoded rowids; secondary indexes in Phase 30 key on arbitrary multi-column values. If Phase 17's tree hardcodes integer keys, Phase 30 is a rewrite of the tree — not a new user of it. Decide this before writing the first node.

**11. Page 1 is header-only. Real data starts at page 2.**
SQLite packs its 100-byte file header into the front of page 1 and shares that page with tree data. Don't copy that. Giving page 1 entirely to the header eliminates an entire class of off-by-one bug that otherwise appears *only on the root page* and is miserable to diagnose. Costs 4KB of file. Take the trade.

**Also locked, minor:** physical (whole-page-image) logging over logical; page-level over record-level log granularity; recursive descent + Pratt for parsing, no parser generator; one coarse `RWMutex` over the tree instead of latch crabbing; explicit `error` returns, with `recover()` only at the statement boundary.

---

## 3. The ordering principle, and the one place I'm deviating from the research

### The principle you gave me

Every phase must end in something **runnable and testable end to end**. Never "build the storage engine" as a phase — there'd be no way to drive it. Vertical slices, not horizontal layers. Login before home screen.

### What everyone who finished actually did

Real build order reconstructed from commit history, chapter order, and course syllabi. Unanimous: **bolt, toydb, build-your-own.org, Dolt — every one went bottom-up.** Durable page store, then ordered tree, then transactions, SQL bolted on at the very end. In toydb's original history the SQL parser is among the last major commits, month four. In build-your-own.org, SQL is chapters 13–14 of 14. Dolt didn't write a parser at all for three years — they reused Vitess.

Course syllabi (CMU 15-445, MIT, Berkeley) teach SQL semantics first conceptually but *implement* it last, and in CMU's case the parser and binder are simply **given to students**. Nobody builds the SQL layer first. It is not a school of thought.

### Where projects die

Same research, most important finding: the common death point is **right after the B-tree structural work is done, before it's wired into anything relational.** cstack's tutorial — the most-read from-scratch database tutorial in existence — stops at Part 14, "Splitting Internal Nodes," and never reaches multiple tables, `WHERE`, `UPDATE`, `DELETE`, joins, or transactions. The B-tree got finished. The database never existed.

That's your exact failure mode, and it's the one thing this plan is engineered against.

### So: the deviation

**We go top-down first for one deliberate stretch, then bottom-up for the rest.**

Phases 1–10 build the entire SQL frontend against an in-memory storage stub. No disk code at all. Six to ten weeks. At the end you have a working in-memory SQL database with a real, climbing, externally-verified pass rate.

Then Phases 11–22 replace the stub with real disk storage — pager, slotted pages, heap file, B+tree — behind the identical interface.

The reason isn't that top-down is better engineering. It isn't; the finishers are right. The reason is specific to you:

1. **When you enter the B-tree tunnel, you will already have a working database sitting on top of it.** The B-tree isn't something you build in the dark hoping it's useful. It's an engine swap in a running car.
2. **Your regression net will already exist and will be brutal.** The moment the B+tree is wrong, dozens of SQL tests go red and name *which query* broke. cstack had no such net. That's why finishing the tree felt like finishing the project.
3. **You get the "I finished something hard" experience at week ~10 instead of month ~6.** You have never had that experience. Getting it early is the whole game.

The risk is real and worth naming: **designing the storage interface before you understand storage means the interface might be wrong.** Mitigation is Decision #3 — cursor-shaped, the shape a B+tree can actually honor. If it's wrong you find out in Phase 15, cheaply, with tests naming the exact broken query. Contained, informative failure. The good kind.

### The honest version of your ordering rule

Your rule was "every phase must be testable end to end." Six phases in Track C and D cannot be, because they're below the SQL layer and the SQL layer can't reach them yet: **11** (fault-injecting file), **13** (record codec), **14** (slotted page), **17** (B+tree insert/split), **18** (cursors), **23/24** (journal and crash matrix).

So the rule as literally stated is impossible, and pretending otherwise would just hide where the risk is. Here is the version that actually holds, and it's stronger:

**Every phase is gated by an oracle at least as strict as an end-to-end test.** Not "it compiles and looks right" — an independent source of truth that can call you wrong.

| Phase | Oracle |
|---|---|
| 11 | Deterministic replay — same seed, same fault sequence, asserted survival set |
| 13 | Round-trip property test over randomly generated rows |
| 14 | Free space recomputed from scratch, compared to the maintained counter after *every* operation |
| 17, 18, 20 | Differential test against an in-memory reference map over random operation sequences |
| 19 | The entire SQL suite, at an unchanged pass rate |
| 23, 24 | Byte-exact state equality after crash injection at every write and sync boundary |

Two of those — 17 and 18 — are the only phases in the whole plan where you're building blind for more than a week, and they're deliberately consecutive so the tunnel is one stretch and not three. Phase 19 ends it by putting the tree under the full SQL suite.

Everything outside that table ends with SQL going in and rows coming out.

---

## 4. The two seams

Everything hangs off two interfaces defined in Phase 0. Get these right and the plan works. Violate them once, anywhere, and it collapses.

**Seam 1 — the file interface.** Every byte that reaches disk goes through it. Read at offset, write at offset, sync, truncate, size, close. Two implementations: real OS file, and a fake that buffers unsynced writes in memory and can be told "crash after write N," "crash after sync N," "tear the last write," "return EIO from sync." *Nothing above this layer knows which it has.*

**Seam 2 — the storage interface.** Cursor-shaped table access. Implementations, in order of arrival: in-memory slice (Phase 3), disk heap file (Phase 15), B+tree (Phase 19). *Nothing above this layer changes when the implementation swaps.* When it does, the whole SQL test suite is the proof.

The single most repeated finding across all seven research reports: build the testability seam **before** the thing gets complex, never after.

---

## 5. The phases

Each phase: what it's for, what to build, what to explicitly not build, and the condition that means it's done. Small on purpose. No phase ends with nothing to run.

---

### TRACK A — Skeleton and signal
*Target: 1–2 weeks. Nothing hard. Everything that makes the hard parts survivable.*

---

#### Phase 0 — The two seams, the REPL, and a pass rate on day five

**Goal:** Before any database exists, the machinery that will tell you the truth for the next nine months.

**Build:**
- Repo skeleton, Go module, package layout reflecting the eventual layers.
- A REPL binary. Reads a line, prints something, exits on `.quit`. Dot-commands stubbed for later (`.schema`, `.tables`, `.explain`).
- **Seam 1**, the file interface, with the real OS implementation only. The fake arrives in Phase 11 — but the interface exists now, so it can.
- **Seam 2**, the storage interface. Cursor-shaped. No implementation yet.
- A sqllogictest runner. Read `dolthub/sqllogictest`'s Go parser to understand the format, then write your own — it's ~300 lines and you need to own it. It must report total, passed, failed, **skipped as its own bucket**, and a per-file breakdown.
- A hand-written `.slt` file of your own — 10 to 20 blocks, testing what you intend to support in Phases 1–4.
- **A pinned manifest of active test files**, checked into the repo. A plain list. It starts as `["mine.slt"]` and grows deliberately, one entry at a time, as capability lands.
- **Decide your engine label**, because the corpus is engine-conditional via `skipif` / `onlyif`. Claim `sqlite`. That's consistent with Decision #5 and means the expected values you inherit are the ones you're building toward. Write this decision down; it silently determines thousands of expected results.

**Don't build:** anything database-shaped. No parser. No storage. Resist entirely.

**Done when:** two things, not one.

1. `make test` prints `3/3 (100%)` against your own hand-written `.slt`, and the REPL echoes input.
2. **The runner is validated against real SQLite.** Point it at the actual `sqlite3` binary as the engine under test — shell out to it, don't take on cgo — and confirm it reports ~100% on a handful of upstream corpus files. If your runner says SQLite fails its own test suite, **your runner is broken, not SQLite.**

**Why step 2 is not optional:** this entire plan rests on one number being honest for nine months. An unvalidated runner that mishandles result hashing, `rowsort`'s ASCII `strcmp` ordering, or `skipif` blocks doesn't give you a *low* number — it gives you a *wrong* number, in either direction, and you will make months of decisions on it. Validate the judge before you trust the judge. It costs an afternoon.

**Why the manifest:** if the set of files you run changes every week, the denominator changes and the percentage is meaningless — you can't tell progress from a shifting sample, and you can quietly inflate the number by dropping hard files. Pin it, grow it on purpose, and log every addition. Report **both** file-count and query-count pass rates, always: a few generated corpus files are 1.2–1.5MB each and dominate query volume, so the two numbers tell genuinely different stories.

**Why here:** this is the anti-abandonment device, and it goes first. Green number in week one. Every phase after this moves it. The infrastructure never needs revisiting.

**Trap:** Don't point at the real upstream corpus yet. Its smallest files already assume multi-operator `WHERE` clauses and type coercion you won't have for a month. You'd get 0% and learn nothing. Your own file first.

---

### TRACK B — A real SQL database, in memory
*Target: 6–10 weeks. The whole frontend, zero disk code.*

---

#### Phase 1 — Tokenizer

**Build:** SQL text to token stream. Keywords, identifiers, operators, punctuation. String literals with `''` escaping — **not** backslash escaping, SQL is not C. Double-quoted identifiers. Integer and float literals. Comments (`--` and `/* */`). The keyword-versus-identifier ambiguity.

**Don't build:** Unicode edge cases, hex blob literals, scientific-notation exotica.

**Done when:** Golden tests — fixed input, expected token list. A dozen cases covering every quoting and escaping form.

---

#### Phase 2 — Expression parser and evaluator: `SELECT 1+2*3;` → `7`

**Build:**
- Pratt / precedence-climbing expression parser. SQL precedence: `OR` < `AND` < `NOT` < comparison < `+ -` < `* /` < unary.
- The `Value` type — dynamic, type-tagged, per Decision #5. Integer, real, text, blob, null.
- **A three-valued logic type. Explicitly. Not Go's `bool`.** TRUE / FALSE / UNKNOWN. Build it now or `AND`/`OR`/`NOT` silently miscompute forever.
- A scalar evaluator: walk an expression tree, produce a `Value`.
- Wire it to the REPL. `SELECT` with no `FROM`.

**Done when:** `SELECT 1+2*3;` prints `7`. `SELECT 'a' || 'b';` prints `ab`. `SELECT 1 = NULL;` prints `NULL`, not `false`. Add to your `.slt`; pass rate stays 100%.

**Why here:** Smallest thing that proves the whole pipeline shape — text in, answer out — with no storage at all.

**Trap:** Unary minus versus binary minus is the classic Pratt off-by-one in binding powers. Test `SELECT -2+3;` and `SELECT 2- -3;` deliberately.

---

#### Phase 3 — Catalog, `CREATE TABLE`, and the in-memory storage stub

**Build:**
- DDL parsing for `CREATE TABLE` with column names and declared types.
- The catalog: table name → ordered column list with declared type/affinity and nullability. A plain Go map — **no persistence** (that's Phase 16).
- **The first implementation of Seam 2:** a table is a slice of rows. `Insert` appends. The cursor walks the slice.
- `.tables` and `.schema` so you can see it worked.

**Don't build:** persistence, constraint enforcement, indexes.

**Done when:** `CREATE TABLE t(a INTEGER, b TEXT);` then `.schema` shows it. `statement ok` blocks for DDL pass.

---

#### Phase 4 — `INSERT` and `SELECT * FROM t`: the first real query

**Build:**
- `INSERT INTO t VALUES (...)` parsing and execution.
- The row representation.
- Your first two Volcano operators: `SeqScan` (pulls from the storage cursor) and `Project` (passthrough for now).
- The `Next()` contract. Get it right — every operator for the next nine months implements it.

**Done when:** `INSERT` three rows, `SELECT * FROM t` returns exactly those three in insertion order. First moment the thing is a database.

**Why here:** End-to-end vertical slice complete: tokenizer → parser → catalog → executor → storage → output. Everything from here is widening it.

---

#### Phase 5 — The binder, and `WHERE`

**Build:**
- **The binder.** Parsed AST → bound AST, per Decision #9. Resolve table names against the catalog, resolve column names to slot indices, attach types, expand `SELECT *` **here** using catalog column order — never in the parser.
- Real errors: unknown table, unknown column, ambiguous column.
- `WHERE` parsing and the `Filter` operator.
- **UNKNOWN must be rejected by `WHERE`, same as FALSE.** Not the same as `GROUP BY`, which will treat all NULLs as one group. The asymmetry is real and it's coming.

**Done when:** `SELECT * FROM t WHERE a > 5` filters correctly, including rows where `a` is NULL (which must not appear). Then **point the runner at the upstream `evidence/` directory** — the small, hand-written, fundamental files. Get a first real, honest, non-zero number against a corpus you didn't write. Record it.

**Why here:** `WHERE` unlocks the largest single share of single-table corpus coverage of any feature.

---

#### Phase 6 — Projection, aliases, expressions in the select list

**Build:** Named column lists, `AS` aliases, arbitrary expressions in the select list, `t.*` for a qualified table. Output schema as a concept separate from input schema.

**Done when:** `SELECT a+1 AS x, 'lit' FROM t WHERE b = 'q'` works with correct column names.

---

#### Phase 7 — `CASE`, `CAST`, `COALESCE`, and scalar functions

**Build:** `CASE WHEN ... THEN ... ELSE ... END` (both searched and simple forms). `CAST(x AS type)` following SQLite's affinity conversion rules. `COALESCE`, `IFNULL`, `NULLIF`. A scalar function dispatch mechanism plus the common ones: `length`, `substr`, `upper`, `lower`, `abs`, `round`, `trim`, `replace`, `typeof`. `LIKE` and `BETWEEN` and `IN (list)` as expression forms.

**Done when:** the `random/expr` corpus directory — roughly 120 files, one of the largest in the whole suite — starts passing in bulk.

**Why here, in Track B:** it needs nothing but Phase 2's evaluator, requires zero new machinery, and buys more corpus coverage per hour than anything else in the entire plan. Cheapest pass-rate phase in the document. Do it before you disappear into storage.

**Trap:** `NOT IN`, `NOT LIKE`, `NOT BETWEEN` are grammar-level negated forms, not general `NOT` wrapped around a call. Parse them as such.

---

#### Phase 8 — `ORDER BY`, `LIMIT`, `OFFSET`, `DISTINCT`

**Build:**
- The `Sort` operator. **Your first blocking operator** — it must consume its entire input before yielding a single row. Structurally different from everything so far.
- Multi-key comparators with `ASC`/`DESC`. **NULLs sort first ascending, last descending** — that's SQLite's behaviour, and since you claimed the `sqlite` engine label in Phase 0, it isn't an open decision. Don't leave it to chance and then wonder why ordered tests fail.
- `Limit` / `Offset` as a counting operator.
- `DISTINCT` — hash-set dedup, where NULL equals NULL (unlike `WHERE`).

**Don't build:** external / spilling sort. In-memory is fine; note it as a known limit.

**Done when:** ordered queries return deterministic order matching sqllogictest's `nosort` expectations. **Big pass-rate jump** — a large share of the corpus uses `ORDER BY` specifically to be deterministic.

**Trap:** sqllogictest's `rowsort` mode sorts rendered ASCII with `strcmp`, so `"9" > "10"`. Your runner must reproduce that exactly or you'll chase phantom failures.

---

#### Phase 9 — `UPDATE`, `DELETE`, and the error path

**Build:** `UPDATE ... SET ... WHERE`, `DELETE FROM ... WHERE`, mutation through the storage interface, and a real error-reporting path so `statement error` blocks pass.

**Known limitation to document, not fix:** until Phase 25, a statement that fails partway through a multi-row mutation (say a runtime error mid-scan) leaves partial changes behind. Statement-level atomicity arrives with the journal. Write this in the README now — an honest known limit beats a silent one.

**Done when:** mutation-then-verify test pairs pass. `statement error` blocks pass for genuine errors.

**Why here:** last piece of the interface contract. When disk storage swaps in at Phase 15, `Insert`/`Update`/`Delete`/`Seek`/`Next` will all be exercised by existing tests. Anything left un-exercised now is a bug you'll meet the hard way in month four.

---

#### Phase 10 — The conformance pass: NULL semantics and type affinity

**Goal:** A phase whose entire purpose is raising the pass rate on things you already built. No new features.

**Build:**
- Read SQLite's `datatype3.html` properly. Implement type affinity and comparison rules to match.
- Audit every comparison, arithmetic op, and aggregate for NULL handling.
- Numeric rendering matching sqllogictest exactly — integers `%d`, floats `%.3f`, NULL as literal `NULL`, empty string as `(empty)`.
- Walk your top failing corpus files one at a time and fix the actual causes.

**Done when:** the pass rate jumps significantly with no new SQL features, and you can explain every remaining failure class in one sentence each.

**Why here:** You're about to disappear into storage for three months. Go in with a **high, honest number** and a fully working in-memory database. Milestone one: *the first hard thing you have ever finished.* Tag the commit. Write the number down.

---

### TRACK C — Real storage on disk
*Target: 11–18 weeks. This is the tunnel. This is where the other five repos died.*

> **Read this before starting Track C. It is the most important paragraph in the document.**
>
> For the next three to four months **the pass rate cannot go up.** You are adding zero SQL features. The number that has been pulling you forward since week one goes flat — and it goes flat across exactly the stretch where every comparable project has died. The anti-abandonment device fails precisely where it's needed most. That is a hole in the plan, not in you, so here's the patch:
>
> **Track C gets its own scoreboard, published weekly alongside the flat pass rate.** Four numbers, all of which move every single week:
>
> 1. **Torture scale** — largest key count the B+tree survives with all invariants asserted. Grows 100 → 10k → 1M → 10M. This is the headline number for Phases 17–20.
> 2. **Differential ops** — count of random operation sequences compared against the in-memory reference map with zero divergence. Only goes up.
> 3. **Invariants asserted** — how many structural properties are checked continuously (sorted order, occupancy bounds, height, free-space accounting, no lost keys, cursor stability). Each new one is a permanent ratchet.
> 4. **Crash points passing** — from Phase 23 on. Goes from 0 to thousands.
>
> Same discipline as the pass rate: one line, published weekly, no prose. `torture: 1M keys · diff: 40k ops · invariants: 6 · crash: —`
>
> And the flat pass rate is itself a result worth stating out loud: **"unchanged at 34% across the entire storage rewrite"** means the swap was clean. A flat number here is a *pass*, not a stall. Say so in the weekly line so you don't read your own progress as failure.

---

#### Phase 11 — The fault-injecting file, and nothing else

**Build:** The second implementation of Seam 1. A fake file that buffers unsynced writes in memory, promotes them to a durable backing store only on `Sync()`, and can be commanded to: crash after the Nth write, crash after the Nth sync, truncate or corrupt the last unsynced write (torn write), return `EIO` from sync. Seeded and deterministic — same seed, same fault sequence, every time.

**Don't build:** anything that uses it yet.

**Done when:** unit tests prove the fake behaves — write without sync then crash loses data; write then sync then crash keeps it; torn-write mode produces a partial page. Same seed reproduces the same scenario exactly.

**Why here, before the pager:** Bolt built its mock filesystem in its first fifteen commits, before the B-tree. Every storage bug from here on is diagnosable in a deterministic, in-process, millisecond test instead of a `kill -9` loop that proves nothing.

**Note:** this is the deterministic-simulation work you started in your `DST` repo, finally with a real customer. `timesim/clock.go` and `randsim/rand.go` port over almost directly. The framework was unadoptable; the technique was always right.

**Trap:** `kill -9` on your own process is **not** a crash test. The OS page cache survives it. It proves nothing about write ordering or torn writes. Real crash testing needs this fake — which is why SQLite ships a crash-simulating VFS and TigerBeetle and FoundationDB inject at the I/O layer.

---

#### Phase 12 — Pager and buffer pool

**Build:**
- Page abstraction: fixed 4096-byte buffers addressed by page number. **Only the pager touches the file.** Nothing above it ever computes a byte offset.
- File header on **page 1, which holds nothing else** (Decision #11) — magic, version, page size, page count, freelist head, schema root page. Data pages start at 2.
- Buffer pool: page table, pin/unpin, dirty bits, flush.
- API: fetch, unpin, allocate, flush.

**Don't build:** eviction. Cache everything; assert the working set fits. LRU/CLOCK/LRU-K comes only when a memory-pressure test demands it. Skip the freelist too — bump-allocate page numbers and leak on delete.

**Done when:** write pages, close, **reopen in a fresh process**, read back byte-identical. Under both the real file and the fake.

---

#### Phase 13 — The record codec

**Build:** Per Decision #7. Varint encoding. Type-tagged serial types per column. Row → bytes → row.

**Done when:** property-based round-trip. Generate random rows with every type including NULL and edge-case integers (0, ±1, maxint, minint), encode, decode, assert deep equality. Thousands of cases, no exceptions.

**Why here:** the one piece of the storage layer with a perfect oracle, so it should be bulletproof before anything depends on it.

---

#### Phase 14 — The slotted page

**Build:** Page header, cell pointer array growing forward, cell content area growing backward from the end, free space in the middle. Cell pointers held in key order so binary search works without moving cell bodies. Insert a cell, delete a cell, free-space accounting.

**Don't build:** freeblock reuse and fragmentation tracking. Start with "content area only grows down; page is full when it can't fit." Add reuse when a test proves you need it.

**Done when:** the invariant test. Repeatedly insert and delete variable-sized cells on one page, and **after every single operation** assert that free space recomputed from scratch equals the maintained counter. This finds the bug class that otherwise corrupts live cells silently.

---

#### Phase 15 — The heap file, and the first swap

**Goal:** Real bytes on disk under the real SQL engine.

**Build:** A second implementation of Seam 2 — a disk-backed table using the pager, slotted pages, and the record codec. Rows appended to pages. Cursor walks pages in order. Rowid assignment from an in-memory counter. No tree, no index, no ordering guarantee beyond insertion order.

Then **swap it in** as the default backend.

**Done when:** **the entire sqllogictest suite from Phases 1–10 passes against disk storage**, at the same pass rate as before, within a single process run. (Surviving restart needs the catalog on disk — that's the next phase.)

**Why this phase matters more than any other:** it's the proof the whole plan works. The frontend didn't change. The tests didn't change. The number didn't change. Only the bytes moved. If the interface was wrong you find out here, cheaply, with tests naming the exact broken query. And you get a visible, undeniable win — *your database is on disk* — before touching the B+tree.

---

#### Phase 16 — The schema table: the database becomes persistent

**Goal:** Close the loop. Until this phase, restarting loses the catalog, so your on-disk rows are unreadable bytes.

**Build:**
- A schema table stored **as a normal table**, through the same record codec and heap-file backend as user data. Rows describing each table: name, type, root page, and the original SQL text.
- Its root page number is a **hardcoded constant** in the header. This is the whole answer to the "you need a table to read tables" bootstrap problem — it isn't recursive, it's a constant. Don't build a general catalog-of-catalogs.
- On open: read the schema table, populate the in-memory catalog. On `CREATE TABLE`: allocate a root page, write a schema row.
- `DROP TABLE` and `DROP INDEX` — the corpus uses them constantly in setup and teardown.
- **Rowid high-water mark recovery.** After restart, new rowids must not collide with existing ones. Either persist the counter or derive it by seeking the last key. Decide and test it.

**Done when:** `CREATE TABLE`, `INSERT` rows, **exit the process**, restart, and `SELECT` returns the data. Then run the full suite twice in a row against the same file without wiping it.

**Why here:** it needs a working table backend to store itself in (Phase 15) and the record codec (Phase 13), and everything after it assumes durability of schema. Phase 15's win is "bytes on disk"; this one's is "**it's a database file now.**"

**Trap:** Nothing in Phases 1–15 forced you to think about schema persistence, which is exactly why it's easy to reach Phase 20 and discover your B+tree stores data nobody can ever read back. This phase exists because the first draft of this plan didn't have it.

---

#### Phase 17 — B+tree: search, insert, split

**Build:**
- Node layout on the slotted page: internal nodes hold separator keys and child pointers plus a rightmost pointer; leaves hold keys and payloads. **Keys are opaque byte-comparable values with a comparator, per Decision #10** — not `int64`.
- Search: binary search at each level, root to leaf.
- Insert with **page split**: allocate sibling, move the top half, promote the median key to the parent, cascade if the parent overflows, and **allocate a genuinely new root page** if the root splits. Root splitting is the only way the tree gains height.

- **Tombstone delete.** Removing a key marks its cell dead and nothing else — no underflow check, no borrowing, no merging, no page reclamation. The tree gets progressively emptier and never rebalances.

**Don't build:** real delete with rebalancing (that's Phase 20). Don't build overflow. Don't build the freelist.

**Why tombstones now:** `DELETE FROM` has been a working SQL statement since Phase 9, so the storage interface must honor `Delete` before the swap in Phase 19 — otherwise the swap can't be gated on the full test suite and you'd be flying blind for two more phases. Tombstones give you *correct* delete semantics for a tenth of the work, and Phase 20 upgrades them in place. This is the trick that keeps the vertical slice intact through the hardest stretch of the project. Many production systems ship tombstones permanently; you're just borrowing the idea for six weeks.

**Done when:** the torture suite. (a) Insert N random keys, traverse leaves in order, assert sorted, assert every key present exactly once. (b) Assert every non-root leaf's occupancy is within bounds. (c) Assert height is O(log_fanout N). (d) Run at N = 1,000,000. (e) **Differential test against an in-memory reference map** — random operation sequences, compare every read.

Start with a deliberately tiny fanout — 3 or 4 keys per node — so splits happen constantly and cascade on small, debuggable inputs. Only then go to real fanout and a million keys.

**Traps, all silent:**
- Median-key off-by-one: whether the median stays in the left node or moves up and is removed from both children. Corrupts search silently. May take thousands of inserts to surface.
- Root split reusing the old root page number as a leaf. Silently drops half the tree.
- Forgetting the rightmost child pointer in internal nodes.

**Reality check:** the hardest component in the project. Four to eight weeks is normal, and that range assumes course scaffolding — solo, expect the top of it. **This is where cstack stopped. This is where the other five repos would have stopped.** The difference: when you finish, you have a working SQL database sitting on top of it and a suite that tells you in seconds whether it's right.

---

#### Phase 18 — Cursors, and cursor invalidation

**Build:** A cursor over the tree: open, seek to key, next, previous, current row. And the thing nearly everyone gets wrong: **a cursor must not trust a remembered `(page, slot)` across a mutation.** Copy SQLite's model — the cursor saves its logical key, marks itself as requiring a re-seek, and lazily re-derives its position with a fresh top-down search on next use.

**Done when:** open a cursor, position it mid-scan, insert and delete elsewhere in the tree, resume, and assert it skips nothing and duplicates nothing.

**Why its own phase:** a cursor assuming page/slot stability produces skipped or duplicated rows during scans interleaved with mutation. One of the hardest bugs in this project to reproduce after the fact — and precisely what `UPDATE ... WHERE` will do to you.

---

#### Phase 19 — The second swap: B+tree as the table backend

**Build:** Implement Seam 2 over the B+tree, rowid-keyed. Swap it in. Keep the heap file — it becomes a test oracle, not dead code.

**Done when:** full suite passes again, same or better pass rate, and data still survives restart. **Plus a backend differential test:** run the same operation sequences against the heap file and the B+tree and diff every result. Two independent implementations of one interface is a free oracle for the rest of the project's life — use it. And now `ORDER BY rowid` is free, because storage is ordered.

**Also: record baseline timings here and keep recording them.** Bulk insert, point lookup, full scan, range scan. Not to optimize — to notice. An accidentally O(n²) page split or a cursor that re-seeks from the root on every `Next()` is invisible to correctness tests and obvious in a timing that got 40× worse between commits. Phase 40 turns this into a real harness; for now, four numbers in a file.

---

#### Phase 20 — Real delete: underflow, redistribution, merge

**Build:** Upgrade Phase 17's tombstones to real deletion. Remove the cell; if occupancy drops below minimum, **first try redistribution** — borrow from a sibling through the shared parent separator — and only merge if redistribution isn't possible. Merges cascade upward exactly as splits do, and the root can lose a level.

**Done when:** interleaved random insert/delete torture against the reference map, with occupancy bounds asserted continuously — **and the full SQL suite still green**, because the tree has been the live backend since Phase 19. This is a refactor under test, not new construction, which is the easiest form of hard work there is.

**Trap:** delete is **not** symmetric with insert. The redistribute-before-merge branch is the one people skip, and skipping it leaves the tree *correct but permanently under-occupied* — which correctness tests never catch. Only an occupancy-bound assertion finds it.

---

#### Phase 21 — Free list and page reuse

**Build:** Track freed pages, reuse them on allocation. Replaces the bump allocator from Phase 12.

**Done when:** a loop inserting then deleting a million rows repeatedly does not grow the file without bound.

**Why now and not earlier:** you deferred it deliberately for nine phases and nothing suffered. This is what correct deferral looks like.

---

#### Phase 22 — Overflow pages

**Build:** When a payload exceeds an on-page threshold, store a prefix in the cell plus a pointer to a linked list of overflow pages.

**Don't build:** SQLite's exact `X`/`M`/`K` threshold formulas. Historical constants tuned for SQLite's specific header sizes. Pick your own simple threshold and move on.

**Done when:** a 1MB `TEXT` value round-trips, and every B+tree invariant test still passes with large values mixed in.

---

### TRACK D — Durability
*Target: 6–8 weeks. Where "database" stops being a figure of speech.*

---

#### Phase 23 — The rollback journal

**Build:** Per Decision #8. Before first modifying any page in a transaction, write its pre-image plus a checksum to the journal file and sync. Then write dirty pages in place. Then sync the database file. Then **delete the journal — that deletion is the atomic commit point.** On open, a present journal means a hot journal: restore all pre-images, delete it.

**Done when:** the crash matrix. Using the Phase 11 fake, crash at **every** write and sync boundary within a transaction, reopen, and assert the database equals *either* the exact pre-transaction state *or* the exact post-transaction state. Never anything between. Not "didn't crash" — byte-exact state equality.

---

#### Phase 24 — The crash matrix as permanent CI

**Build:** Promote Phase 23's crash test into an exhaustive, permanent suite. Every injection point, run automatically, forever. Include torn-write injection and assert checksums detect it and recovery discards exactly the corrupt tail and nothing more. Every failure must print the seed that reproduces it.

**Done when:** thousands of injected crash points all passing, fast enough to run on every commit — and a failing case is reproducible from a single integer.

**Why its own phase:** this is the artifact. "I ran N thousand injected crash scenarios; here are the bugs it found, each reproducible from a seed" is the single most credible thing you will produce in this entire project. Not the B+tree. This.

---

#### Phase 25 — Transactions in SQL

**Build:** `BEGIN`, `COMMIT`, `ROLLBACK` wired to the journal. Implicit single-statement transactions — which retroactively fixes the Phase 9 partial-mutation limitation. Retire the no-op transaction layer from Track B.

**Don't build:** savepoints. Just named markers into the undo list — pure bookkeeping, no new durability mechanism. Defer.

**Done when:** transaction test files pass. `ROLLBACK` after a partial multi-statement transaction leaves exactly the pre-`BEGIN` state. A statement that errors mid-mutation leaves no partial changes.

---

#### Phase 26 — The fsync truth pass

**Build:**
- **macOS specifically: `fsync()` on Darwin does not flush the drive's write cache.** Go's `file.Sync()` gives you Linux-style `fsync`. Only `fcntl(F_FULLFSYNC)` is a real barrier. Implement it, expose it as an option, document the difference.
- **Any fsync error is fatal.** Do not retry. Linux historically marked pages clean after reporting `EIO` once, so a second `fsync` returns success on data that is permanently gone — Postgres's 2018 fsyncgate. Abort and require recovery.
- Write the durability guarantee down precisely in the README. What survives process crash. What survives power loss. What doesn't.

**Done when:** the `EIO`-injection test asserts the engine aborts rather than continuing, and the README states the guarantee in language you could defend to a stranger.

**Why here:** "survives `kill -9` on my MacBook" and "survives power loss" are different claims. Knowing the difference — and saying so honestly — is the difference between your `Distributed-Cache` README and a real one.

**Milestone two.** End of Track D: a durable, crash-tested, transactional SQL database on disk. Tag it. More than 95% of people who start this project never have this.

---

### TRACK E — Widening SQL
*Target: open-ended. Every phase raises the pass rate and each is independently shippable.*

---

#### Phase 27 — Aggregates without `GROUP BY`
`COUNT`, `SUM`, `MIN`, `MAX`, `AVG` over the whole table. The `Aggregate` operator with a single implicit group. NULL-skip semantics — `SUM`/`AVG` ignore NULL, `COUNT(*)` and `COUNT(col)` differ. The binder must reject select lists mixing aggregates with bare columns.

#### Phase 28 — `GROUP BY` and `HAVING`
Hash aggregation. Group-key hashing where **all NULLs group together** — the opposite of `WHERE` equality. And the binder scope split: `WHERE` resolves against raw rows and cannot reference aggregates; `HAVING` resolves against group output and can reference aggregates absent from the select list.
**Trap:** conflating those two scopes is the single most common binder bug in from-scratch engines. Big pass-rate jump when right.

#### Phase 29 — Constraints
`PRIMARY KEY`, `UNIQUE`, `NOT NULL`, `DEFAULT`, `CHECK`. Catalog metadata, enforcement at insert and update, correct error reporting for `statement error` blocks.

#### Phase 30 — `CREATE INDEX`, secondary indexes, and the first planner decision
An index is just another B+tree — same node format, same split code, same cursor (and this is where Decision #10 pays for itself: keys are `(indexed columns..., rowid)`, with rowid appended as tiebreaker even for non-unique indexes). Build the `IndexScan` operator and the planner rule: equality or range predicate on an indexed column → `IndexScan`, otherwise `SeqScan`.

**The actual work is index maintenance, not index creation.** Every `INSERT`, `UPDATE`, and `DELETE` must update every index on the table, and `UPDATE` of an indexed column is a delete-then-insert in the index tree. This is where the bugs are.

**Done when:** unique-constraint tests pass; a plan-shape unit test asserts the right access method was chosen (sqllogictest is results-only and cannot see plans, so index selection needs its own tests); and — the real test — **every query in your suite returns identical results with the index present and absent.** That's exactly what the corpus's `index/commute`, `index/between`, and `index/delete` directories exist to check, so those directories become available to you here.

**Expect the pass rate to DROP in this phase, and don't panic.** Many corpus queries are marked `nosort` with no `ORDER BY`, meaning the expected results are pinned to whatever natural row order the generating engine produced. The moment your planner picks an `IndexScan`, natural order changes — index order instead of rowid order — and queries that passed for months start failing on ordering alone while returning a correct row set.

This is the one predictable pass-rate cliff in the entire plan. Three legitimate responses, in order of preference: (a) confirm via the with-index/without-index differential test that the row *sets* still match, so you know it's ordering and not correctness; (b) keep index selection deliberately conservative — only use an index when the predicate genuinely demands it, which is also what SQLite does; (c) accept the drop, document it, and move on. What you must not do is quietly stop running those files to protect the number.

#### Phase 31 — `INNER JOIN`, two tables
Multi-table scope merge, qualified names, ambiguous-column detection. `NestedLoopJoin`. Fixed left-deep order, no reordering.

#### Phase 32 — Join chains, `LEFT`/`RIGHT`/`FULL`, `USING`, comma joins
Outer-join NULL padding. `USING` column de-duplication. Comma-join versus `JOIN` keyword precedence.

#### Phase 33 — Uncorrelated subqueries, `IN (SELECT ...)`, `EXISTS`
Subquery as a nested plan producing a scalar or list, executed once and materialized.
**Trap:** `NOT IN` over a subquery containing NULL collapses to the empty set. Non-obvious, and heavily tested.

#### Phase 34 — Correlated subqueries
Scope chaining — the inner query references the outer row. Naive re-execution per outer tuple is fine.

#### Phase 35 — Hash join
Build and probe on the join key. NULL never matches. Same results as nested loop; purely about not timing out on large inputs.

#### Phase 36 — Set operations
`UNION`, `UNION ALL`, `INTERSECT`, `EXCEPT`. Schema compatibility checks across branches; reuse `Sort` and `Distinct`.

#### Phase 37 — Differential fuzzing against real SQLite
Generate random schemas and queries, run against both your engine and real SQLite, diff results. Catches what sqllogictest structurally cannot: **logic that is self-consistent but wrong.** A canned expected value can be matched by coincidence; an independent oracle can't be fooled.

#### Phase 38 — SQLancer and SQLsmith
SQLancer's PQS, TLP, and NoREC find optimizer and logic bugs on query shapes no static corpus generates. SQLsmith finds panics from legal-but-weird queries — it found 70+ real bugs in Postgres. Both target *your* code paths specifically.

#### Phase 39 — `EXPLAIN`
Print the physical plan tree. You should honestly have hacked a version of this around Phase 5 as a debugging tool; this makes it real. It's also how you demonstrate the engine to anyone.

#### Phase 40 — Benchmarks against real SQLite, published with the losses

**Build:** A benchmark harness measuring your engine and real SQLite on the same workloads, same machine, same data: bulk insert, point lookup by rowid, point lookup by index, full scan, range scan, aggregate over a large table, and a mixed read/write loop. Report throughput and p50/p99 latency. Vary dataset size across at least three orders of magnitude so you can see the shape of the curve, not one point on it.

**Then publish the whole table — including every case where you lose, with an explanation of why.** "SQLite is 12× faster on point lookups because it mmaps the file and I do a `pread` per page" is a sentence that demonstrates more understanding than any benchmark you win.

**Done when:** the table exists in the README, every row has a number, and every loss has a one-sentence cause you actually believe.

**Why this phase exists at all:** it was in the original argument for building this project and the first three drafts of this plan dropped it entirely. Correctness is the point, but a database with no performance numbers is a database whose author never looked. It also catches the bug class that correctness testing structurally cannot see — algorithmic regressions. A cursor that re-seeks from the root on every `Next()` is *perfectly correct* and turns every scan into O(n log n). Only a timing finds that.

**Don't** let this become an optimization project. Measure, explain, publish, move on. Optimizing is a different discipline and it's not what this nine months is for.

---

#### Phase 41 — A `database/sql` driver

**Build:** Implement Go's `database/sql/driver` interfaces over your engine — `Driver`, `Conn`, `Stmt`, `Rows`, `Tx`. Placeholder parameter binding. Proper `Close` semantics and error mapping.

**Done when:** a small standalone Go program that imports your package, opens a database with `sql.Open`, and runs `CREATE`/`INSERT`/`SELECT`/`BEGIN`/`COMMIT` through the standard library works — with no knowledge of your internals.

**Why this is worth a week:** it's the line between "a REPL I built" and "a library someone can actually use." Anyone evaluating your work can try it in five minutes with code they already know how to write. It's also a genuine API design exercise, and it will surface real gaps — statement lifecycle, parameter binding, concurrent connection handling — that a REPL never forced you to think about. Needs transactions, which is why it lands after Phase 25 rather than early.

---

#### Phase 42+ — Optional, in rough value order
WAL mode as an alternate pager backend, unlocking concurrent readers (needs wal-index and checkpointing — a real subsystem). Savepoints. Buffer pool eviction with LRU-K, once a working-set test demands it. External spilling sort. CTEs. Window functions. A VDBE-style bytecode executor as a v2 engine — a genuine rewrite, justified only by curiosity at this point, which is a fine reason once everything above is done.

---

## 6. Where you will want to quit, and what to do

Predicted from your five abandoned repos plus the archaeology on projects that died.

| Phase | The feeling | The move |
|---|---|---|
| 0 | "This is just scaffolding, I want to build the real thing" | The scaffolding *is* the real thing. It's why this will be #6-that-shipped instead of #6-abandoned. Two weeks. |
| 5 | First real corpus number comes back embarrassingly low | It's supposed to be. It's honest, which is more than any previous repo of yours managed. Put it in the README and move. |
| 10 | "No new features, this is boring" | This phase converts three months of work into a number you can defend. Highest value per hour in Track B. |
| 11–14 | "I was writing SQL last month, now I'm counting bytes in a page header" | Phase 15 is four weeks away and it puts your working database on disk. That's the payoff. Look at it. |
| all of C | "The pass rate hasn't moved in two months" | It's not supposed to. Publish the Track C scoreboard instead — torture scale, differential ops, invariants, crash points. Something moves every week. A flat pass rate through a storage rewrite is a **pass**. |
| **17** | **The real one. Weeks in, splits cascading wrong, tests red, no idea why.** | **This is the project. This is the wall every previous repo of yours stopped at. Tiny fanout, deterministic cases, reference-map differential testing. Three weeks of feeling stupid is the correct experience, not a signal to stop.** |
| 20 | "Delete is just insert backwards, I'll skip the redistribute branch" | It isn't, and skipping it silently under-occupies your tree forever. Assert occupancy bounds. |
| 23–24 | "It survives `kill -9`, that's good enough" | It isn't. `kill -9` proves nothing — the page cache survives it. The crash matrix is the most valuable artifact in the project. |
| 27+ | "I've proven the point, this is just more SQL features" | Correct, and that's why it's the easy part. Ride the pass rate. Ship a feature a week. |

Two structural rules, both from projects that finished:

**One repo.** Nothing else gets started until this ships. Not a DST rewrite, not a "quick" experiment.

**Publish the number weekly.** One line, one number. `evidence/: 34%`. Bolt and toydb both survived because the testing seam existed before the hard part. Yours is the pass rate, and making it public makes it load-bearing.

---

## 7. Reference implementations

Read to understand. Then close the tab, wait a day, and write your own from a blank file with your own names. Write your own tests *before* reopening the reference to compare. Temptation to copy is highest with bbolt and build-your-own.org precisely because they're idiomatic Go and structurally identical to what you're building — a split algorithm copy-pastes trivially and you won't notice you did it.

### Tier 1 — worth real time

| Repo | What for | Notes |
|---|---|---|
| **`etcd-io/bbolt` at tag `v1.3.7`** | Page layout, B+tree split/merge, cursor, freelist, single-writer transactions | **Read v1.3.7, not `main`** — later versions split `page.go`/`freelist.go` into `internal/` packages with two competing freelist implementations, purely for performance, zero pedagogical value. Order: `page.go` → `node.go` → `cursor.go` → `freelist.go` → `tx.go` → `db.go`. ~5–6k lines. Best reference for this project. |
| **`build-your-own.org/database`** (book) | Closest existing analog — same language, same shape | Chapters 0–7 free (files → pager → B-tree → B+tree insert/delete → append-only KV → freelist); 8–14 paid (tables, range queries, secondary indexes, transactions, concurrency, SQL parser, execution). ~2,800 lines total. No companion repo — code is inline, you type it. Worth the money. |
| **`chaisql/chai`** (formerly `genjidb/genji`) | A genuinely hand-written recursive-descent SQL parser in Go | `internal/sql/scanner/` → `internal/sql/parser/parser.go` → `parser/expr.go` (precedence) → `parser/select.go`. Confirmed not yacc-generated, unlike every production Go parser. |
| **`dolthub/go-mysql-server`** | Binder/analyzer design only | `ARCHITECTURE.md` at repo root, then `sql/analyzer/rules.go` (the ordered rule list *is* the binder spelled out), then one or two rule files. **Stop there** — 59 packages will drown you. |
| **`uchicago-cs/chidb`** (C, tiny) | What a minimal SQLite-shaped engine looks like whole | `pager.c` (8.6KB) → `btree.c` (14.6KB) → `dbm-cursor.c` → `dbm-ops.c` → `dbm.c`. SQLite's architecture at 1/50th the size. |

### Tier 2 — when you reach that phase

| Repo / resource | Phase |
|---|---|
| CodeCrafters "Build your own SQLite" (9 read-only stages: page size → table names → `COUNT(*)` → columns → `WHERE` → index scan) | Optional 1–2 evening warm-up before Phase 1. Builds file-format confidence fast. Don't spend more. |
| `cockroachdb/pebble` → `vfs/errorfs/errorfs.go` | Phase 11. The only good fault-injection-for-file-I/O code in Go. Adapt the idea, not the code. |
| SQLite docs: `arch.html`, `fileformat.html`, `schematab.html` | Phases 12–16. |
| SQLite docs: `atomiccommit.html` | Phase 23. The rollback journal explained by its author. Mandatory. |
| SQLite docs: `wal.html`, `walformat.html` | Phase 40, if you do WAL. |
| SQLite `datatype3.html` | Phase 10. Mandatory, not optional. |
| `tidwall/buntdb` | Whenever you design the public Go API — cleanest small example of `View`/`Update` closure transactions. |
| `erikgrinaker/toydb` — `docs/architecture/index.md` | Phase 25+, and read `goldenscript` regardless: auto-generated expected output, diffed every run, reviewed by eye. Directly applicable testing discipline. |
| `cmu-db/bustub` headers | Phase 40, if you build real buffer-pool eviction. |
| Berkeley `rookiedb` | Reference for ARIES structure only — a course skeleton with the algorithms stubbed out. |
| CMU 15-445 lectures (YouTube, free) | Throughout. Best structured explanation of everything in Tracks C and D. |
| "Database Internals", Alex Petrov | Part I only. Part II is distributed systems, not this project. |

### Tier 3 — do not read

`cockroachdb/pebble` or `dgraph-io/badger` in full (LSM-shaped; teaches intuitions that actively mislead a B+tree design). `vitessio/vitess`, `pingcap/tidb` (yacc-generated parsers, hundreds of MB; the `.go` you'd read is machine output). `dolthub/dolt` in full (content-addressed prolly trees — a different database). `xwb1989/sqlparser` (unmaintained stale fork).

### Where no good reference exists — you're on your own

- **Rollback-journal-style recovery in Go.** No clean teaching example. You'll translate SQLite's C prose into Go yourself.
- **Cost-based index selection, beginner-friendly, in Go.** Doesn't exist.
- **MVCC on a B+tree** (as opposed to LSM) in a small Go codebase. Doesn't exist.
- **Fault injection for a plain single-file store.** Only Pebble does it well, written against Pebble's own VFS abstraction.

Nothing in open source sits at the exact intersection of: idiomatic modern Go, single-file B+tree, journal or WAL, plus a real SQL layer, readable end to end. That gap is why this project is worth building and worth publishing.

---

## 8. Effort estimates

Wide variance. Estimates, not commitments.

| Track | Phases | Component | Estimate |
|---|---|---|---|
| A | 0 | Skeleton, seams, sqllogictest runner | 1–2 weeks |
| B | 1–2 | Tokenizer, Pratt parser, evaluator | 1–2 weeks |
| B | 3–6 | Catalog, stub storage, binder, `WHERE`, projection | 1–2 weeks |
| B | 7 | `CASE`/`CAST`/scalar functions | 1 week |
| B | 8–10 | `ORDER BY`, mutations, conformance pass | 2–4 weeks |
| C | 11–14 | Fault-injecting file, pager, record codec, slotted page | 3–4 weeks |
| C | 15–16 | Heap file, first swap, schema table | 1–2 weeks |
| C | **17–18** | **B+tree insert/split + cursors** | **4–8 weeks** — hardest single component; that range assumes course scaffolding. Solo, expect the top. |
| C | 19–22 | Second swap, delete/merge, freelist, overflow | 3–4 weeks |
| D | 23–26 | Journal, crash matrix, transactions, fsync pass | 4–6 weeks |
| E | 27–30 | Aggregates, `GROUP BY`, constraints, indexes | 3–5 weeks |
| E | 31–36 | Joins, subqueries, set ops | 4–6 weeks |
| E | 37–39 | Differential fuzzing, SQLancer, `EXPLAIN` | 1–2 weeks |
| E | 40–41 | Benchmarks vs SQLite, `database/sql` driver | 2 weeks |

**Total to a genuinely useful, honestly-tested engine: 6–9 months.** Consistent with how long chidb, toydb, and build-your-own.org actually took their authors.

---

## 9. Rules of engagement

1. **The pass rate is the progress report — except in Track C, where the scoreboard is.** Not commits, not lines, not features. One line, published weekly, always with a number that moved. In Tracks B, D, and E that's the pass rate. In Track C it's torture scale, differential ops, invariants asserted, and crash points, while the pass rate deliberately stays flat.
2. **The number must be honest before it can be useful.** Validated runner (Phase 0), pinned file manifest, `skipped` as its own bucket, both file-count and query-count reported. A number you can inflate is a number that will eventually let you lie to yourself — and that is exactly how `Distributed-Cache` ended up with a 25KB README over a silent data-loss bug.
3. **No README until the number is real.** Across all fifty of your repos the prose is more finished than the code, every single time. That's not a documentation habit — it's how you feel productive without being productive. `Distributed-Cache` has a 25KB README and a silent data-loss bug. Not again.
4. **Test before feature. Always.** You have one test file in fifty repos. Here the test comes first or the feature doesn't get written.
5. **Never violate the two seams.** One direct `os.File` call above Seam 1, or one collection-shaped method on Seam 2, and the plan stops working.
6. **Read references, then close the tab and write your own.** A copied B+tree is a wasted year.
7. **Deferral is a feature.** Freelist, overflow, eviction, savepoints, hash join, WAL — all deliberately deferred, some by nine phases. That's the plan working, not slipping.
8. **When the pass rate drops after a correctness fix, that's correct.** A fix can unmask a bug that formatting was hiding. Don't panic, don't revert.
9. **After three failed days on the same bug, write down what you know and ask.** Don't grind. Grinding is how the last five died.
10. **Stop at the end of a phase, never in the middle of one.** Every phase boundary is a safe place to put the project down for a week and pick it back up. Mid-phase is not.
