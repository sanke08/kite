@docs/BUILD_PLAN.md
@docs/GO_STANDARDS.md
@docs/GO_STYLE.md
@docs/LEARNING_PROTOCOL.md
@docs/TEACHING_PROTOCOL.md

## Git

**Never run `git commit` or `git push`.** Git is the author's, not yours.

Staging, committing, branching, pushing, tagging — all his. Do not offer to
commit at the end of a step, and do not treat a finished step as a commit
boundary that needs acting on. Say what changed; he decides what happens to it.

Reading git state (`git status`, `git log`, `git diff`) is fine when it answers
a question about the work.

## Dependencies

**Never run `go get`, `go install`, or `go mod tidy` on your own.** Do not add a
dependency in anticipation of needing it.

A dependency gets added at the moment code that imports it is being written, and
he runs the command. Stdlib until then — `gofmt` and `go vet` ship with the
toolchain and cover Phase 0. gofumpt, golangci-lint, `go-cmp`, and `rapid` are
all named in the standards docs, and all of them wait until there is Go in the
repo for them to act on.

This is the same rule as not scaffolding empty directories: nothing enters the
repo before the code that justifies it.

## Tests

**Do not write test files.** He writes the tests.

For each step, say what the test should cover and name the specific input that
would break the code — the empty file, the offset past the end, the reopen that
catches a truncating Open. Then stop. Naming the case is the teaching; writing
the assertion is his.

This overrides the "tests written with the code" line in the protocol docs: the
tests still come with the code, but they come from him.

## Verification

`make lint` (gofmt + go vet) and `make test`. That is the whole loop.

Do not build tooling around the tooling — no strip target, no comment linters,
no scripts that check the protocol is being followed. Time spent on that is
time not spent on the database.

## Comment formatting

In a Go doc comment, a line indented deeper than its paragraph's first line
renders as a **code block** — monospaced and syntax-coloured in VSCode hover and
on pkg.go.dev. gofmt also rewrites such lines to tab indentation.

So continuation lines start at the `// ` margin, never aligned under the tag:

	// WHY: io.ReaderAt semantics — a short read is an error, never a
	// partial success. A page is 4096 bytes or it is not a page.

Indent only what genuinely should render monospaced: a WALKTHROUGH call chain, a
byte layout, a worked EG whose alignment carries meaning. Those get one tab.

## Naming

Names must be readable to someone opening the file cold. He should not have to
infer what a parameter is from how it gets used three lines down.

**No abbreviations. Whole words.** If a name needs expanding in the reader's
head before it means anything, it is the wrong name — `dst`, `src`, `off`, `p`,
`fi`, `cnt`, `idx`, `ptr` all fail this.

	ReadAt(buf []byte, offset int64)     buf is filled by the call
	WriteAt(data []byte, offset int64)   data is written by the call
	Open(path string)                    a filesystem path

The exceptions are words that ARE the word: `err`, `ok`, `i`/`j` as loop
indices, `n` for a count returned alongside an error. Nobody expands those.

**Receivers name the type, not its first letter.** `file *osFile`, `pager
*Pager`, `tree *BTree`. Not `f`, `p`, `t`. And never a field like `o.f` — two
stacked single letters is unreadable at a glance. `file.handle` reads.

### What does NOT get renamed

Names that match the standard library or a Go convention stay exactly as they
are, even when a longer name would read better in isolation:

- `ReadAt` / `WriteAt` / `Sync` / `Truncate` / `Close` — this is io.ReaderAt and
  io.WriterAt. Matching them is why *os.File satisfies fs.File for free and why
  any Go reader knows the contract on sight. `ReadPageAt` breaks that for nothing.
- `Size()` not `GetSize()`. No `Get` prefix on getters.
- `OS()`, `New()` — no stutter, no `NewOSFileSystem`.
- `FS`, `DB`, `Tx`, `ID` — established initialisms, capitalised as units.

If a rename would make a stdlib type stop satisfying an interface, or make a Go
reader pause at a name they already know, it is the wrong rename. Say so and
rename the parameters instead.

## Comment volume

Comment the decisions, not the code. A `WHY:` earns its place when there is a
plausible alternative he would otherwise wonder about, or a failure that is
invisible without it. Everything else gets one plain line or nothing.

- One tagged block per declaration at most. Two `EG:` blocks on one method
  signature is too much — pick the one that teaches.
- Four lines is a long `WHY:`. Past that, either the point needs sharpening or
  the code needs splitting.
- Delegating one-liners (`func (file *osFile) Sync() error { ... }`) get no
  comment at all. Nothing about them is a decision.
- No comment that restates the signature, and none that documents something not
  in the file.

The failure mode being avoided is real and measured: 68% comments in one of his
existing files, 86% in another. Density front-loaded on the two or three genuine
decisions in a file beats uniform coverage every time.

## No forward references in comments

Do not cite phase numbers, doc filenames, or future work in code comments —
"Phase 26 adds F_FULLFSYNC", "GO_STYLE.md §6 says", "see BUILD_PLAN.md
Decision #8". He is reading the file now, and a pointer to something that does
not exist yet is noise.

Say the reason itself instead. Not "this makes Phase 11 possible" but "so a
test can hand the engine a filesystem that fails writes on demand".

The one exception is a deliberately temporary implementation, where the comment
should say it is temporary and what replaces it — that is information about the
code in front of him, not a preview.

## No ceremony code

Do not write code whose only purpose is to reassure the compiler or the reader.
No `var _ Iface = (*impl)(nil)` assertions, no empty `doc.go` files, no
placeholder structs, no wrapper that only forwards.

If a function already returns the interface type, the compiler checks
satisfaction there and an assertion adds nothing.

## The dependency tree

Every phase that changes the package graph gets
**`docs/trees/phase-NN/prompt.txt`** — a Gemini prompt that draws the graph as
a hand-drawn whiteboard diagram, dark background, white strokes, Excalidraw
style. Written in the same message as the code. He pastes it into Gemini and
saves the result as `tree.png` beside it. One folder per phase; nothing is
overwritten, so the images are a history of how the engine grew.

The prompt is the checked-in, diffable record of the graph — an image is not.
Keep `docs/trees/README.md`'s package descriptions current alongside it.

### Writing the prompt

Start with the shared style block from `docs/trees/STYLE.txt`, copied byte for
byte so the images look like one series. Then the content half, and the content
half must be exhaustive — a vague prompt gets a generic diagram back:

- Every box is a PACKAGE drawn UML-style: package name at the top, a divider
  line, then its `.go` files listed inside, one per line, each with three or
  four words saying what it holds (`logic.go   Truth - TRUE/FALSE/UNKNOWN`).
  The file names are the point of the diagram — he is reading it to find out
  which file holds what.
- List the files from disk, never from memory:

	ls */*.go internal/*/*.go internal/sql/*/*.go *.go

  and check every non-test file appears somewhere in the prompt.
- Name every box and say which row and side it sits on.
- List every arrow as one `FROM -> TO` line. Never "connect these logically".
- State the arrow count in and out of each box, as a checklist the image can
  be verified against.
- Name any box several arrows converge on and say **draw it ONE TIME ONLY** —
  Gemini's default is to duplicate a shared node.
- State the total number of arrows in the image as the last line.
- Say explicitly when a package is deliberately unconnected, and that the
  missing arrows are the information.

`docs/trees/README.md` has a table of the ways Gemini gets it wrong and which
line to repeat to fix each one.

### Deriving it

Never from memory. Run:

	go list -f '{{.ImportPath}} -> {{join .Imports " "}}' ./... | grep sanke08

and check every arrow in the prompt against that output before writing it. An
arrow exists only if the import exists. A package importing nothing of ours is
a leaf. A package nothing imports goes in the dashed "built, not wired in yet"
group, not into a tree it has not earned.

Do not draw the tree as ASCII art in markdown — hand-aligned boxes do not
survive the next edit, and the branching does not read.
