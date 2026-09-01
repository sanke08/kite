run:
	go run ./cmd/kite

test:
	go test -race ./...

# slt runs the pinned sqllogictest manifest against the real sqlite3 binary.
# It must report 100% — if it does not, the runner is wrong, not SQLite.
slt:
	go run ./cmd/slt testdata/manifest.json

lint:
	gofmt -l -w .
	go vet ./...

.PHONY: run test lint slt
