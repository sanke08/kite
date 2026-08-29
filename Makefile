# Build, test and lint targets for kite.
#
# WHY: every target here is one the plan says runs on every commit. There is
#      deliberately no `make build` producing a binary into the repo — the
#      REPL is `go run ./cmd/kite` until there's a reason to ship an artifact.

# WHY: -race unconditionally, per GO_STANDARDS.md §9. A storage engine that
#      races is a corruption bug, and the detector costs nothing at this size.
#      Turning it on later means every test written before then ran unchecked.
test:
	go test -race ./...

# WHY: gofumpt and golangci-lint are not stdlib. `go tool` reads the tool
#      directives in go.mod, so the versions are pinned in the repo and every
#      machine runs the same linter. Adding the tools is Step 1's only
#      dependency work; the config lands with the first real Go file.
lint:
	go tool gofumpt -l -w .
	go vet ./...
	go tool golangci-lint run

# strip removes the tagged teaching comments and prints what's left.
#
# WHY: LEARNING_PROTOCOL.md §4 — the tags exist so teaching and production Go
#      are separable artifacts from one file. This target is what makes that
#      claim checkable rather than aspirational.
#
# EG:  //  WHY: SQL doubles a quote to escape it     <- removed
#      // scanString reads a string literal.         <- kept
#
# WITHOUT: nothing enforces the boundary, tagged comments drift into untagged
#          ones, and the file ends up 68% comments like api-gateway/router.go.
#
# NOTE: prints to stdout rather than rewriting files. Rewriting would destroy
#       the teaching copy, which is the artifact that matters most here.
strip:
	@find . -name '*.go' -not -path './.git/*' | while read -r f; do \
		echo "==> $$f"; \
		grep -v -E '^[[:space:]]*//[[:space:]]*(WHY|EG|WITHOUT|TRAP):' "$$f" \
		| grep -v -E '^[[:space:]]*//[[:space:]]{6,}'; \
	done

.PHONY: test lint strip
