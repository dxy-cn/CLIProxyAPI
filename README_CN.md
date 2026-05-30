# CLI 代理 API

[English](README.md) | 中文

CLI Proxy API 是一个 Go 后端服务，用一个代理入口承接多种 AI 客户端协议。它可以接收 OpenAI 兼容的 Chat Completions、Completions、Images、Responses，请求 Claude Messages、Gemini 原生接口、Codex Responses、Codex WebSocket 流量，以及 Amp CLI 的 provider 调用，再把请求路由到配置好的上游账号、API Key、OAuth token 或 OpenAI 兼容服务。

服务内部共用一套运行时，负责认证、模型注册、协议转换、重试、凭证调度、请求日志、使用量统计和管理 API。

## 主要能力

- 为 OpenAI、Claude、Gemini、Codex 和 Amp 兼容客户端提供统一 API 入口。
- 支持多种上游凭证：OAuth token 文件、服务商 API Key、Vertex 服务账号、OpenAI 兼容服务和 Amp 上游密钥。
- 在不同客户端协议和 provider executor 之间做请求转换。
- 模型注册表支持 alias、prefix、排除模型、服务商模型列表和内置模型目录回退。
- 凭证路由策略支持 `round-robin`、`fill-first`、`sequential-fill` 和 `account-bind`。
- 支持 Codex Responses WebSocket 转发，并可开启 WebSocket 鉴权。
- 提供管理 API 和 Web 控制台，可管理配置、auth 文件、API Key、模型监控数据、使用量、日志和路由设置。
- 支持请求日志、滚动应用日志、健康检查、可选 pprof 和使用量持久化。
- 支持本地文件、PostgreSQL、MySQL、对象存储或 Git 后端保存配置和认证数据。
- 提供 Go SDK，可在其他 Go 应用中嵌入同一套代理运行时。

## 支持的上游

| 上游 | 配置方式 | 运行时支持 |
| --- | --- | --- |
| Codex | OAuth 登录文件或 `codex-api-key` 配置 | Responses API、compact responses、HTTP streaming、可选 WebSocket transport |
| Claude | OAuth 登录文件或 `claude-api-key` 配置 | Claude Messages API、token counting、Claude Code 风格请求处理 |
| Gemini API | `gemini-api-key` 配置 | Gemini 原生 `v1beta` 调用，以及翻译后的 OpenAI/Claude 请求 |
| Gemini CLI | Google OAuth 登录文件 | Code Assist / Gemini CLI internal API，由 `enable-gemini-cli-endpoint` 控制是否启用 |
| Vertex / AI Studio | Vertex import 或已保存的 OAuth 凭证 | Gemini/Vertex executor 和翻译后的请求处理 |
| Antigravity | Antigravity OAuth 登录文件 | 模型执行、signature 处理，以及可选 credits fallback |
| Kimi | Kimi device login 文件 | 通过共享运行时执行 chat-completion 风格请求 |
| OpenAI 兼容服务 | `openai-compatibility` 配置 | 可配置 base URL、API Key、headers、模型 alias 和 compact responses |
| Amp | `ampcode` 配置 | Amp 管理代理、provider aliases、Gemini bridge，以及向 Amp 上游控制面的 fallback |

## API 接口

客户端 API Key 通过顶层 `api-keys` 配置。请求可以使用以下任意方式认证：

- `Authorization: Bearer <api-key>`
- `X-Goog-Api-Key: <api-key>`
- `X-Api-Key: <api-key>`
- `?key=<api-key>`
- `?auth_token=<api-key>`

主要客户端接口：

| 路由 | 用途 |
| --- | --- |
| `GET /healthz` | 存活检查 |
| `GET /v1/models` | OpenAI 兼容模型列表 |
| `POST /v1/chat/completions` | OpenAI Chat Completions |
| `POST /v1/completions` | OpenAI Completions |
| `POST /v1/images/generations` | OpenAI 兼容图片生成 |
| `POST /v1/images/edits` | OpenAI 兼容图片编辑 |
| `POST /v1/messages` | Anthropic Claude Messages |
| `POST /v1/messages/count_tokens` | Claude token counting |
| `POST /v1/responses` | OpenAI/Codex Responses API |
| `GET /v1/responses` | Responses 流量的 WebSocket upgrade 入口 |
| `POST /v1/responses/compact` | Responses compaction 接口 |
| `GET /backend-api/codex/responses` | Codex WebSocket 兼容后端路由 |
| `POST /backend-api/codex/responses` | Codex 后端 Responses 路由 |
| `GET /v1beta/models` | Gemini 原生模型列表 |
| `POST /v1beta/models/*action` | Gemini 原生模型 action |
| `GET /v1beta/models/*action` | Gemini 原生读取 action |
| `POST /v1internal:*` | Gemini CLI internal endpoint，默认禁用，需要显式开启 |
| `/api/provider/:provider/...` | Amp provider aliases，兼容 OpenAI、Claude 和 Gemini 风格调用 |

管理路由位于 `/v0/management` 下，只有配置 `remote-management.secret-key` 后才会注册。管理 API 接受 `Authorization: Bearer <secret-key>` 或 `X-Management-Key: <secret-key>`。

## 快速开始

环境要求：

- Go `1.26` 或更新版本，与 `go.mod` 保持一致。
- 一个 `config.yaml` 文件，或下文列出的外部存储环境变量配置。

从示例配置开始：

```bash
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，至少配置一个客户端 API Key 和一个上游凭证。一个最小的 OpenAI 兼容服务配置如下：

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

启动服务：

```bash
go run ./cmd/server --config config.yaml
```

调用代理：

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H 'Authorization: Bearer sk-local-dev' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "Say hello in one sentence."}]
  }'
```

构建本地二进制：

```bash
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

## 登录和凭证导入

服务可以为多个 provider 创建 OAuth auth 文件：

```bash
go run ./cmd/server --config config.yaml --login
go run ./cmd/server --config config.yaml --codex-login
go run ./cmd/server --config config.yaml --codex-device-login
go run ./cmd/server --config config.yaml --claude-login
go run ./cmd/server --config config.yaml --antigravity-login
go run ./cmd/server --config config.yaml --kimi-login
```

常用登录参数：

- `--no-browser`：不自动打开浏览器，只打印 OAuth URL。
- `--oauth-callback-port <port>`：覆盖 OAuth callback 端口。
- `--project_id <id>`：为 Gemini 登录设置 Google project ID。
- `--vertex-import <file>`：导入 Vertex service account JSON 文件。
- `--vertex-import-prefix <prefix>`：为导入的 Vertex 模型加命名空间前缀。

默认情况下，auth 文件保存在 `config.yaml` 的 `auth-dir` 下。自动化部署时，可以在启动时导入 auth 文件：

```bash
AUTH_BOOTSTRAP_DIR=/path/to/auths
AUTH_BOOTSTRAP_FILE=/path/to/auth.json
AUTH_BOOTSTRAP_OVERWRITE=true
```

## 配置

完整配置结构见 [config.example.yaml](config.example.yaml)。常用顶层配置包括：

| 配置项 | 用途 |
| --- | --- |
| `host`, `port` | 服务监听地址 |
| `tls` | HTTPS 证书和私钥配置 |
| `auth-dir` | 本地 auth 文件目录 |
| `api-keys` | 允许调用代理的客户端 key |
| `proxy-url` | 上游请求使用的 HTTP proxy |
| `local-model` | 只使用内置模型目录 |
| `force-model-prefix` | 对带 prefix 的凭证强制要求显式模型前缀 |
| `request-log` | 启用详细请求日志 |
| `passthrough-headers` | 将部分上游响应头透传给客户端 |
| `request-retry` | provider 请求失败后的重试次数 |
| `max-retry-credentials` | 单次失败请求最多尝试多少个凭证 |
| `max-retry-interval` | 对冷却凭证重试前的最大等待时间 |
| `streaming.first-chunk-timeout` | 等待首个流式 payload（TTFT）的秒数，`0` 表示关闭 |
| `streaming.bootstrap-retries` | 流式响应写出任何字节前的重试次数，也用于 TTFT 超时重试 |
| `disable-cooling` | 禁用 quota 冷却调度 |
| `usage-statistics-enabled` | 启用内存使用量聚合 |
| `usage-persistence-enabled` | 将使用量持久化到 PostgreSQL、MySQL 或 SQLite |
| `ws-auth` | WebSocket 端点是否要求鉴权 |
| `routing.strategy` | 凭证选择策略 |
| `payload` | provider payload 的默认值、覆盖、raw 覆盖和过滤规则 |

不同 provider 配置支持 `api-key`、`priority`、`prefix`、`base-url`、`proxy-url`、`models`、`headers`、`excluded-models` 等字段，具体取决于 provider 类型。

## 路由和模型

运行时会把每个已配置凭证注册到共享模型表。客户端可见的模型 ID 可以来自上游发现、内置模型、provider 级 `models` alias、OAuth 级 alias，或带 prefix 的凭证。

路由策略：

- `round-robin` 或 `rr`：在可用凭证之间轮询。
- `fill-first` 或 `ff`：优先使用第一个可用凭证，直到它耗尽或不可用。
- `sequential-fill` 或 `sf`：持续使用当前凭证，直到它不可用后再顺序切换。
- `account-bind` 或 `ab`：把每个客户端 API Key 绑定到指定的稳定 auth identity。未显式绑定的 key 可通过 `routing.default-model-account` 设置 fallback。

`quota-exceeded` 可以启用自动切换 project、切换 preview model，以及在 provider 支持时使用 Antigravity credits fallback。

## WebSocket Responses

Codex Responses 流量可以通过 WebSocket 进入：

- `GET /v1/responses`
- `GET /backend-api/codex/responses`

设置 `ws-auth: true` 后，WebSocket 连接也需要使用同一套代理 API Key 鉴权。对 Codex API Key 凭证，可以在对应 `codex-api-key` 条目下设置 `websockets: true`，优先使用 WebSocket executor。

## 管理和监控

通过以下配置启用管理能力：

```yaml
remote-management:
  allow-remote: false
  secret-key: change-me
```

然后打开：

```text
http://127.0.0.1:8317/management.html
```

管理 API 提供以下能力：

- 读取和更新 `config.yaml`；
- 管理 API Key 和 auth 文件；
- OAuth 辅助流程；
- 模型和 quota 监控数据；
- 请求日志和使用量统计；
- 路由策略和 WebSocket 鉴权设置；
- Amp 配置和监控元数据。

除非服务已经放在 TLS、可信反向代理和强管理密钥之后，否则建议保持 `allow-remote: false`，只允许本地管理。

## 存储后端

未配置存储环境变量时，服务会从工作目录读取 `config.yaml`，并从 `auth-dir` 读取本地 auth 文件。

外部存储通过环境变量选择。工作目录下的 `.env` 文件会在启动时自动加载。

| 后端 | 环境变量 |
| --- | --- |
| PostgreSQL | `PGSTORE_DSN`，可选 `PGSTORE_SCHEMA`、`PGSTORE_LOCAL_PATH` |
| MySQL | `MYSQLSTORE_DSN`，可选 `MYSQLSTORE_LOCAL_PATH` |
| 对象存储 | `OBJECTSTORE_ENDPOINT`、`OBJECTSTORE_ACCESS_KEY`、`OBJECTSTORE_SECRET_KEY`、`OBJECTSTORE_BUCKET`，可选 `OBJECTSTORE_LOCAL_PATH` |
| Git store | `GITSTORE_GIT_URL`，可选 `GITSTORE_GIT_USERNAME`、`GITSTORE_GIT_TOKEN`、`GITSTORE_GIT_BRANCH`、`GITSTORE_LOCAL_PATH` |

启动时的存储优先级是 PostgreSQL、MySQL、对象存储、Git store、本地文件。

当 `usage-persistence-enabled` 为 true 时，使用量持久化会优先使用 `PGSTORE_DSN` 对应的 PostgreSQL，其次使用 `MYSQLSTORE_DSN` 对应的 MySQL；如果两者都没有配置，则使用 `auth-dir` 下的 SQLite。

## Amp 集成

`ampcode` 配置段用于支持 Amp CLI。后端可以：

- 通过 `/api` 代理 Amp 管理和 OAuth 路由；
- 暴露 Amp 客户端期望的 `/threads`、`/docs`、`/settings`、`/threads.rss`、`/news.rss` 根路由；
- 将 `/api/provider/:provider` 请求映射到本地 OpenAI、Claude 和 Gemini handler；
- 当本地 provider 或模型映射不可用时，fallback 到配置的 Amp 上游；
- 将 Amp 管理路由限制为仅 localhost 可访问。

## SDK 嵌入

同一套运行时可以通过 `sdk/cliproxy` 下的 Go SDK 嵌入到其他应用。

文档：

- [SDK 使用文档](docs/sdk-usage_CN.md)
- [SDK 认证接入](docs/sdk-access_CN.md)
- [SDK 高级用法](docs/sdk-advanced_CN.md)
- [SDK watcher](docs/sdk-watcher_CN.md)

## 开发

常用检查：

```bash
go test ./...
go build -o /tmp/cli-proxy-api ./cmd/server
```

重要目录：

| 路径 | 用途 |
| --- | --- |
| `cmd/server` | CLI 入口、配置加载、登录模式、存储选择 |
| `internal/api` | Gin 服务、公开 API 路由、管理路由注册 |
| `internal/api/modules/amp` | Amp 集成路由和上游 fallback |
| `internal/config` | YAML schema、配置加载、迁移辅助逻辑 |
| `internal/runtime/executor` | Codex、Claude、Gemini、Vertex、Antigravity、Kimi 和 OpenAI 兼容上游 executor |
| `internal/translator` | 客户端请求格式和运行时请求之间的协议转换 |
| `internal/store` | PostgreSQL、MySQL、对象存储、Git 和文件存储 |
| `internal/usage` | 使用量聚合和持久化 |
| `sdk/cliproxy` | 可嵌入代理服务、认证管理器、模型注册表集成 |

## 许可证

本项目根据 [LICENSE](LICENSE) 中的条款授权。
