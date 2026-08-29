# Go Standards — How this code gets written

Companion to `BUILD_PLAN.md` (what to build) and `TEACHING_PROTOCOL.md` (how to teach it). This file is the engineering standard: **how the Go gets written, and why.**

Written against an audit of the author's five existing Go repos, plus how real Go storage engines are actually built — bbolt, Pebble, badger, buntdb, chai, go-mysql-server, and the Go standard library.

---

## 1. The principle, stated once

**"Use all the Go features" is the wrong target. Use the feature the problem calls for, and know why you rejected the others.**

Go's culture is subtractive. Interfaces you don't need are a liability. Goroutines you didn't have to start are a lifetime someone must reason about. Channels used to take a lock are slower, harder to debug, and untestable under fault injection.

The clearest example, and it matters because it was an explicit request: **this project has almost no channels.** It's a single-writer, synchronous, embedded engine. bbolt — ~5,000 lines, the closest production analog in existence — has essentially none in its storage path. A page fetch returning through a channel instead of a return value would be slower, harder to reason about, and would break the deterministic crash-injection design in Phase 11 outright. Go Code Review Comments says it directly: *prefer synchronous functions.* Go Proverbs says it shorter: **channels orchestrate, mutexes serialize.**

You'll use channels in exactly one place, if at all: coordinating a background checkpointer goroutine, should you build WAL mode in Phase 42+. That's orchestration. Everything else is a mutex or a return value.

---

## 2. What the audit found in your existing Go

This section exists because standards written against generic advice get ignored, and standards written against your actual habits don't.

### Keep doing these — they're genuinely good

**Your typed-error design is the best thing in your repos, and it's the model for this project.** `api-gateway/internal/repository/error.go` — `RepoError{Kind, Op, Entity, Err}` with `Unwrap()` and `Kind`-based `Is()`, plus `classifySQLError` mapping Postgres codes to semantic categories. That is exactly the shape a storage engine's error taxonomy should take. Reuse the idea in §7.

**Correct concurrency primitives when hand-written.** `in-mem-message-queue/internal/queue/queue.go:88` — `sync.Cond` used properly, `Wait()` inside a re-checked loop, `Signal()` after every mutation. Not cargo-culted. `Distributed-Cache/internal/cache/user_cache.go:30-66` — a textbook goroutine lifecycle with `stopCh`/`stoppedCh`/`sync.Once`.

**Defensive value copying is habitual.** `user_cache.go` copies byte slices on every get, set, and snapshot so callers can't alias cache-internal memory. That instinct is directly load-bearing in a page cache — a caller holding a `[]byte` into a page you later evict is a corruption bug.

**Loop-variable capture handled correctly.** `trimmer/BC/services/chapter_adder.go:47` passes `idx, file` as goroutine parameters instead of closing over them.

**Consistent receivers and constructors across all five repos.** Pointer receivers, short names (`b *Breaker`, `uc *UserCache`), `New*` everywhere, no functional-options ceremony, no giant positional constructors. No mixed value/pointer receivers found anywhere. Nothing to fix here.

**You know how to write a good test.** `api-gateway/internal/middleware/admin_guard_test.go` — table-driven, cases named for the security property under test, a `seedContext` helper that builds context the way real middleware would, assertions on both status code and whether `next` was reached. It's genuinely good. Which means four repos with zero tests is a **priority problem, not a skill problem.** That's better news than the alternative.

### Three real bugs, because they show the exact failure modes to design against

**1. `Distributed-Cache/internal/cluster/cluster.go:157` — this function panics every single time it's called.**

```go
cs.mu.RLocker()
defer cs.mu.RUnlock()
```

`RLocker()` returns a `sync.Locker` **value**. It does not acquire anything. The deferred `RUnlock()` then fires on a mutex nobody locked — "RUnlock of unlocked RWMutex," guaranteed panic. `GetReplicaNodes` has never worked. One test calling it once would have caught this.

**2. `Distributed-Cache/internal/cache/user_cache.go:135` — the last-write-wins guarantee doesn't exist.**

The insert-new-key branch of `set()` never assigns `Timestamp: ts`. Every first write of a key stores `Timestamp: 0`, while a comment two lines above says the code "enforces last-write-wins." Since `ts < existing.Timestamp` can't be true against a stored zero, the ordering guarantee the whole replication path depends on silently isn't there.

This is precisely the bug class that will be catastrophic in a SQL engine — write ordering, silently wrong, no crash, no error, correct-looking output.

**3. `Distributed-Cache/internal/cluster/hashring.go:79` — `panic()` on data from the network.** `ReplaceFromSnapshot` panics when `strconv.ParseInt` fails on a hash key, and it's reached from `PollLeader` after decoding JSON from a peer over HTTP. One malformed peer response kills the process.

Contrast with `DST/internal/dst/timesim/clock.go:119`, which panics on a negative duration — an invariant *you* control. That one's correct. The rule is in §7.

### Habits to break

**Swallowed errors at I/O boundaries.** `cache.go:277` drops the return of `RestoreUserFromSnapshot` entirely. `chapter_adder.go:22` has `files, _ := filepath.Glob(...)` and an unchecked `os.MkdirAll`. `ffmpeg/commands.go:67` — `GetVideoCodecAndPixFmt` has no `error` return at all and discards ffprobe failures with `out, _ := RunCmd(...)`. A database cannot do this anywhere.

**`%w` almost never used.** Roughly 1 of 13 `fmt.Errorf` calls in api-gateway, 0 of 22 in trimmer. `RepoError.Unwrap()` exists, so the machinery is there — it just isn't used at call sites, which means `errors.Is`/`errors.As` work only sometimes. Error handling that works sometimes is worse than none, because you'll trust it.

**Interfaces centralized Java-style.** `api-gateway/internal/repository/interfaces.go` — six interfaces, each with exactly one implementation, no test doubles anywhere. Compare `internal/proxy/handler.go:142`, where you declared a 2-method `Registry` interface at the point of use. That one's right. You clearly know how; it just isn't the default. §4 makes it the default.

**Goroutines with no way to stop.** `queue.go:38` starts `go q.requeueExpired()` — an infinite ticker loop — from the constructor, and `InMemoryQueue` has no `Stop` or `Close`. `broker.go:24` creates a new queue, and therefore a new permanent goroutine, for every `tenantID:queueName` it ever sees, and never evicts one. Unbounded leak under normal use.

**No consistent rule about when to fan out.** `services/health.go:525` probes every upstream **sequentially**, so readiness latency is the sum of every timeout rather than the max. `chapter_adder.go:44` does the opposite — one unbounded goroutine per input file, each shelling out to ffmpeg, no semaphore. Under-concurrent where it costs latency, over-concurrent where it costs resources, in the same codebase-author's two repos.

**Documentation describing code that doesn't exist.** `DST/internal/dst/scheduler/event.go:60` documents a `seq` field as "used by the queue below" / "the queue in this same file." The file is 103 lines and has no queue. Neither does the package.

**Comment/code contradiction.** `Distributed-Cache/internal/server/replication.go:106` — "attempt with retries and exponential backoff" sits directly above `time.Sleep(2 * time.Second)`. `attempt` increments and is never used to compute a delay.

**Dead code left in place.** `repository/error.go:12-17` declares six sentinel errors that nothing references, superseded by `RepoError` a few lines down. `broker.go:47` has a commented-out `ExtendLease`. `user_cache.go:112` has commented-out scratch code inside a live function.

### The comment-volume finding, and how it squares with the teaching comments

`api-gateway/internal/server/router.go` is **68% comments** — 1,210 of 1,777 lines. `internal/circuit_breaker/breaker.go` is **86%** — 1,448 of 1,685. `DST/internal/dst/core/ids.go` spends 161 lines documenting three `uint64` wrapper types whose entire logic is `return n != 0` and an `fmt.Sprintf`, explaining "zero means invalid" three separate times. DST's three `doc.go` files total 223 lines explaining that PRNGs depend on sequence position — background you already had, since you were building a DST framework.

You asked for dense teaching comments in this project. That's fine and it's the plan — but the audit is the reason it needs a boundary, so here it is, and it's the whole rule:

**Teaching comments are tagged (`WHY:`, `EG:`, `WITHOUT:`, `TRAP:`), so they are mechanically strippable, and `make strip` must produce a file whose remaining comments read like normal production Go.** If stripping the tags leaves something unreadable, the untagged comments were doing teaching work and belong in a tag. Two artifacts, one file, cleanly separable.

And two hard rules that apply to tagged and untagged comments equally, straight out of the audit:

- **A comment that contradicts the code is worse than no comment.** The "exponential backoff" line is the model failure.
- **Never document code that isn't in the file.** The `event.go` "queue below" line is the model failure.

---

## 3. Where each Go feature earns its place — in this project

| Feature | Verdict here | Where |
|---|---|---|
| **Interfaces** | Sparingly. **Two seams only.** | `fs.FS`/`fs.File` (fault injection), `plan.Node` and `Expression` (genuinely N implementations). See §4. |
| **Concrete structs** | The default for everything. | Pager, btree, freelist, journal, catalog, cursor, scanner, parser. |
| **Pointer receivers** | Nearly always. | Everything stateful. See §5. |
| **Value receivers** | Rare. Small immutable values only. | `PageID`, `RowID`, `Value`. |
| **Channels** | Almost never. | Only a background checkpointer, if WAL mode ever happens (Phase 42+). |
| **Goroutines** | Almost never. Zero in the storage path. | Same as above. |
| **`sync.Mutex` / `RWMutex`** | Yes — the correct tool. | One coarse `RWMutex` over the tree, per plan Decision "also locked, minor". |
| **`sync.Cond`** | Possibly once. | Nothing in the current plan needs it. Don't reach for it. |
| **Generics** | Only for one thing. | Comparator-driven tree keys, if it removes real duplication. Not by default. |
| **`context.Context`** | Public API boundary only, if at all. | Nothing internal can be cancelled, so don't accept a `ctx` you never check. |
| **Embedding** | Rarely, never for reuse. | Never embed `sync.Mutex` — a named field, always, or you export `Lock`/`Unlock` on your public type. |
| **`unsafe`** | No. | See §6. |
| **mmap** | No. | See §6. |
| **Functional options** | No. | An `Options` struct with `DefaultOptions()` — badger's model, simpler to read. |
| **Reflection** | No. | Not once, anywhere. |

---

## 4. Interfaces

**The rule: define an interface where it's consumed, sized to what the consumer calls, and only when a second real implementation or a test double actually exists today.**

Go has no `implements` declaration, so a speculative interface buys you nothing and costs every future caller a layer of indirection. Dave Cheney's name for it is *interface pollution*; Pike's rule is *don't design with interfaces, discover them*. And in a hot path — page fetch, tree descent — an interface call can't be inlined unless the concrete type is statically known, and values boxed into an interface commonly escape to the heap. In a B+tree traversal loop that's a real allocation tax, not a style preference.

**Evidence from the real thing:** bbolt's entire engine has **zero interfaces**. `DB`, `Tx`, `Bucket`, `Cursor`, `node`, `Page` are all concrete. Its freelist has *two* implementations — `internal/freelist/array.go` and `hashmap.go` — selected by a `FreelistType string` field and a factory function, not by polymorphism. chai's `internal/database/table.go` is `type Table struct`, concrete, in a real Go SQL engine. Pebble, meanwhile, defines real interfaces in exactly one place that matters to us: `vfs/vfs.go`'s `FS` and `File` — and notably `vfs` is the one package Pebble deliberately keeps *out* of `internal/`, because it's the stable seam.

### The two interfaces this project gets

**1. `fs.FS` / `fs.File`** — Seam 1 from the plan. Three implementations exist by Phase 11: real OS file, in-memory, fault-injecting. This is the most justified interface in the project and it's why crash testing is possible at all. Model it on `pebble/vfs/vfs.go` and `pebble/vfs/errorfs/errorfs.go`.

**2. `plan.Node` and `plan.Expression`** — genuinely N implementations (SeqScan, IndexScan, Filter, Project, Sort, Limit, Aggregate, NestedLoopJoin, HashJoin…), all walked polymorphically. This is go-mysql-server's model and it's the right one. `Next() (Row, error)` plus `Close() error` plus a schema accessor. Three methods, maybe four.

### Seam 2 is a judgment call, and here's the call

`BUILD_PLAN.md` Decision #3 requires a cursor-shaped storage interface so the in-memory stub, heap file, and B+tree can swap. That's three implementations, two of which survive as permanent test oracles — so it clears the bar. **Keep it, and keep it small:** open a cursor, seek, next, insert, update, delete. Not a method more. The moment it grows a convenience method the B+tree can't honestly implement, the plan breaks.

### Mechanics

- Assert satisfaction at compile time: `var _ fs.File = (*osFile)(nil)`. You currently do this nowhere. Start.
- Accept interfaces, return concrete structs.
- Never a `Storage`/`Manager`/`Service` interface with one implementation. If `internal/repository/interfaces.go` appears in this project, something went wrong.

---

## 5. Receivers, structs, errors

### Receivers

**If in doubt, pointer.** Pointer when the method mutates, when the struct holds something that must not be copied (a mutex, a `noCopy`), or when it's large. A stray value receiver fails *silently* — it mutates a copy and the write vanishes. In a page buffer or a tree node, that's a data-corruption bug with no stack trace.

Never mix receiver kinds on one type. Short, consistent names. Never `this` or `self`. You already do all of this correctly — no change.

### Structs

- **Make the zero value useful** where you can, `bytes.Buffer`-style. Constructor only when zero is genuinely invalid (needs a file handle, needs a page size).
- **Field order matters here.** The compiler pads for alignment; ordering fields by descending size (pointers/int64 → int32 → int16 → bool/byte) can shrink a struct materially. For a page header or a WAL record this isn't micro-optimization, it directly sets per-page footprint. Enable the `fieldalignment` analyzer.
- **Never embed `sync.Mutex`** — a named `mu` field, always, or you've exported `Lock`/`Unlock` on your public API.
- **No functional options.** An `Options` struct with `DefaultOptions()` and chainable `With*` returning copies, badger's model. Fewer moving parts, reads better.

### Errors — the decision tree

1. Caller doesn't need to distinguish it → `fmt.Errorf` with `%w`, return.
2. Caller matches by identity → **sentinel** `var ErrPageNotFound = errors.New("...")`, matched with `errors.Is`.
3. Caller needs *data* out of it — page number, offset, key → **typed error**, matched with `errors.As`. This is where your `RepoError{Kind, Op, Entity, Err}` design goes, and it's the right design. `StorageError{Kind, Op, PageID, Err}`.
4. Adding context on the way up → **always `%w`**: `fmt.Errorf("btree: split page %d: %w", pg, err)`. Never `%v` unless you deliberately want to hide the cause.
5. Lowercase, no trailing punctuation.
6. **Handle exactly once.** Either handle it or wrap-and-return. Never log-and-return. Never `_ =`.
7. **`panic` only for invariants you own.** Corrupt internal state that should be impossible: panic. Anything derived from disk bytes, a journal file, a peer, or user SQL: return an error. The `hashring.go` panic is the failure model; the `clock.go` panic is the correct model.
8. **Centralize corruption detection.** bbolt puts it in one place — `Meta.Validate()` returns `ErrInvalid` / `ErrVersionMismatch` / `ErrChecksum`. Do the same: one function that decides a page is untrustworthy, not scattered checks.

One `errors.go` per package with the sentinels. Don't build a hierarchy.

---

## 6. On-disk structures: `encoding/binary`, not `unsafe`

bbolt casts structs directly onto mmap'd bytes and provides `UnsafeAdd`/`UnsafeIndex`/`UnsafeByteSlice` to walk cells with zero copies. It's fast, and it's correct because it's been beaten on for a decade.

**You will not do this.** Explicit `encoding/binary` encode/decode over `os.File.ReadAt`/`WriteAt`. Reasons, in order:

1. **Memory safety.** An unsafe cast bug is undefined behavior that manifests as corrupted data three phases later, with no stack trace pointing at the cause.
2. **mmap fights the design.** `BUILD_PLAN.md` Decision #8 is a rollback journal, which needs precise control over write and fsync ordering. mmap hands that to the kernel.
3. **Platform explosion.** bbolt carries twelve arch-specific files (`bolt_amd64.go`, `bolt_arm.go`, `bolt_386.go`, `bolt_loong64.go`, …) purely to make mmap portable. None of that is your project.
4. **Phase 11 needs every byte to pass through the file interface.** mmap bypasses it, and crash injection stops working.

Little-endian, page-relative offsets, explicit varints. Slower and you will never notice.

---

## 7. Package layout

`golang-standards/project-layout` has 55k stars and is **not** an official standard. Russ Cox opened issue #117 on that repo to say so, objecting that the org name implies endorsement it doesn't have and that the layout — especially its ubiquitous `pkg/` — doesn't reflect how real Go repos are built. Popularity, not authority. Ignore it. **No `pkg/` directory.**

What bbolt and Pebble actually do: public API in the **root package**, engine guts under `internal/`, a thin `cmd/`. The one asymmetry worth copying is Pebble's: the filesystem seam lives *outside* `internal/`, because it's the stable, intentionally-public boundary.

```
mydb/
  db.go tx.go errors.go doc.go     # public API — flat root, bbolt-style
  fs/                              # File/FS interfaces + os, mem, fault impls
                                   #   deliberately NOT internal — this is the stable seam
  internal/
    pager/                         # page cache, allocation, page codec
    freelist/                      # free page tracking
    journal/                       # rollback journal + recovery
    btree/                         # node, split, merge, cursor
    record/                        # row encode/decode, varints
    catalog/                       # schema table, in-memory catalog
    sql/
      scanner/                     # tokenizer
      parser/                      # recursive descent + Pratt
      ast/                         # parsed tree
      bound/                       # bound tree (plan Decision #9)
      plan/                        # Node + Expression interfaces, plan structs
      planner/                     # binder + rule passes
      exec/                        # Volcano operators
  driver/                          # database/sql/driver adapter (Phase 41), isolated
  cmd/mydb/                        # the REPL
  testdata/                        # golden files, .slt manifest
```

Package naming: short, lowercase, no `util`, no `common`, no `helpers`, no `models`. No stutter — `pager.New`, never `pager.NewPager`. Your existing package boundaries in api-gateway are genuinely good and non-leaky; keep that instinct.

**Do not** copy Pebble's ~50 `internal/` micro-packages. Right for an LSM engine, wrong scale model for 20k lines.

---

## 8. Public API design

bbolt exposes **both**, and so will you:

- `db.Update(func(tx *Tx) error)` and `db.View(...)` — closure style, the idiomatic Go surface, panic-safe commit/rollback, the primary path in the docs.
- `db.Begin(writable bool) (*Tx, error)` with `Commit()`/`Rollback()` — public underneath. `Update`/`View` are thin wrappers over it.

Not a stylistic hedge. `driver.Tx` in the standard library is **exactly** `{Commit() error; Rollback() error}` — no closures anywhere. Phase 41's `database/sql` driver has to be built on the explicit layer, so keeping it public isn't optional; it's a prerequisite.

Naming, grounded in what real engines call these things: `DB`, `Tx`, `Cursor`, `Pager`, `freelist`, `meta`, `journal`, `plan.Node`. Not `Transaction`, not `Iterator`, not `DatabaseManager`.

---

## 9. Testing

Four repos with zero tests, and the one test you wrote is good. So this is about discipline, not technique.

- **Table-driven by default.** One struct per case with a name, `t.Run(tc.name, ...)` so failures isolate and re-run with `-run`.
- **`t.Helper()`** in every shared assertion or setup function, or failures point at the helper instead of the case.
- **Plain `if got != want`** for scalars; `google/go-cmp`'s `cmp.Diff` for structures. No testify — the community is genuinely split and for a systems library the smaller dependency graph and the fact that a failed assertion doesn't abort the whole table both win.
- **Property tests where the plan calls for them.** `pgregory.net/rapid` — generics-based with automatic shrinking, the closest thing Go has to QuickCheck. Warranted for record round-trip, B+tree invariants, and journal replay idempotence. bbolt uses stdlib `testing/quick` for exactly this in `quick_test.go`.
- **An invariant checker as real code, not test code.** bbolt has `Tx.Check()` in `tx_check.go` — a standalone walker that validates the whole tree. Build the equivalent early and call it after every mutation in tests. This is the single highest-leverage test artifact in a B+tree project.
- **Golden files** for scanner → tokens, parser → AST, planner → plan tree. Pebble's `testdata/` + datadriven pattern. toydb's `goldenscript` approach — generate expected output, eyeball it once, commit it, diff forever — is worth copying wholesale.
- **Fault injection from day one**, per plan Phase 11. Model: `pebble/vfs/errorfs/errorfs.go`.
- **`go test -race` unconditionally**, every commit.
- Colocate `_test.go` beside the code. A `internal/testutil` package only once duplication actually appears — bbolt grew `internal/btesting/` that way, after the fact.

---

## 10. Tooling

Small enough that you never argue with it. A linter you routinely suppress stops being read.

```
gofumpt          # stricter gofmt superset, no debate, just run it
go vet
golangci-lint    # staticcheck, errcheck (check-blank: true), ineffassign,
                 # unused, gosimple, revive, fieldalignment
go test -race    # every commit
```

`errcheck` with `check-blank: true` is deliberately aimed at your `_ = err` habit — it makes those visible instead of silent.

`go build -gcflags=-m` to check escape analysis on hot paths (page fetch, tree descent) — a targeted diagnostic when you're curious, never a CI gate.

---

# PART TWO — DESIGN

Everything above is mechanics. This part is design: how to get readable, extensible, maintainable Go, given that Go pursues those goals through different machinery than the OOP tradition you learned them in.

**The thesis, and it's evidence-backed:** Go doesn't reject SOLID, DDD, or LLD. It **relocates the mechanism** — usually down from the type/interface layer to the **package** and the **compiler's import graph**. Applying the OOP versions literally produces measurably worse Go, and your `repository/interfaces.go` is what that looks like.

---

## 11. SOLID, honestly translated

| Principle | Verdict in Go |
|---|---|
| **S**ingle Responsibility | Translates — but at the **package**, not the type |
| **O**pen/Closed | Translates differently — first-class functions, not inheritance |
| **L**iskov Substitution | Translates differently — behavioral contract, no type hierarchy to violate |
| **I**nterface Segregation | Translates **strongly**, and Go pushes it further than Java |
| **D**ependency Inversion | **Dangerous.** Literal translation produces interface pollution |

Canonical treatment: Dave Cheney, [SOLID Go Design](https://dave.cheney.net/2016/08/20/solid-go-design).

**SRP is about packages.** `net/http` does one thing. `encoding/json` does one thing. A `util` or `common` package violates SRP by accretion. Go's unit of reuse and of change is the package, not the type — so when you're asking "does this have one responsibility," ask it about the package.

**OCP has no inheritance to lean on**, and there's a subtlety Cheney flags that matters: **embedded methods dispatch on the embedded type as receiver, not the outer type.** `car.Start()` compiles to `car.Engine.Start()`. So if `Start` needs to see `Car`-level state, embedding silently cannot do it. Embedding is not substitutable extension. The actual extension mechanisms in Go are **function types** and **wrapping/middleware chains**.

**LSP** has no nominal hierarchy to violate, so what's left is the behavioral contract at the interface boundary — Jim Weirich's formulation, which Cheney cites: *require no more, promise no less.* `io.Reader` is the proof that structural substitutability works with no inheritance at all: dozens of unrelated types interchangeable, none of them declaring a relationship.

**ISP translates strongest, and Go pushes further than Java can.** Because satisfaction is implicit, you can define a one-method interface **after the fact, at the call site, with zero coordination with the implementer.** Java's explicit `implements` makes late narrow interfaces expensive, so Java drifts toward broad, early-committed interfaces. Go doesn't have to. Pike's proverb is the cultural default: *the bigger the interface, the weaker the abstraction.*

**DIP is the one that hurts you.** In Java, DIP means "depend on an interface, inject the implementation," and interfaces everywhere is correct. Cheney reframes it for Go as an **import-graph property**: push concrete detail toward `main`, keep leaf packages dependency-free. Not "put an interface in front of every collaborator."

Go Code Review Comments states the rule outright, and it's worth reading twice because it describes your existing code exactly:

> *Do not define interfaces before they are used: without a realistic example of usage, it is too difficult to see whether an interface is even necessary.*

So the Go answers to "how do I decouple and stay testable without an interface for everything":

1. **Consumer-defined interfaces at the point of use** — the package that needs behavior declares the interface it needs, sized to what it calls.
2. **Function types instead of single-method interfaces** — `http.HandlerFunc` is the model.
3. **Dependencies as explicit arguments.** Mat Ryer's practice evolved *away* from dependency-holding struct fields over 13 years of production Go: *"If a handler function wants a dependency, it can bloody well ask for it as an argument."*

---

## 12. DDD, Clean Architecture, and the repository trap

**Transfers cleanly:**
- **Ubiquitous language** — pure naming discipline, language-agnostic, and genuinely valuable. `Pager`, `freelist`, `meta`, `journal`, `Cursor` *is* your ubiquitous language. Use the domain's real words.
- **Bounded contexts → packages**, and Go gives this teeth Java doesn't have: crossing a boundary wrongly is a **compile error**, not a lint warning. See §13.
- **Entities and value objects → structs**, with immutability by convention (unexported fields plus a constructor). Go's zero-value-useful design changes the calculus versus Java's null-fearing constructors.

**Transfers as a modeling idea, with no enforcement primitive:** aggregates. Unexported fields and package boundaries are all you get, and they're enough.

**The repository pattern — the contested one, stated plainly.** Three Dots Labs argues for domain-package repository interfaces. Their *own* retrospective, "Is Clean Architecture Overengineering?", then names the top concrete pitfall as **"creating an interface for every struct, even with single implementations."**

Verdict: **repository-as-package-boundary is good. Repository-as-interface needs a second implementation or a real test double.** Your `repository/interfaces.go` — six interfaces, six implementations, zero doubles — is precisely the pattern the official Go review guidance tells you not to write. Not a style disagreement; the documented anti-pattern.

**Clean Architecture / Hexagonal — the honest debate.** Proponents: Ben Johnson's "Standard Package Layout," Three Dots Labs' Wild Workouts. Critics: Three Dots Labs themselves, plus a broad community position that Go's unit of layering is the package and the function, not the class and the DI container, so a literal port adds ceremony Go's own mechanisms already handle.

The defensible middle, in their words: *"if you are using some architecture and it's slowing you down, it's probably the bad one."* Keep the dependency-direction principle. Introduce an interface only at a genuine substitution point. Let layer count scale with actual complexity instead of being applied uniformly.

**One correction to the research worth flagging, because it protects you from over-applying good advice.** Ben Johnson's scheme — domain types and interfaces in a dependency-free root package, sub-packages per *dependency* (`postgres/`, `http/`, `redis/`), a `mock/` package, wiring only in `main` — is designed for **applications with swappable adapters.** It's excellent for that.

Note what it isn't designed for: **Johnson wrote BoltDB, and BoltDB does not follow Johnson's application layout.** bbolt is a flat root package of concrete types with zero interfaces. Same author, different problem — a library with one implementation of everything is not an application with interchangeable backends. You're building the second kind of thing. Take Johnson's dependency-direction principle; don't take his adapter-per-package structure.

---

## 13. Where dependency direction actually comes from in Go

Not from interfaces. From the **compiler-enforced import DAG.**

Go statically forbids import cycles. This is a hard build error. Java needs ArchUnit or a review process to catch layering violations after the fact; Go catches them before your code compiles. That is a real architectural tool and it's free.

Bill Kennedy's Package Oriented Design operationalizes it: **packages at the same level of the tree must not import each other.** A package that imports nothing is a leaf, and everything depends toward it. That gets you DIP's *outcome* — high-level code independent of low-level detail — from graph shape alone, leaving interfaces for actual variation points.

For this project: if `btree` needs something from `journal` and `journal` needs something from `btree`, the compiler is telling you a boundary is wrong. Three fixes, in order of preference:

1. **Extract a third package** both depend on — shared types, shared errors.
2. **Invert with a consumer-defined interface** — `btree` declares the small interface it needs, `journal` satisfies it structurally, and `btree` stops importing `journal`.
3. **Pass a function type** — hand the behavior in instead of importing the package that implements it.

The fix to avoid: merging the two packages to silence the compiler. That's the signal to re-scope, not to concatenate.

---

## 14. Extensibility without inheritance

Ranked by preference, most to least:

1. **Consumer-defined interface satisfaction** — the default when behavior has state and multiple methods.
2. **Explicit delegation to a field** — the struct holds `fs fs.FS` and calls `s.fs.Open(...)`. Verbose, and the verbosity is the point: intent is unambiguous, no method-set surprises.
3. **Function-type fields (Strategy)** — `type CompareFunc func(a, b []byte) int` as a field. Cheap, no interface ceremony, ideal for single-behavior injection: comparators, key encoders, cost estimators, filter predicates.
4. **Struct embedding** — only for a genuine "is-mostly-a" relationship. Three failure modes: promoted methods run with the **embedded** type as receiver so they can't see outer state; mixing value and pointer receivers across the embed breaks interface satisfaction unpredictably; and **every promoted method silently becomes part of your exported API.**
5. **Wrapping / decorating** — hold another value of the same interface and add behavior around the calls. Correct for cross-cutting concerns. This is how `fs/faultfs` wraps `fs/osfile` in Phase 11 — it's the pattern Pebble's `vfs/errorfs` uses.

**Template Method has no Go equivalent.** Embedding for defaults plus explicit function-type fields for hooks is the replacement.

---

## 15. Functions vs interfaces, and the iterator question

**The rule: if all you need is a function, use a function.** `sort.Slice`'s `less func(i, j int) bool`, `filepath.WalkFunc`, `http.HandlerFunc` all exist because a single stateless behavior doesn't need a named type with a method to carry it.

Reach for an interface when the behavior will plausibly grow a second method, when one value must satisfy **multiple** interfaces at once (trivial for a struct, awkward for a closure), or when callers will implement a type rather than supply a function.

**For this project:** comparators, key encoders, and filter predicates in operators are **function types**. `fs.FS`, `fs.File`, `Cursor`, and `plan.Node` are **interfaces** — state plus multiple methods.

### Iterators — a real decision, and the answer is not the new thing

Go 1.23 added range-over-func: `iter.Seq[V]`, `iter.Seq2[K,V]`. Tempting, and wrong for the core of this engine, for a documented reason: **range-over-func iterators cannot propagate an error out of the loop.** A mid-iteration disk failure has nowhere to go. The community workaround is returning `(iter.Seq[V], func() error)` — iterate, then ask separately what went wrong.

Note what the standard library does: **`database/sql.Rows` still uses explicit `Next()` / `Err()` / `Close()`.** Seq2 wrappers get built *on top of* it, not instead of it.

And for Volcano operators the mismatch is worse. Volcano is **pull**: a parent asks its child for exactly one row and controls the loop. Range-over-func is **push**: the callee drives and calls `yield`. That inverts the operator-tree relationship for no benefit.

**Decision:**
- Storage `Cursor`: explicit `Next() bool`, `Err() error`, `Close() error`. You need mid-scan error surfacing, buffer reuse control, and explicit resource release.
- Volcano operators: `Next() (Row, error)` pull model, per `BUILD_PLAN.md` Decision #4.
- `iter.Seq`: allowed as a **convenience wrapper at the public API boundary only** — `for row := range cursor.Seq() { ... }` built on top of the real cursor. Never the internal contract.

---

## 16. Craft: readability at the line and file level

**Line of sight** (Mat Ryer's term): keep the happy path unindented at the left edge, and push every guard and error case into an early `return`. A reader should follow control flow by scanning one vertical column, not by holding nested `if` blocks in their head. This single habit does more for readability than any amount of commenting.

**Naming — the actual rule** (Andrew Gerrand, "What's in a name?"): **length proportional to scope size, inversely proportional to use frequency.** `i` in a three-line loop. `numRetries` at package scope. Short names are not laziness, they're scope signaling.

Also: no `Get` prefix on getters (`Count()`, not `GetCount()`). Single-method interfaces take `-er`. **No stutter** — `storage.Storage` is wrong, `storage.Engine` is right; `pager.New`, not `pager.NewPager`.

**Functions:** no hard length limit, but prefer multiple return values for unrelated data and a struct only when the fields are cohesive and grow together. **Named returns are discouraged** except for same-typed returns needing disambiguation, or when a deferred closure must mutate the result. Naked returns only in genuinely small functions.

**File-per-concept, not file-per-layer.** `btree.go`, `cursor.go`, `journal.go` beat `types.go` + `interfaces.go` + `impl.go`. Someone looking for cursor behavior should find one file, not three. **This is the direct replacement for the `repository/interfaces.go` habit** — the interface lives in the file of the code that consumes it.

**Keeping 20k lines navigable:**
- Interfaces live where they're consumed, which also prevents package-name stutter.
- Tests beside the code. A black-box `package storage_test` for anything testing only the public API.
- `internal/` earns its place the moment code is an implementation detail shared by two or more of your own sub-packages — it's compiler-enforced "not part of the contract."
- `doc.go` only when no single file is the obvious home for package docs, and it holds nothing but the doc comment and the `package` clause.

---

## 17. What makes Go code age badly

The rot list, with the fix. Several of these already appear in your repos.

| Rot | Fix |
|---|---|
| `any` / `interface{}` leaking through public APIs | Concrete types or generics at the boundary; `any` only for genuinely type-erased internals |
| **Package-level mutable state** | Encapsulate in a struct instance. `trimmer/BC/models/types.go:73`'s global `ProgressState` is the example to not repeat |
| `init()` side effects | Explicit `New()` / `Open()`. Importing a package must never do anything |
| Over-broad, producer-defined interfaces | Small, consumer-defined |
| **Goroutines with no lifecycle owner** | Every goroutine's caller knows how it stops. `queue.go:38` starting a ticker loop from a constructor with no `Close` is the example |
| `context.Context` threaded as a habit | Cancellation and deadlines only. Don't accept a `ctx` you never check |
| Premature abstraction | Wait for the second implementation |
| `util` / `common` / `helpers` packages | Split by domain concept. *A little copying is better than a little dependency* |
| **Dead code kept "just in case"** | Delete it. Six unused sentinels in `repository/error.go`, a commented-out `ExtendLease` in `broker.go`, scratch code inside `user_cache.go:112` |

---

## 18. The translation table

If you learned it the OOP way, this is the Go equivalent. This table is the short version of Part Two.

| You learned (Java / DDD / LLD) | In Go, do this instead |
|---|---|
| One interface per class | Concrete types; interface only at the consumer, only when needed |
| Interface + single impl "for testability" | Don't. No interface until a second impl or a real fake exists |
| DI container / framework wiring | Explicit construction, wired in `main` |
| Abstract Factory, factory hierarchies | A `New*` function returning a concrete struct |
| Singleton | An explicit value, passed in. Not a package var |
| Inheritance for reuse | Composition: a named field and explicit delegation |
| Template Method with overrides | Embedding for defaults + function-type fields for hooks |
| Builder with chained setters | A struct literal, or `Options` + `DefaultOptions()` |
| Repository interface per aggregate | Package boundary for storage; interface only if genuinely swappable |
| Mandatory controller/service/repo folders | Package boundaries + import direction. Layer count scales with real complexity |
| SRP at class level | SRP at **package** level |
| DIP via interfaces everywhere | DIP via **import-graph shape**; `internal/`; leaf packages import nothing |
| Exceptions for control flow | Explicit `(T, error)` |
| Visitor pattern by default for trees | **Type switch in one recursive `walk`.** See below |
| Iterator objects | Explicit `Next()`/`Err()`/`Close()` cursor here; `iter.Seq` only as a public convenience wrapper |
| Getters and setters | Exported fields, or `Count()` with no `Get` |
| `IFoo` / `FooImpl` naming | `Foo` interface, concrete type named for what it *is* (`osFile`, `memFile`) |

### Tree walking, specifically — you have two trees

The AST (Phase 2+) and the plan tree (Phase 5+) both need walking, and Visitor is the one GoF pattern the standard library ships as-is: `ast.Visitor` is literally `Visit(node Node) Visitor`, driven by `ast.Walk`.

But `go/ast` also offers `Inspect` — a plain `func(Node) bool` callback — for the common case, and Go 1.23+ added `ast.Preorder` for simple read-only walks. The stdlib reaches for full Visitor only where *external* packages must plug in traversal behavior it doesn't control.

**Your node set is closed and package-owned, so: a type switch inside one recursive `walk` function.** Least ceremony, most readable, and you don't need the openness the interface buys. Promote to a `Visitor` interface only if something outside the package genuinely needs to inject traversal — which, in this project, it won't.

---

## 19. The rules

1. **No interface until a second real implementation or a test double exists.** Two seams in this project, plus `plan.Node`. That's it.
2. **Accept interfaces, return concrete structs.**
3. **Pointer receiver unless you can say why not. Never mix kinds on one type.**
4. **The public API is synchronous. Zero goroutines the caller didn't ask for.** Any goroutine you do start has a documented way to stop, and a `Close`.
5. **Mutex serializes, channels orchestrate.** No channel where a return value works.
6. **Every error handled exactly once. `%w` always. `errors.Is`/`errors.As` at the match site.** No `_ = err`, ever.
7. **`panic` only for invariants you own.** Never on bytes from disk, a journal, or user SQL.
8. **`encoding/binary`, `ReadAt`/`WriteAt`. No `unsafe`, no mmap, no reflection.**
9. **Never violate the two seams.** One direct `os.File` call above `fs.FS` and Phase 11 stops working.
10. **Build only what the phase says.** No hooks for deferred features. Comment that the simple version is temporary and name the phase that replaces it.
11. **Test with the code, never after.** Table-driven, `-race`, invariant checker called after every mutation.
12. **`make strip` must leave readable production Go.** Teaching comments are tagged and separable. A comment that contradicts the code, or documents code that isn't there, is a defect.
13. **Delete dead code immediately.** No commented-out functions, no unused sentinels, no scratch code left in a live function.
14. **`gofumpt`, `go vet`, `golangci-lint`, `go test -race` — every commit, no bikeshedding.**

Design rules, from Part Two:

15. **SRP applies to the package, DIP applies to the import graph.** If `A` and `B` import each other, a boundary is wrong — extract a third package or invert with a consumer-defined interface. Never merge to silence the compiler.
16. **No `interfaces.go` file, ever.** The interface lives in the file of the code that consumes it. File-per-concept, never file-per-layer.
17. **Keep the happy path at the left margin.** Guards and errors early-return. Line of sight beats commenting.
18. **Name length is proportional to scope.** `i` in a loop, `numRetries` at package scope. No `Get` prefix, no stutter — `pager.New`, not `pager.NewPager`.
19. **Function type before single-method interface.** Comparators, encoders, predicates are `func`. Only state-plus-multiple-methods earns an interface.
20. **Explicit cursors, not `iter.Seq`, for anything that can fail mid-iteration.** Range-over-func can't propagate an error out of the loop, and Volcano is pull, not push. `iter.Seq` is a public convenience wrapper at most.
21. **Type switch in one recursive `walk` for the AST and plan tree.** Visitor interface only if something outside the package must inject traversal — it won't.
22. **No package-level mutable state, no `init()` side effects, no `util` package.**
