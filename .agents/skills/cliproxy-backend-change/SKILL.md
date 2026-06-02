---
name: cliproxy-backend-change
description: Use when changing the CLIProxyAPI Go backend, including management APIs, provider runtime executors, auth stores, usage logging, config watching, websocket paths, SDK behavior, or protocol translators.
---

# CLIProxyAPI Backend Change

Use this skill before backend code edits in `CLIProxyAPI/`.

## First Checks

1. Read `AGENTS.md` in this repository.
2. Inspect the current code path, not README text, before answering behavior questions.
3. Keep changes narrow and tied to the user's request.
4. If the task touches frontend-visible management API behavior, inspect the frontend repository too.

## Runtime Truth Path

For request routing and provider behavior, trace the live code path:

`route -> handler -> service/conductor -> executor/store -> upstream`

Important areas:

- `cmd/server/`: server entrypoint and flags.
- `internal/api/`: Gin routes and middleware.
- `internal/api/handlers/management/`: management API contracts.
- `internal/runtime/executor/`: provider executors only.
- `internal/runtime/executor/helps/`: executor helpers.
- `internal/translator/`: protocol translators; avoid standalone translator-only changes unless explicitly authorized by repo rules.
- `sdk/cliproxy/`: embedded service, auth conductor, usage manager, watcher pipeline.
- `internal/usage/`: usage persistence and monitor queries.
- `internal/store/`: file/Postgres/MySQL/git/object store implementations.

## Sensitive Paths

Review carefully before changing or logging:

- API keys and management secrets.
- OAuth token files and `auths/`.
- `config.yaml`, store-backed config, and hot reload paths.
- Authorization headers, custom headers, proxy headers, and query-string tokens.
- Usage logs and monitor response payloads.

Use `make codex-sensitive-scan` from the parent workspace when local changes might include secret material or lockfiles.

## Verification

Pick the narrowest useful check:

- Go formatting: `gofmt -w <changed-go-files>`
- Targeted tests: `go test ./path/to/pkg -run TestName -count=1`
- Package tests for management/usage work: `go test ./internal/api/handlers/management ./internal/usage -count=1`
- Full backend check: `GOCACHE=/tmp/cliproxy-codex-go-cache go test ./...`
- Compile check: `GOCACHE=/tmp/cliproxy-codex-go-cache go build -o /tmp/cli-proxy-api-build ./cmd/server`

From the parent workspace, use `make verify-backend`.
