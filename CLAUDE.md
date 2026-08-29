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
