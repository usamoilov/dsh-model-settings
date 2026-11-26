# The program the DSH plugins start; `go build -o bin/` adds .exe on Windows.
build:
	go build -trimpath -o bin/ ./cmd/dsh-model-settings

# Unit tests (Go), the plugin modules against the built program, the settings page.
test: build
	go test ./...
	node shim/test/integration.test.mjs
	node packages/dsh-model-defaults/test/client.test.mjs

# On a machine with DSH: real profiles are only read; the compose check runs `dsh --dump-config`.
live:
	go test -tags live -v ./internal/profile

.PHONY: build test live
