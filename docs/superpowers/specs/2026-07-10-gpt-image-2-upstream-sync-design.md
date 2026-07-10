# GPT Image 2 Upstream Sync Design

## Goal

Restore the latest upstream Codex image execution path for `gpt-image-2` on the
existing `sol-main` branch without changing the runtime behavior of any other
model or any local-only feature.

The implementation must match the `gpt-image-2` behavior in
`upstream/main` at commit `26d45fd46a2d2911adef14772465131066dae465` while
preserving the local request handling, authentication selection, logging,
quota, monitoring, and scheduling behavior already present in `sol-main`.

## Current State

The current branch already contains the `gpt-image-2` model registration,
OpenAI-compatible image routes, Responses image tool fallback, configuration,
and response translation support. The previous upstream merge intentionally
kept the local deletion of these upstream files:

- `internal/runtime/executor/codex_openai_images.go`
- `internal/runtime/executor/codex_openai_images_test.go`
- `internal/runtime/executor/codex_openai_images_extract_test.go`

As a result, the current tree does not contain the latest upstream Codex image
executor or its direct Images API path.

## Scope

### In Scope

- Restore the upstream Codex OpenAI image executor implementation required by
  `gpt-image-2`.
- Route `gpt-image-2` and `codex/gpt-image-2` requests from
  `/v1/images/generations` and `/v1/images/edits` into that executor.
- Support both streaming and non-streaming execution.
- Preserve upstream request conversion, direct Images API forwarding,
  Responses image tool fallback, SSE parsing, result extraction, usage
  reporting, error conversion, request headers, and configurable base model
  behavior for `gpt-image-2`.
- Add regression tests that prove both the target behavior and isolation from
  non-target models.

### Out of Scope

- Enabling or changing `gpt-image-1.5` routing.
- Changing XAI image model behavior.
- Changing configured OpenAI-compatible image model behavior.
- Replacing the local OpenAI images handler, AuthManager, selector, scheduler,
  logging, quota warning, monitoring, account binding, or config loading
  implementations with upstream versions.
- Synchronizing unrelated upstream changes.
- Refactoring unrelated image or Codex code.

The restored shared upstream executor may retain internal support code for
`gpt-image-1.5`, but no route or dispatch change may make that code reachable
for `gpt-image-1.5` as part of this work.

## Runtime Design

### Request Flow

For `gpt-image-2` only, the request flow will be:

1. `POST /v1/images/generations` or `POST /v1/images/edits` enters the existing
   local OpenAI images handler.
2. The handler validates the request using the existing local validation and
   request-body handling behavior.
3. When the normalized model is `gpt-image-2`, the handler builds the
   OpenAI-image executor request and calls the existing image-aware
   AuthManager execution method.
4. The existing AuthManager selects a Codex credential without bypassing local
   account binding, rotation, cooldown, or logging behavior.
5. `CodexExecutor.Execute` or `CodexExecutor.ExecuteStream` dispatches the
   request to the restored upstream image executor only when both conditions
   hold:
   - the source format is `openai-image`; and
   - the request path is `/v1/images/generations` or `/v1/images/edits`.
6. The restored executor uses the latest upstream behavior:
   - forward to a direct Images API endpoint when the request and credential
     support it;
   - otherwise translate the request to the hosted Responses
     `image_generation` tool path;
   - apply Codex authentication and model header overrides;
   - preserve identity-confusion and request-recording behavior;
   - convert streaming and non-streaming results back to OpenAI Images API
     responses.

All other models continue through their existing local branches.

### Model Isolation

The handler-level model predicate must match only the normalized
`gpt-image-2` model, including the explicit `codex/gpt-image-2` prefix form.

The following inputs must not be newly routed into the restored Codex image
executor:

- `gpt-image-1.5`
- `codex/gpt-image-1.5`
- XAI image models
- configured OpenAI-compatible image models
- ordinary Codex Responses requests
- ordinary Codex chat-completions requests

### Error Handling

The implementation will retain upstream status-code and error-body conversion
for direct and Responses-backed image calls. It will not add process exits,
panics, or post-connection timeouts.

Malformed input continues to use the current local handler responses. Upstream
non-2xx responses must be returned through the existing AuthManager error
contract so that credential rotation and cooldown behavior remain unchanged.

Streaming requests must distinguish between errors before the first downstream
byte and errors after streaming starts. SSE frames split across arbitrary read
boundaries must be accumulated and parsed without truncating image payloads.

## Expected Production Changes

The expected production change surface is limited to:

- `internal/runtime/executor/codex_openai_images.go`
- `internal/runtime/executor/codex_executor.go`
- `sdk/api/handlers/openai/openai_images_handlers.go`

An additional production file may be changed only if compilation or a failing
targeted test proves it is an inseparable dependency of the upstream
`gpt-image-2` implementation. Such a dependency must be documented in the
implementation commit.

## Test Design

Tests will be written or restored before production behavior is changed.

### Executor Tests

- `gpt-image-2` generation request conversion.
- `gpt-image-2` edit request conversion.
- Direct Images API request selection where applicable.
- Responses image tool fallback.
- Streaming and non-streaming result conversion.
- Partial image SSE handling.
- Completed image extraction without rebuilding large JSON payloads.
- Upstream error conversion.
- Usage reporting behavior.
- `gpt-image-2-base-model` fallback and configured value handling.

### Handler and Isolation Tests

- `gpt-image-2` and `codex/gpt-image-2` use image-aware AuthManager execution.
- `gpt-image-1.5` and `codex/gpt-image-1.5` retain their pre-change behavior.
- XAI image routing remains unchanged.
- OpenAI-compatible image routing remains unchanged.
- Non-image Codex requests do not enter the image executor.

The initial target test must fail on the current implementation for the
expected missing-executor behavior before production code is restored.

## Verification

The final implementation must pass all of the following:

```bash
gofmt -w internal/runtime/executor/codex_openai_images.go internal/runtime/executor/codex_openai_images_test.go internal/runtime/executor/codex_openai_images_extract_test.go internal/runtime/executor/codex_executor.go internal/runtime/executor/codex_executor_imagegen_test.go sdk/api/handlers/openai/openai_images_handlers.go sdk/api/handlers/openai/openai_images_handlers_test.go
go test ./internal/runtime/executor -run 'CodexOpenAIImage|GPTImage2' -count=1
go test ./sdk/api/handlers/openai -run 'Images|GPTImage2' -count=1
go test ./internal/runtime/executor ./sdk/api/handlers/openai -count=1
go test ./... -count=1
go build -o /tmp/cli-proxy-api-build ./cmd/server
git diff --check
make -C .. codex-sensitive-scan
```

The final diff must also show:

- no unrelated production files changed;
- no new routing behavior for `gpt-image-1.5` or other image models;
- no changes to local monitoring, quota, account-binding, selector, scheduler,
  or request-logging features.

## Commit Strategy

The design document is committed separately from implementation. The
implementation will be a focused Conventional Commit on `sol-main`; it will not
amend or rewrite the earlier GPT-5.6 upstream merge commit.
