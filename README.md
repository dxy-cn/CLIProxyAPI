# CLI Proxy API

English | [Chinese](README_CN.md)

CLI Proxy API is a Go backend that exposes one proxy endpoint for multiple AI client protocols. It accepts OpenAI-compatible Chat Completions, Completions, Images, Videos, Responses, Anthropic Claude Messages, Gemini native calls, Codex Responses, Codex WebSocket traffic, xAI image/video calls, and Amp CLI provider calls, then routes each request to the configured upstream account, API key, OAuth token, or OpenAI-compatible provider.

The server is built around a shared runtime that handles authentication, model registration, protocol translation, retries, credential scheduling, request logging, usage statistics, and management APIs.

## Main Capabilities

- Unified API surface for OpenAI, Claude, Gemini, Codex, xAI, and Amp-compatible clients.
- Multiple upstream credential types: OAuth token files, provider API keys, Vertex service accounts, OpenAI-compatible providers, xAI OAuth credentials, and Amp upstream keys.
- Request translation between client protocols and provider executors.
- Model registry with aliases, prefixes, exclusions, provider-specific model lists, and embedded model catalog fallback.
- Credential routing strategies: `round-robin`, `fill-first`, `sequential-fill`, and `account-bind`.
- OpenAI-compatible image and video endpoints, including xAI Grok image and video models.
- Codex Responses WebSocket forwarding with optional WebSocket authentication.
- Management API and web control panel for config, auth files, API keys, API-key usage, model monitor data, usage queues, logs, and routing settings.
- Request logging, rotating application logs, health checks, optional pprof, Redis-style usage queues, and usage persistence.
- Local file, PostgreSQL, MySQL, object store, or Git-backed config/auth storage.
- SDK package for embedding the same proxy runtime in another Go application.

## Supported Upstreams

| Upstream                    | Configuration                                 | Runtime support                                                                                   |
| --------------------------- | --------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| Codex                       | OAuth login files or `codex-api-key` entries  | Responses API, compact responses, HTTP streaming, optional WebSocket transport                    |
| Claude                      | OAuth login files or `claude-api-key` entries | Claude Messages API, token counting, Claude Code-style request shaping                            |
| Gemini API                  | `gemini-api-key` entries                      | Gemini native `v1beta` calls and translated OpenAI/Claude requests                                |
| Gemini CLI                  | Google OAuth login files                      | Code Assist / Gemini CLI internal API, gated by `enable-gemini-cli-endpoint`                      |
| Vertex / AI Studio          | Vertex import or stored OAuth credentials     | Gemini/Vertex executor and translated request handling                                            |
| Antigravity                 | Antigravity OAuth login files                 | Model execution, signature handling, and optional credits fallback                                |
| Kimi                        | Kimi device login files                       | Chat-completion style execution through the shared runtime                                        |
| xAI                         | xAI OAuth auth files                          | Chat, image, and video execution, including Grok image/video models and OAuth callback flow       |
| OpenAI-compatible providers | `openai-compatibility` entries                | Configurable base URL, API key, headers, model aliases, compact responses, and image-capable models |
| Amp                         | `ampcode` config                              | Amp management proxy, provider aliases, Gemini bridge, and fallback to upstream Amp control plane |

## API Surface

Client API keys are configured with top-level `api-keys`. Requests can authenticate with any of these forms:

- `Authorization: Bearer <api-key>`
- `X-Goog-Api-Key: <api-key>`
- `X-Api-Key: <api-key>`
- `?key=<api-key>`
- `?auth_token=<api-key>`

Main client-facing routes:

| Route                               | Purpose                                                          |
| ----------------------------------- | ---------------------------------------------------------------- |
| `GET /healthz`                      | Liveness check                                                   |
| `GET /v1/models`                    | OpenAI-compatible model listing                                  |
| `POST /v1/chat/completions`         | OpenAI Chat Completions                                          |
| `POST /v1/completions`              | OpenAI Completions                                               |
| `POST /v1/images/generations`       | OpenAI-compatible image generation                               |
| `POST /v1/images/edits`             | OpenAI-compatible image edits                                    |
| `POST /v1/videos`                   | OpenAI-compatible video creation backed by xAI Grok video models |
| `POST /v1/videos/generations`       | xAI video generation endpoint                                    |
| `POST /v1/videos/edits`             | xAI video edit endpoint                                          |
| `POST /v1/videos/extensions`        | xAI video extension endpoint                                     |
| `GET /v1/videos/:request_id`        | Retrieve xAI video request status or result                      |
| `POST /v1/messages`                 | Anthropic Claude Messages                                        |
| `POST /v1/messages/count_tokens`    | Claude token counting                                            |
| `POST /v1/responses`                | OpenAI/Codex Responses API                                       |
| `GET /v1/responses`                 | WebSocket upgrade endpoint for Responses traffic                 |
| `POST /v1/responses/compact`        | Responses compaction endpoint                                    |
| `GET /backend-api/codex/responses`  | Codex WebSocket-compatible backend route                         |
| `POST /backend-api/codex/responses` | Codex backend Responses route                                    |
| `GET /v1beta/models`                | Gemini native model listing                                      |
| `POST /v1beta/models/*action`       | Gemini native model actions                                      |
| `GET /v1beta/models/*action`        | Gemini native read actions                                       |
| `POST /v1internal:*`                | Gemini CLI internal endpoint, disabled unless explicitly enabled |
| `/api/provider/:provider/...`       | Amp provider aliases for OpenAI, Claude, and Gemini-style calls  |

Management routes live under `/v0/management` and are only registered when `remote-management.secret-key` is configured. The management API accepts `Authorization: Bearer <secret-key>` or `X-Management-Key: <secret-key>`.

## Quick Start

Requirements:

- Go `1.26` or newer, matching `go.mod`.
- A `config.yaml` file or one of the external store environment configurations listed below.

Start from the example config:

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` and set at least one client API key plus one upstream credential. A minimal OpenAI-compatible provider configuration looks like this:

```yaml
host: 127.0.0.1
port: 8317

api-keys:
  - sk-local-dev

openai-compatibility:
  - name: upstream-openai
    api-key: sk-upstream
    base-url: https://api.openai.com
    models:
      - name: gpt-4.1
        alias: gpt-4.1
```

Run the server:

```bash
go run ./cmd/server --config config.yaml
```

Call the proxy:

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H 'Authorization: Bearer sk-local-dev' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "Say hello in one sentence."}]
  }'
```

Build a local binary:

```bash
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

## Login and Credential Import

The server can create OAuth-backed auth files for Codex and Claude:

```bash
go run ./cmd/server --config config.yaml --login
go run ./cmd/server --config config.yaml --codex-login
go run ./cmd/server --config config.yaml --codex-device-login
go run ./cmd/server --config config.yaml --claude-login
```

Useful login flags:

- `--no-browser` prints the OAuth URL instead of opening a browser.
- `--oauth-callback-port <port>` overrides the callback port for OAuth flows.
- `--project_id <id>` sets the Google project ID for Gemini login.
- `--vertex-import <file>` imports a Vertex service account JSON file.
- `--vertex-import-prefix <prefix>` namespaces imported Vertex models.

By default auth files are stored under `auth-dir` from `config.yaml`. For automated deployments, auth files can be imported at startup with:

```bash
AUTH_BOOTSTRAP_DIR=/path/to/auths
AUTH_BOOTSTRAP_FILE=/path/to/auth.json
AUTH_BOOTSTRAP_OVERWRITE=true
```

## Configuration

See [config.example.yaml](config.example.yaml) for the full schema. Important top-level settings include:

| Key                         | Purpose                                                                 |
| --------------------------- | ----------------------------------------------------------------------- |
| `host`, `port`              | Server bind address                                                     |
| `tls`                       | HTTPS certificate and key settings                                      |
| `auth-dir`                  | Local auth file directory                                               |
| `api-keys`                  | Client keys allowed to call the proxy                                   |
| `proxy-url`                 | Outbound HTTP proxy for upstream calls                                  |
| `local-model`               | Use only the embedded model catalog                                     |
| `force-model-prefix`        | Require prefixed model names for prefixed credentials                   |
| `request-log`               | Enable detailed request logging                                         |
| `disable-image-generation`  | Disable image tool injection globally or only on chat-style endpoints   |
| `passthrough-headers`       | Forward selected upstream response headers to clients                   |
| `request-retry`             | Retry count for failed provider requests                                |
| `max-retry-credentials`     | Limit how many credentials are attempted per failed request             |
| `max-retry-interval`        | Max wait before retrying a cooled-down credential                       |
| `streaming.first-chunk-timeout` | Seconds to wait for the first streaming payload (TTFT); `0` disables it |
| `streaming.bootstrap-retries` | Retry count before any streaming bytes are sent, including TTFT timeout |
| `disable-cooling`           | Disable quota cooldown scheduling                                       |
| `usage-statistics-enabled`  | Enable in-memory usage aggregation                                      |
| `redis-usage-queue-retention-seconds` | In-memory usage queue retention for the Management API                  |
| `usage-persistence-enabled` | Persist usage data to PostgreSQL, MySQL, or SQLite                      |
| `ws-auth`                   | Require auth on WebSocket endpoints                                     |
| `remote-management`         | Management secret, remote access, and control panel asset settings      |
| `remote-management.disable-control-panel` | Disable the bundled management panel route and panel asset download     |
| `remote-management.disable-auto-update-panel` | Disable background management panel asset updates                       |
| `remote-management.panel-release-url` | Override the management panel release asset URL                         |
| `routing.strategy`          | Credential selection strategy                                           |
| `payload`                   | Default, override, raw override, and filter rules for provider payloads |

Provider sections support common controls such as `api-key`, `priority`, `prefix`, `base-url`, `proxy-url`, `models`, `headers`, and `excluded-models`, depending on provider type.

`disable-image-generation` accepts `false`, `true`, or `"chat"`. `true` disables `image_generation` injection and returns `404` for `/v1/images/*` endpoints. `"chat"` only disables image generation injection on non-image endpoints while keeping image endpoints available.

## Routing and Models

The runtime registers every configured credential into a shared model registry. Client-visible model IDs can come from upstream discovery, embedded models, per-provider `models` aliases, OAuth-level aliases, or prefixed credentials.

Routing strategies:

- `round-robin` or `rr`: rotate across ready credentials.
- `fill-first` or `ff`: prefer the first ready credential until it is exhausted.
- `sequential-fill` or `sf`: keep using the current credential until it becomes unavailable.
- `account-bind` or `ab`: bind each client API key to a specific durable auth identity. Use `routing.default-model-account` as the fallback for unbound keys.

`quota-exceeded` can enable automatic project switching, preview-model switching, and Antigravity credits fallback when supported by the provider.

## WebSocket Responses

Codex Responses traffic can use WebSockets through:

- `GET /v1/responses`
- `GET /backend-api/codex/responses`

Set `ws-auth: true` to require the same proxy API key authentication on WebSocket connections. For Codex API-key credentials, set `websockets: true` under the matching `codex-api-key` entry to prefer the WebSocket executor.

## Management and Monitoring

Enable management by setting:

```yaml
remote-management:
  allow-remote: false
  secret-key: change-me
```

Then open:

```text
http://127.0.0.1:8317/management.html
http://127.0.0.1:8317/user/monitor
```

The management API provides endpoints for:

- reading and updating `config.yaml`;
- managing API keys and auth files;
- API-key usage summaries and live usage queue records;
- OAuth helper flows, including xAI;
- model and quota monitor data;
- request logs and usage statistics;
- routing strategy and WebSocket auth settings;
- public monitor endpoints for scoped API keys;
- Amp configuration and monitor metadata.

Keep `allow-remote: false` for local-only administration unless the server is protected by TLS, a trusted reverse proxy, and a strong management key.

## Storage Backends

Without store environment variables, the server reads `config.yaml` and local auth files from the working directory and `auth-dir`.

External stores are selected with environment variables. A local `.env` file in the working directory is loaded automatically.

| Backend      | Environment variables                                                                                                               |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| PostgreSQL   | `PGSTORE_DSN`, optional `PGSTORE_SCHEMA`, `PGSTORE_LOCAL_PATH`                                                                      |
| MySQL        | `MYSQLSTORE_DSN`, optional `MYSQLSTORE_LOCAL_PATH`                                                                                  |
| Object store | `OBJECTSTORE_ENDPOINT`, `OBJECTSTORE_ACCESS_KEY`, `OBJECTSTORE_SECRET_KEY`, `OBJECTSTORE_BUCKET`, optional `OBJECTSTORE_LOCAL_PATH` |
| Git store    | `GITSTORE_GIT_URL`, optional `GITSTORE_GIT_USERNAME`, `GITSTORE_GIT_TOKEN`, `GITSTORE_GIT_BRANCH`, `GITSTORE_LOCAL_PATH`            |

Startup preference is PostgreSQL, then MySQL, then object store, then Git store, then local files.

When `usage-persistence-enabled` is true, usage persistence uses PostgreSQL first when `PGSTORE_DSN` is present, then MySQL when `MYSQLSTORE_DSN` is present, otherwise SQLite in `auth-dir`.

When Home control-plane mode is enabled, the server can receive Home credentials through `--home-jwt` or `HOME_JWT`. `--home-disable-cluster-discovery` keeps the configured endpoint instead of discovering a cluster endpoint.

## Amp Integration

The `ampcode` section configures Amp CLI support. The backend can:

- proxy Amp management and OAuth routes through `/api`;
- expose `/threads`, `/docs`, `/settings`, `/threads.rss`, and `/news.rss` root routes expected by Amp clients;
- map `/api/provider/:provider` requests to local OpenAI, Claude, and Gemini handlers;
- fall back to the configured Amp upstream when no local provider or model mapping is available;
- restrict Amp management routes to localhost.

## SDK Embedding

The same runtime can be embedded through the Go SDK under `sdk/cliproxy`.

Documentation:

- [SDK usage](docs/sdk-usage.md)
- [SDK access providers](docs/sdk-access.md)
- [SDK advanced usage](docs/sdk-advanced.md)
- [SDK watcher](docs/sdk-watcher.md)

## Development

Useful checks:

```bash
go test ./...
go build -o /tmp/cli-proxy-api ./cmd/server
```

Important directories:

| Path                        | Purpose                                                                                                  |
| --------------------------- | -------------------------------------------------------------------------------------------------------- |
| `cmd/server`                | CLI entrypoint, config loading, login modes, storage selection                                           |
| `internal/api`              | Gin server, public API routes, management route registration                                             |
| `internal/api/modules/amp`  | Amp integration routes and upstream fallback                                                             |
| `internal/config`           | YAML schema, config loading, migration helpers                                                           |
| `internal/runtime/executor` | Provider executors for Codex, Claude, Gemini, Vertex, Antigravity, Kimi, xAI, and OpenAI-compatible upstreams |
| `internal/home`             | Optional Home control-plane integration                                                                  |
| `internal/redisqueue`       | Redis-style usage queue protocol and refresh notifications                                               |
| `internal/translator`       | Protocol translation between client request formats and runtime requests                                 |
| `internal/store`            | PostgreSQL, MySQL, object store, Git, and file-backed storage                                            |
| `internal/usage`            | Usage aggregation and persistence                                                                        |
| `sdk/cliproxy`              | Embeddable proxy service, auth manager, model registry integration                                       |

## License

This project is licensed under the terms in [LICENSE](LICENSE).
