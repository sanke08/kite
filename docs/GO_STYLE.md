# Go Style — Production Go, and why

**Applies to any Go project.** Companion to `LEARNING_PROTOCOL.md` (how to teach) — this file is how the Go gets written.

Grounded in: Effective Go, Go Code Review Comments, the Google and Uber style guides, Go Proverbs, Dave Cheney's writing, and how real Go codebases are actually built (bbolt, Pebble, badger, buntdb, the standard library).

---

## 1. The principle

**"Use all the Go features" is the wrong target. Use the feature the problem calls for, and know why you rejected the others.**

Go's culture is subtractive. An interface you don't need is a liability. A goroutine you didn't have to start is a lifetime someone must reason about. A channel used to take a lock is slower, harder to debug, and harder to test.

Readable, extensible, maintainable are the right goals. Go reaches them through **the package** and **the compiler's import graph**, not through type hierarchies. Part Two covers that.

---

## 2. Personal habits — keep these

Measured from an audit of five real Go repos. Genuinely good; don't change them.

- **Consistent receivers and constructors.** Pointer receivers, short names (`b *Breaker`, `uc *UserCache`), `New*` everywhere, no functional-options ceremony, no giant positional constructors. No mixed value/pointer receivers found anywhere.
- **Typed errors done right.** `RepoError{Kind, Op, Entity, Err}` with `Unwrap()` and a `Kind`-based `Is()`, plus a classifier mapping driver error codes to semantic categories. That's the right shape for a domain error taxonomy. Reuse the pattern.
- **Correct concurrency primitives when hand-written.** `sync.Cond` used properly — `Wait()` inside a re-checked loop, `Signal()` after every mutation. A textbook goroutine lifecycle with `stopCh`/`stoppedCh`/`sync.Once`.
- **Defensive value copying.** Copying byte slices on the way in and out so callers can't alias internal memory.
- **Loop-variable capture handled correctly** — passing `idx, item` as goroutine parameters.
- **Meaningful package boundaries and correct `internal/` use.** Real layering that doesn't leak.
- **Genuinely good tests when they get written.** Table-driven, cases named for the property under test, helpers that build state the way real callers would.

## 3. Personal habits — break these

Each is cited from real code and each has bitten.

**Swallowed errors at I/O boundaries.** Dropped return values from restore functions; `files, _ := filepath.Glob(...)`; an unchecked `os.MkdirAll`; a function with no `error` return at all that discards a subprocess failure with `out, _ := RunCmd(...)`. **Every `_ =` must be a visible, deliberate decision.**

**`%w` almost never used** — roughly 1 in 13 `fmt.Errorf` calls in the largest repo, 0 in 22 in another. The `Unwrap()` machinery existed and the call sites didn't use it, so `errors.Is`/`errors.As` worked only sometimes. **Error handling that works sometimes is worse than none, because you'll trust it.**

**Interfaces centralized Java-style.** An `interfaces.go` with six interfaces, each with exactly one implementation, no test doubles. Elsewhere in the same repo: a two-method interface declared at the point of use. The second one is right. See §5.

**Goroutines with no way to stop.** An infinite ticker loop started from a constructor on a type with no `Close`. A factory creating a new such goroutine per key it ever sees, never evicting one. Unbounded leak under normal use.

**`panic()` on data from outside the process.** A `strconv.ParseInt` failure panicking, reached from a handler that decoded JSON from a network peer. One malformed message kills the process. Contrast a panic on a negative duration passed to an internal clock — that one's correct. See §7.

**Two real bugs the audit surfaced, as pattern reminders:**

`mu.RLocker()` where `mu.RLock()` was meant. `RLocker()` returns a `sync.Locker` **value** — it acquires nothing. The deferred `RUnlock()` then fires on an unlocked mutex and panics, every call, guaranteed. One test calling it once would have caught it.

A last-write-wins guarantee that didn't exist: the insert-new-key branch never assigned the timestamp field, so every first write stored zero, while a comment two lines up claimed the code enforced ordering. No crash, no error, correct-looking output, wrong semantics. **That's the class of bug to design against.**

**No consistent rule about when to fan out.** One repo probes every upstream sequentially, so latency is the sum of every timeout instead of the max. Another spawns one unbounded goroutine per input file, each shelling out to a subprocess, no semaphore. Under-concurrent where it costs latency, over-concurrent where it costs resources.

**Dead code kept in place.** Unused sentinel errors superseded by a later design. A commented-out method. Scratch code inside a live function. Delete it.

**Comment volume.** One file 68% comments (1,210 of 1,777 lines), another 86%. 161 lines documenting three `uint64` wrapper types whose entire logic is `return n != 0`, explaining the same fact three times. See the strip rule in `LEARNING_PROTOCOL.md` §4.

---

## 4. Choosing a feature

| Feature | Default | Reach for it when |
|---|---|---|
| Concrete struct | **Yes, always start here** | — |
| Interface | No | A second real implementation or a test double exists *today* |
| Pointer receiver | Yes | — |
| Value receiver | No | Small, immutable, copyable value type |
| `sync.Mutex` / `RWMutex` | Yes, for shared state | Protecting state. `RWMutex` only when reads dominate and the critical section is cheap |
| Channel | No | Orchestrating genuinely independent flows: fan-out, pipeline, producer/consumer, cancellation |
| Goroutine | No | Real concurrent work — and it has an owner who knows how it stops |
| Generics | No | You're about to write the same logic twice for different types |
| `context.Context` | No | Real cancellation or deadline that something actually checks |
| Embedding | No | A true "is-mostly-a", accepting the exported-API widening |
| Functional options | No | 3+ optional parameters and an option set expected to grow across versions |
| `unsafe` | Never | — |
| Reflection | Never | — |
| `any` in a public API | Never | — |

**The concurrency rule, stated properly:** *channels orchestrate, mutexes serialize* (Go Proverbs). A mutex protecting shared state is simpler, cheaper, and more obviously correct than modeling mutual exclusion as message-passing. A channel used to take a lock is a mistake. A channel coordinating a background worker, a pipeline stage, or a fan-out is correct.

**And a library API stays synchronous.** Go Code Review Comments says it directly — *prefer synchronous functions*. Every goroutine you spawn on the caller's behalf is a lifetime they must reason about, a leak risk if `Close` isn't called, and surprise latency. Let the caller add concurrency; they know their workload.

---

## 5. Interfaces

**The rule: define an interface where it's consumed, sized to what the consumer calls, and only when a second real implementation or a test double exists today.**

Go has no `implements` declaration, so a speculative interface buys nothing and costs every future caller a layer of indirection. Its name is **interface pollution**. In a hot path it's also real cost: an interface call can't be inlined unless the concrete type is statically known, and values boxed into interfaces commonly escape to the heap.

Go Code Review Comments, verbatim:

> *Go interfaces generally belong in the package that uses values of the interface type, not the package that implements those values.*
>
> *Do not define interfaces before they are used: without a realistic example of usage, it is too difficult to see whether an interface is even necessary.*

**Evidence from real code:** bbolt's entire engine has **zero interfaces** — `DB`, `Tx`, `Bucket`, `Cursor` all concrete. Its freelist has *two* implementations selected by a string field and a factory function, not by polymorphism. Pebble defines real interfaces in essentially one place that matters: its `vfs` filesystem seam, which is also the one package it deliberately keeps *out* of `internal/`, because it's the stable boundary.

**Where an interface is genuinely earned:**
- A pluggable backend that actually has multiple implementations (filesystem, transport, storage).
- A fault-injection or in-memory test double you actually wrote.
- A closed set of node types walked polymorphically (AST nodes, plan nodes, expressions).
- A boundary someone outside your module implements (`database/sql/driver`).

**Mechanics:**
- Assert satisfaction at compile time: `var _ File = (*osFile)(nil)`. Moves drift from a confusing distant failure to a build error naming the type.
- **Accept interfaces, return concrete structs.**
- **No `interfaces.go` file, ever.** The interface lives in the file of the code that consumes it.
- Never a `Manager`/`Service`/`Handler` interface with one implementation.

---

## 6. Receivers and structs

**Receivers.** If in doubt, pointer. Pointer when the method mutates, when the struct holds something that must not be copied (a mutex, a `noCopy`), or when it's large. A stray value receiver fails **silently** — it mutates a copy and the write vanishes.

Never mix receiver kinds on one type. Short consistent names. Never `this` or `self`.

Method sets matter: a value's method set contains only value-receiver methods, so `T` satisfies an interface only if every method has a value receiver. A `T` stored in a map isn't addressable, so you can't call a pointer-receiver method on it.

**Structs.**
- **Make the zero value useful** where you can — `sync.Mutex`, `bytes.Buffer`. Constructor only when zero is genuinely invalid.
- **Field order matters** when memory footprint does. The compiler pads for alignment; ordering by descending size (pointers/int64 → int32 → int16 → bool/byte) can shrink a struct materially. Enable the `fieldalignment` analyzer if it matters.
- **Never embed `sync.Mutex`** — a named `mu` field, or you've exported `Lock`/`Unlock` on your public type.
- **Prefer wrapping over embedding for third-party types.** Embedding `*os.File` promotes ~20 methods into your API, including `Seek`, which may falsify an invariant you documented.
- **Options: a plain exported struct with sane zero values** unless there are 3+ optional parameters and real versioning pressure. bbolt uses a plain `*Options`. badger uses `DefaultOptions(path).WithX(...)`. Both beat functional options for a small library.

---

## 7. Errors

1. Caller doesn't need to distinguish it → `fmt.Errorf` with `%w`, return.
2. Caller matches by identity → **sentinel** `var ErrNotFound = errors.New("pkg: not found")`, matched with `errors.Is`.
3. Caller needs *data* out of it → **typed error**, matched with `errors.As`. This is where the `{Kind, Op, Entity, Err}` shape goes.
4. Adding context on the way up → **always `%w`**: `fmt.Errorf("pkg: op %d: %w", id, err)`. `%v` only to deliberately hide the cause.
5. Lowercase, no trailing punctuation.
6. **Handle exactly once.** Handle it, or wrap and return. Never log-and-return. Never `_ =`.
7. **`panic` only for invariants you own.** Corrupt internal state that should be impossible: panic. Anything from a file, a network peer, a config, or user input: return an error.
8. **Translate at the seam.** If your package wraps `os` or a driver, don't let `os.ErrNotExist` escape — map it to your sentinel, or callers must import `os` to check and the abstraction has leaked.
9. **Centralize corruption/validation detection** in one function rather than scattering checks.

One `errors.go` per package with the sentinels. Don't build a hierarchy.

---

## 8. Package layout

`golang-standards/project-layout` has 55k stars and is **not** official — Russ Cox opened issue #117 on that repo objecting that the org name implies endorsement it doesn't have and that the layout, especially its `pkg/`, doesn't reflect how real Go repos are built. Popularity, not authority. **No `pkg/` directory.**

**Library (+ optional CLI)** — what bbolt and Pebble actually do:
```
mylib/
  thing.go errors.go doc.go     # public API, flat root
  seam/                         # a genuinely public, stable boundary — NOT internal
  internal/...                  # everything else
  cmd/mytool/                   # thin CLI
```

**Application or service** — the domain-hub shape:
```
myapp/
  internal/
    domain/        # types + the interfaces consumers need. Imports nothing.
    postgres/  http/  redis/    # one package per external dependency
  cmd/myapp/       # the ONLY place that knows concrete types; wires the graph
```

**One caveat that matters.** Ben Johnson's "Standard Package Layout" — dependency-free domain root, sub-packages per adapter, wiring in `main` — is excellent, and it's for **applications with swappable backends.** Johnson wrote BoltDB, and **BoltDB does not follow it**: bbolt is a flat root package of concrete types with zero interfaces. Same author, different problem. A library with one implementation of everything is not an app with interchangeable adapters. Take the dependency-direction principle; match the *structure* to which kind of thing you're building.

**Naming:** short, lowercase. No `util`, `common`, `helpers`, `models`. No stutter — `pager.New`, not `pager.NewPager`; `storage.Engine`, not `storage.Storage`.

**File-per-concept, not file-per-layer.** `btree.go`, `cursor.go`, `journal.go` beat `types.go` + `interfaces.go` + `impl.go`. Someone looking for cursor behavior should find one file.

---

# PART TWO — DESIGN

Go doesn't reject SOLID, DDD, or LLD. It **relocates the mechanism** — down from the type/interface layer to the **package** and the **compiler's import graph**. Applying the OOP versions literally produces measurably worse Go.

## 9. SOLID, translated

| Principle | Verdict in Go |
|---|---|
| **S**ingle Responsibility | Translates — at the **package**, not the type |
| **O**pen/Closed | Differently — first-class functions, not inheritance |
| **L**iskov Substitution | Differently — behavioral contract, no hierarchy to violate |
| **I**nterface Segregation | **Strongly**, and further than Java can |
| **D**ependency Inversion | **Dangerous.** Literal translation causes interface pollution |

**SRP is about packages.** `net/http` does one thing. A `util` package violates SRP by accretion. The unit of reuse and change in Go is the package.

**OCP has a trap.** Embedded methods dispatch on the **embedded** type as receiver, not the outer one — `car.Start()` compiles to `car.Engine.Start()`. If `Start` needs `Car`-level state, embedding silently can't do it. **Embedding is not substitutable extension.** Function types and wrapping are.

**LSP** has no nominal hierarchy to violate; what's left is the behavioral contract — *require no more, promise no less*. `io.Reader` proves structural substitutability works with no inheritance at all.

**ISP is strongest, and Go pushes further than Java can.** Satisfaction is implicit, so you can declare a one-method interface **after the fact, at the call site, with zero coordination with the implementer**. Java's explicit `implements` makes late narrow interfaces expensive, which is why Java drifts toward broad early interfaces. *The bigger the interface, the weaker the abstraction.*

**DIP is the dangerous one.** In Java, "depend on an interface, inject the implementation" is correct. In Go, DIP is an **import-graph property**: push concrete detail toward `main`, keep leaf packages dependency-free. Not "an interface in front of every collaborator."

The Go answers to "decouple and stay testable without an interface for everything":
1. **Consumer-defined interfaces at the point of use.**
2. **Function types instead of single-method interfaces** — `http.HandlerFunc` is the model.
3. **Dependencies as explicit arguments.** Mat Ryer's practice moved *away* from dependency-holding struct fields over 13 years: *"If a handler function wants a dependency, it can bloody well ask for it as an argument."*

## 10. DDD, and the repository trap

**Transfers cleanly:** ubiquitous language (pure naming discipline — use the domain's real words); bounded contexts → packages, with compile-time teeth Java lacks; entities and value objects → structs with unexported fields plus a constructor.

**The repository pattern, stated plainly.** Three Dots Labs argues for domain-package repository interfaces. Their *own* retrospective names the top pitfall as **"creating an interface for every struct, even with single implementations."**

**Repository-as-package-boundary: good. Repository-as-interface: needs a second implementation or a real test double.** An `interfaces.go` with six interfaces and six implementations is the documented anti-pattern, not a style disagreement.

**Clean Architecture / Hexagonal.** The defensible middle, in the words of its own advocates: *"if you are using some architecture and it's slowing you down, it's probably the bad one."* Keep the dependency-direction principle. Introduce an interface only at a genuine substitution point. Let layer count scale with real complexity instead of being applied uniformly.

## 11. Dependency direction comes from the import graph

Not from interfaces. From the **compiler-enforced import DAG.**

Go statically forbids import cycles — a hard build error. Java needs ArchUnit or a review process to catch layering violations after the fact; Go catches them before your code compiles. That's a free architecture linter.

Bill Kennedy's Package Oriented Design operationalizes it: **packages at the same level of the tree must not import each other.** A package that imports nothing is a leaf, and everything depends toward it. That's DIP's *outcome* from graph shape alone.

When you hit a cycle, three fixes in order of preference:
1. **Extract a third package** both depend on — shared types, shared errors.
2. **Invert with a consumer-defined interface** — `A` declares the small interface it needs, `B` satisfies it structurally, `A` stops importing `B`.
3. **Pass a function type** — hand behavior in instead of importing the implementer.

**Never merge the two packages to silence the compiler.** That's the signal to re-scope, not to concatenate.

## 12. Extensibility without inheritance

Ranked, most preferred first:

1. **Consumer-defined interface satisfaction** — when behavior has state and multiple methods.
2. **Explicit delegation to a field** — verbose, and the verbosity is the point. No method-set surprises.
3. **Function-type fields (Strategy)** — comparators, encoders, cost estimators, predicates. Cheap, no ceremony.
4. **Struct embedding** — only for a real "is-mostly-a". Three failure modes: promoted methods can't see outer state; mixed receiver kinds break interface satisfaction unpredictably; **every promoted method silently joins your exported API.**
5. **Wrapping / decorating** — hold another value of the same interface, add behavior around calls. Correct for cross-cutting concerns.

**Template Method has no Go equivalent.** Embedding for defaults plus function-type fields for hooks replaces it.

## 13. Functions vs interfaces, and iterators

**If all you need is a function, use a function.** `sort.Slice`'s `less`, `filepath.WalkFunc`, `http.HandlerFunc` exist because a single stateless behavior doesn't need a named type to carry it.

Interface instead when the behavior will grow a second method, when one value must satisfy **multiple** interfaces at once (trivial for a struct, awkward for a closure), or when callers will implement a type rather than supply a function.

**Iterators — don't reflexively use the new thing.** Go 1.23's `iter.Seq` / `iter.Seq2` are idiomatic for simple, in-memory, **can't-fail** iteration. But **range-over-func cannot propagate an error out of the loop** — a mid-iteration I/O failure has nowhere to go. The convention is returning `(iter.Seq[V], func() error)`.

Note that `database/sql.Rows` still uses explicit `Next()` / `Err()` / `Close()`; Seq2 wrappers get built on top, not instead.

**Rule:** anything that can fail mid-iteration, or that a caller must pull one item at a time from, gets an explicit cursor — `Next() bool`, `Err() error`, `Close() error`. `iter.Seq` is a convenience wrapper at the public boundary, never the internal contract. Push-based `yield` also inverts pull-based operator/pipeline models awkwardly; don't fight that.

## 14. Craft

**Line of sight** (Mat Ryer): keep the happy path unindented at the left edge, push every guard and error into an early `return`. A reader should follow control flow scanning one vertical column, not holding nested `if` blocks in their head. This does more for readability than any amount of commenting.

**Naming — the actual rule** (Andrew Gerrand, *What's in a name?*): **length proportional to scope size, inversely proportional to use frequency.** `i` in a three-line loop; `numRetries` at package scope. Short names are scope signaling, not laziness. No `Get` prefix on getters. Single-method interfaces take `-er`. No stutter.

**Functions:** no hard length limit, but prefer multiple return values for unrelated data and a struct only when fields are cohesive. **Named returns discouraged** except for same-typed returns needing disambiguation, or a deferred closure that must mutate the result. Naked returns only in genuinely small functions.

**Tree walking.** `ast.Visitor` is the one GoF pattern the stdlib ships as-is — but `go/ast` also offers `Inspect` (a plain callback) and reaches for full Visitor only where *external* packages must inject traversal. **For a closed, package-owned node set: a type switch inside one recursive `walk` function.** Promote to a Visitor interface only when something outside the package genuinely needs to plug in.

**Keeping a large codebase navigable:** interfaces where consumed; tests beside the code, plus a black-box `package foo_test` for public-API-only tests; `internal/` the moment code is a shared implementation detail; `doc.go` only when no file is the obvious home for package docs.

## 15. What makes Go code age badly

| Rot | Fix |
|---|---|
| `any` / `interface{}` in public APIs | Concrete types or generics at the boundary |
| Package-level mutable state | Encapsulate in a struct instance |
| `init()` side effects | Explicit `New()` / `Open()`. Importing must do nothing |
| Over-broad, producer-defined interfaces | Small, consumer-defined |
| Goroutines with no lifecycle owner | Every goroutine's caller knows how it stops. Provide `Close` |
| `context.Context` threaded as a habit | Cancellation and deadlines only. Don't accept a `ctx` you never check |
| Premature abstraction | Wait for the second implementation |
| `util` / `common` / `helpers` | Split by domain concept. *A little copying is better than a little dependency* |
| Dead code kept "just in case" | Delete it |

## 16. The translation table

| You learned (Java / DDD / LLD) | In Go, do this |
|---|---|
| One interface per class | Concrete types; interface at the consumer, only when needed |
| Interface + single impl "for testability" | Don't. No interface until a second impl or a real fake exists |
| DI container / framework wiring | Explicit construction, wired in `main` |
| Abstract Factory, factory hierarchies | A `New*` function returning a concrete struct |
| Singleton | An explicit value, passed in. Not a package var |
| Inheritance for reuse | Composition: a named field, explicit delegation |
| Template Method with overrides | Embedding for defaults + function-type fields for hooks |
| Builder with chained setters | A struct literal, or `Options` + `DefaultOptions()` |
| Repository interface per aggregate | Package boundary; interface only if genuinely swappable |
| Mandatory controller/service/repo folders | Package boundaries + import direction |
| SRP at class level | SRP at **package** level |
| DIP via interfaces everywhere | DIP via **import-graph shape**; `internal/`; leaf packages import nothing |
| Exceptions for control flow | Explicit `(T, error)` |
| Visitor by default for trees | Type switch in one recursive `walk` |
| Iterator objects | Explicit `Next()`/`Err()`/`Close()`; `iter.Seq` only as a public wrapper |
| Getters and setters | Exported fields, or `Count()` with no `Get` |
| `IFoo` / `FooImpl` | `Foo` interface; concrete type named for what it *is* (`osFile`, `memFile`) |

---

## 17. Testing

- **Table-driven by default.** One struct per case with a name; `t.Run(tc.name, ...)` so failures isolate and re-run with `-run`.
- **`t.Helper()`** in every shared assertion or setup function, or failures point at the helper.
- **Plain `if got != want`** for scalars; `google/go-cmp`'s `cmp.Diff` for structures. No testify — smaller dependency graph, and a failed assertion doesn't abort the rest of the table.
- **Property tests** where invariants matter — `pgregory.net/rapid` (generics-based, automatic shrinking). Round-trip codecs, data-structure invariants, idempotence.
- **An invariant checker as real code, not test code.** bbolt's `Tx.Check()` walks the whole structure and validates it. Call it after every mutation in tests. Highest-leverage test artifact in any data-structure project.
- **Golden files** for anything tree- or format-shaped: tokens, ASTs, plans, serialized output. Generate expected output, eyeball once, commit, diff forever.
- **Fault injection** behind an interface for anything touching I/O. `kill -9` is not a crash test — the OS page cache survives it.
- **`go test -race` unconditionally**, every commit.
- Colocate `_test.go`. A shared `internal/testutil` only once duplication actually appears.

## 18. Tooling

Small enough that you never argue with it. A linter you routinely suppress stops being read.

```
gofumpt          # stricter gofmt superset, no debate
go vet
golangci-lint    # staticcheck, errcheck (check-blank: true), ineffassign,
                 # unused, gosimple, revive
go test -race    # every commit
```

`errcheck` with `check-blank: true` makes `_ = err` visible instead of silent. `go build -gcflags=-m` for escape analysis on hot paths — a diagnostic when curious, never a CI gate.

## 19. The rules

1. **No interface until a second real implementation or a test double exists.**
2. **Accept interfaces, return concrete structs.**
3. **Pointer receiver unless you can say why not. Never mix kinds on one type.**
4. **Library APIs are synchronous. Zero goroutines the caller didn't ask for.** Any goroutine you start has a documented way to stop, and a `Close`.
5. **Mutex serializes, channels orchestrate.** No channel where a return value works.
6. **Every error handled exactly once. `%w` always. `errors.Is`/`errors.As` at the match site.** No `_ = err`.
7. **`panic` only for invariants you own.** Never on data from a file, a peer, a config, or a user.
8. **Translate errors at the seam** so your abstraction doesn't leak `os` or driver errors.
9. **No `unsafe`, no reflection, no `any` in a public API.**
10. **Build only what's asked.** No hooks for deferred features.
11. **Test with the code, never after.** Table-driven, `-race`, invariant checker.
12. **Delete dead code immediately.**
13. **`gofumpt`, `go vet`, `golangci-lint`, `go test -race` — every commit, no bikeshedding.**
14. **SRP applies to the package; DIP applies to the import graph.** Mutual imports mean a boundary is wrong — extract or invert, never merge.
15. **No `interfaces.go` file, ever.** File-per-concept, never file-per-layer.
16. **Happy path at the left margin.** Guards early-return.
17. **Name length proportional to scope.** No `Get` prefix, no stutter.
18. **Function type before single-method interface.**
19. **Explicit cursors, not `iter.Seq`, for anything that can fail mid-iteration.**
20. **Type switch in one recursive `walk` for closed node sets.**
21. **No package-level mutable state, no `init()` side effects, no `util` package.**
