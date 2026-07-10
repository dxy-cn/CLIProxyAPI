# GPT Image 2 Upstream Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the latest upstream Codex image execution path for `gpt-image-2` while preserving every non-target model and local-only runtime behavior.

**Architecture:** Keep the local OpenAI images handler and AuthManager as the request boundary, but route only normalized `gpt-image-2` image-endpoint requests through the latest upstream Codex OpenAI image executor. Restore the upstream executor implementation verbatim, including its direct Images API path, hosted Responses fallback support, streaming and non-streaming conversion, usage reporting, and upstream error mapping, then add a local dispatch guard so the shared executor is reachable only for `gpt-image-2`; retain existing XAI, OpenAI-compatible, and non-image request paths.

**Tech Stack:** Go 1.26+, Gin, CLIProxyAPI AuthManager and executor SDK, `gjson`/`sjson`, `net/http`, Go `testing` and `httptest`.

## Global Constraints

- Work only on branch `sol-main`.
- Upstream reference is `upstream/main` commit `26d45fd46a2d2911adef14772465131066dae465`.
- Production behavior changes are limited to `gpt-image-2` and `codex/gpt-image-2` on `/v1/images/generations` and `/v1/images/edits`.
- Do not enable or change routing for `gpt-image-1.5` or `codex/gpt-image-1.5`.
- Do not change XAI image models, configured OpenAI-compatible image models, ordinary Codex requests, AuthManager selection, selector, scheduler, monitoring, quota warning, account binding, or request logging.
- Do not add a post-connection network timeout.
- Keep the upstream `internal/runtime/executor/codex_openai_images.go` image behavior identical to `upstream/main`. The current local Codex executor intentionally retains an older `cacheHelper` contract, so the only permitted source adaptation in that upstream file is renaming its four cache-helper calls to an image-scoped compatibility method; place the compatibility method and upstream identity-confusion dependency closure in `codex_openai_images_compat.go` so ordinary Codex requests remain untouched.
- Use test-first red-green cycles for every production behavior change.
- Run `gofmt` after Go changes and the repository-required full test and server build before completion.

---

### Task 1: Restore and Gate the Upstream Codex Image Executor

**Files:**
- Create from upstream: `internal/runtime/executor/codex_openai_images.go`
- Create for local API compatibility: `internal/runtime/executor/codex_openai_images_compat.go`
- Create and adapt for target-only coverage: `internal/runtime/executor/codex_openai_images_test.go`
- Create from upstream: `internal/runtime/executor/codex_openai_images_extract_test.go`
- Modify: `internal/runtime/executor/codex_executor.go`
- Modify: `internal/runtime/executor/codex_executor_imagegen_test.go`

**Interfaces:**
- Consumes: `CodexExecutor.Execute`, `CodexExecutor.ExecuteStream`, `cliproxyexecutor.Request`, `cliproxyexecutor.Options`, `helps.PayloadRequestPath`.
- Produces: `isCodexGPTImage2Request(req cliproxyexecutor.Request, opts cliproxyexecutor.Options) bool`, plus the upstream `executeOpenAIImage` and `executeOpenAIImageStream` methods used by Codex dispatch.

- [ ] **Step 1: Verify the branch and upstream reference before writing tests**

Run:

```bash
git status --short --branch
git rev-parse upstream/main
```

Expected:

```text
## sol-main
26d45fd46a2d2911adef14772465131066dae465
```

- [ ] **Step 2: Read the complete upstream executor and tests before adapting them**

Run:

```bash
git show upstream/main:internal/runtime/executor/codex_openai_images.go
git show upstream/main:internal/runtime/executor/codex_openai_images_test.go
git show upstream/main:internal/runtime/executor/codex_openai_images_extract_test.go
```

Expected: all three blobs are readable. Confirm that direct endpoints are `/images/generations` and `/images/edits`, and that `codexIsDirectOpenAIImageModel` contains both `gpt-image-1.5` and `gpt-image-2` only as shared internal support.

- [ ] **Step 3: Write the failing target-dispatch test**

Append this table-driven test to `internal/runtime/executor/codex_executor_imagegen_test.go`:

```go
func TestIsCodexGPTImage2Request(t *testing.T) {
	imageOptions := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-image"),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/generations",
		},
	}

	tests := []struct {
		name  string
		model string
		opts  cliproxyexecutor.Options
		want  bool
	}{
		{name: "GPT Image 2", model: "gpt-image-2", opts: imageOptions, want: true},
		{name: "prefixed GPT Image 2", model: "codex/gpt-image-2", opts: imageOptions, want: true},
		{name: "GPT Image 1.5 remains isolated", model: "gpt-image-1.5", opts: imageOptions, want: false},
		{name: "ordinary Codex request", model: "gpt-image-2", opts: cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCodexGPTImage2Request(cliproxyexecutor.Request{Model: tt.model}, tt.opts); got != tt.want {
				t.Fatalf("isCodexGPTImage2Request(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}
```

Add imports for `cliproxyexecutor` and `sdktranslator` using their existing repository aliases.

Also add the base-model configuration test that exercises the restored
`resolveGPTImage2BaseModel` method:

```go
func TestResolveGPTImage2BaseModel(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{name: "default", cfg: &config.Config{}, want: "gpt-5.4-mini"},
		{name: "configured", cfg: &config.Config{SDKConfig: config.SDKConfig{GPTImage2BaseModel: "gpt-5.6-luna"}}, want: "gpt-5.6-luna"},
		{name: "reject non GPT value", cfg: &config.Config{SDKConfig: config.SDKConfig{GPTImage2BaseModel: "claude-sonnet"}}, want: "gpt-5.4-mini"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewCodexExecutor(tt.cfg).resolveGPTImage2BaseModel(); got != tt.want {
				t.Fatalf("resolveGPTImage2BaseModel() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

Use the existing `internal/config` import name in this test file.

- [ ] **Step 4: Restore the upstream tests to the worktree and narrow model fixtures to GPT Image 2**

Restore these exact upstream test blobs:

```bash
git restore --source upstream/main --worktree -- internal/runtime/executor/codex_openai_images_test.go internal/runtime/executor/codex_openai_images_extract_test.go
```

In `codex_openai_images_test.go`, change only test request fixtures and assertions that name `gpt-image-1.5` or `codex/gpt-image-1.5` to `gpt-image-2` or `codex/gpt-image-2`. Do not alter the test servers, expected endpoints, headers, payload fields, streaming assertions, or response assertions.

- [ ] **Step 5: Run the executor tests and verify RED**

Run:

```bash
go test ./internal/runtime/executor -run 'TestIsCodexGPTImage2Request|TestResolveGPTImage2BaseModel|TestCodexExecutorDirectOpenAIImage|TestCodexExtractImageResults' -count=1
```

Expected: FAIL to compile because `isCodexGPTImage2Request`, `codexOpenAIImageTestOptions`, or restored image-executor functions are undefined. The failure must be caused by the missing production executor, not a malformed test.

- [ ] **Step 6: Restore the complete upstream production executor and add its image-scoped compatibility boundary**

Restore the exact upstream blob:

```bash
git restore --source upstream/main --worktree -- internal/runtime/executor/codex_openai_images.go
```

The local `cacheHelper` has a deliberately preserved five-argument contract, while the upstream image executor expects the newer seven-argument identity-confusion contract. Add `codex_openai_images_compat.go` containing the upstream identity-confusion state and transforms plus `codexOpenAIImageCacheHelper`, which implements the newer contract without changing the existing `cacheHelper` or ordinary Codex paths.

Rename only the four image file calls from `e.cacheHelper(...)` to `e.codexOpenAIImageCacheHelper(...)`.

Verify that this is the complete production-file deviation:

```bash
git diff --unified=0 upstream/main -- internal/runtime/executor/codex_openai_images.go
```

Expected: exactly four one-line call-site renames and no other differences.

- [ ] **Step 7: Add the target-only dispatch guard and dispatch calls**

Add this helper to `internal/runtime/executor/codex_executor.go` near `Execute`:

```go
func isCodexGPTImage2Request(req cliproxyexecutor.Request, opts cliproxyexecutor.Options) bool {
	if !isCodexOpenAIImageRequest(opts) {
		return false
	}
	return codexOpenAIImageBaseModel(req.Model) == codexDefaultImageToolModel
}
```

In `CodexExecutor.Execute`, after the compact-request branch and before normal Codex translation, add:

```go
if isCodexGPTImage2Request(req, opts) {
	return e.executeOpenAIImage(ctx, auth, req, opts)
}
```

In `CodexExecutor.ExecuteStream`, after the compact-request branch and before normal Codex translation, add:

```go
if isCodexGPTImage2Request(req, opts) {
	return e.executeOpenAIImageStream(ctx, auth, req, opts)
}
```

- [ ] **Step 8: Format and verify GREEN for the executor**

Run:

```bash
gofmt -w internal/runtime/executor/codex_openai_images_test.go internal/runtime/executor/codex_openai_images_extract_test.go internal/runtime/executor/codex_executor.go internal/runtime/executor/codex_executor_imagegen_test.go
go test ./internal/runtime/executor -run 'TestIsCodexGPTImage2Request|TestResolveGPTImage2BaseModel|TestCodexExecutorDirectOpenAIImage|TestCodexExtractImageResults' -count=1
```

Expected: PASS.

- [ ] **Step 9: Verify the upstream production behavior diff remains limited to compatibility call-site names**

Run:

```bash
git diff --unified=0 upstream/main -- internal/runtime/executor/codex_openai_images.go
```

Expected: exactly four one-line `cacheHelper` to `codexOpenAIImageCacheHelper` renames.

- [ ] **Step 10: Commit the executor deliverable**

```bash
git add internal/runtime/executor/codex_openai_images.go internal/runtime/executor/codex_openai_images_compat.go internal/runtime/executor/codex_openai_images_test.go internal/runtime/executor/codex_openai_images_extract_test.go internal/runtime/executor/codex_executor.go internal/runtime/executor/codex_executor_imagegen_test.go
git commit -m "feat(codex): restore GPT Image 2 image executor"
```

---

### Task 2: Route Only GPT Image 2 Through the Image-Aware AuthManager Path

**Files:**
- Modify: `sdk/api/handlers/openai/openai_images_handlers.go`
- Modify: `sdk/api/handlers/openai/openai_images_handlers_test.go`

**Interfaces:**
- Consumes: `imagesModelBase`, `buildOpenAICompatImagesJSONRequest`, `buildOpenAICompatImagesMultipartRequest`, `handleRoutedImages`, `ExecuteImageWithAuthManager`, and `ExecuteImageStreamWithAuthManager`.
- Produces: `isCodexImagesToolModel(model string) bool`, intentionally matching only `gpt-image-2` in this local branch.

- [ ] **Step 1: Change the existing generation test to describe the required routed behavior**

Rename `TestImagesGenerationsDefaultGPTImage2UsesResponsesBaseModel` to `TestImagesGenerationsDefaultGPTImage2UsesImageExecutor`.

Change `imagesResponsesCaptureExecutor.Execute` to capture `req` and `opts`, then return an OpenAI Images response:

```go
func (e *imagesResponsesCaptureExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.calls++
	e.model = req.Model
	e.sourceFormat = opts.SourceFormat.String()
	e.payload = bytes.Clone(req.Payload)
	return coreexecutor.Response{Payload: []byte(`{"created":1704067200,"data":[{"b64_json":"aW1hZ2U="}]}`)}, nil
}
```

Change the test assertions to:

```go
if executor.model != "gpt-image-2" {
	t.Fatalf("model = %q, want %q", executor.model, "gpt-image-2")
}
if executor.sourceFormat != "openai-image" {
	t.Fatalf("source format = %q, want %q", executor.sourceFormat, "openai-image")
}
if got := gjson.GetBytes(executor.payload, "prompt").String(); got != "draw a cat" {
	t.Fatalf("prompt = %q, want original image prompt; payload=%s", got, string(executor.payload))
}
```

Keep the response assertion for `data.0.b64_json`.

- [ ] **Step 2: Add a handler-level isolation test**

Add:

```go
func TestCodexImagesToolModelOnlyMatchesGPTImage2(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-image-2", want: true},
		{model: "codex/gpt-image-2", want: true},
		{model: "gpt-image-1.5", want: false},
		{model: "codex/gpt-image-1.5", want: false},
		{model: "grok-imagine-image", want: false},
	}

	for _, tt := range tests {
		if got := isCodexImagesToolModel(tt.model); got != tt.want {
			t.Fatalf("isCodexImagesToolModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}
```

- [ ] **Step 3: Run the handler tests and verify RED**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestImagesGenerationsDefaultGPTImage2UsesImageExecutor|TestCodexImagesToolModelOnlyMatchesGPTImage2' -count=1
```

Expected: FAIL because `isCodexImagesToolModel` is undefined and the current handler sends `gpt-5.4-mini` with source format `openai-response`.

- [ ] **Step 4: Add the GPT Image 2-only model predicate**

Add beside `isSupportedImagesModel`:

```go
func isCodexImagesToolModel(model string) bool {
	return imagesModelBase(model) == defaultImagesToolModel
}
```

Update `isSupportedImagesModel` to call this predicate instead of comparing the base model inline.

- [ ] **Step 5: Route generation requests before non-Codex image branches**

After extracting `responseFormat` and `stream`, and before the XAI branch, add:

```go
if isCodexImagesToolModel(imageModel) {
	imageReq := buildOpenAICompatImagesJSONRequest(rawJSON, imageModel, stream)
	h.handleRoutedImages(c, imageReq, imageModel, stream)
	return
}
```

Leave the existing XAI, OpenAI-compatible, and Responses fallback blocks unchanged.

- [ ] **Step 6: Route multipart edit requests without changing other edit branches**

After extracting `responseFormat` and `stream`, and before the XAI branch, add:

```go
if isCodexImagesToolModel(imageModel) {
	imageReq, contentType, errBuild := buildOpenAICompatImagesMultipartRequest(form, imageModel, stream)
	if errBuild != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", errBuild),
				Type:    "invalid_request_error",
			},
		})
		return
	}
	c.Request.Header.Set("Content-Type", contentType)
	h.handleRoutedImages(c, imageReq, imageModel, stream)
	return
}
```

- [ ] **Step 7: Route JSON edit requests without changing other edit branches**

After extracting `responseFormat` and `stream`, and before the XAI branch, add:

```go
if isCodexImagesToolModel(imageModel) {
	imageReq := buildOpenAICompatImagesJSONRequest(rawJSON, imageModel, stream)
	h.handleRoutedImages(c, imageReq, imageModel, stream)
	return
}
```

- [ ] **Step 8: Format and verify GREEN for handler routing**

Run:

```bash
gofmt -w sdk/api/handlers/openai/openai_images_handlers.go sdk/api/handlers/openai/openai_images_handlers_test.go
go test ./sdk/api/handlers/openai -run 'TestImagesGenerationsDefaultGPTImage2UsesImageExecutor|TestCodexImagesToolModelOnlyMatchesGPTImage2|Images' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit the handler deliverable**

```bash
git add sdk/api/handlers/openai/openai_images_handlers.go sdk/api/handlers/openai/openai_images_handlers_test.go
git commit -m "fix(openai): route GPT Image 2 through image executor"
```

---

### Task 3: Prove Upstream Fidelity and Local Regression Safety

**Files:**
- Verify only; no expected production edits.

**Interfaces:**
- Consumes: all deliverables from Tasks 1 and 2.
- Produces: fresh test, build, diff, and Git evidence for completion.

- [ ] **Step 1: Verify the upstream executor differs only at the compatibility call sites**

Run:

```bash
git diff --unified=0 upstream/main -- internal/runtime/executor/codex_openai_images.go
echo upstream_executor_behavior_synced=yes
```

Expected: four one-line cache-helper call-site renames followed by `upstream_executor_behavior_synced=yes`.

- [ ] **Step 2: Run focused executor and handler packages**

Run:

```bash
go test ./internal/runtime/executor -run 'CodexOpenAIImage|GPTImage2|CodexExtractImageResults' -count=1
go test ./sdk/api/handlers/openai -run 'Images|GPTImage2|CodexImagesToolModel' -count=1
go test ./internal/runtime/executor ./sdk/api/handlers/openai -count=1
```

Expected: all commands exit `0`.

- [ ] **Step 3: Run the full repository test suite**

Run:

```bash
env GOCACHE=/tmp/cliproxy-gpt-image-2-go-cache GOMODCACHE=/tmp/cliproxy-gpt-image-2-go-mod-cache go test ./... -count=1
```

Expected: exit code `0` with no failing package.

- [ ] **Step 4: Run the required server build**

Run:

```bash
env GOCACHE=/tmp/cliproxy-gpt-image-2-go-cache GOMODCACHE=/tmp/cliproxy-gpt-image-2-go-mod-cache go build -o /tmp/cli-proxy-api-build ./cmd/server
```

Expected: exit code `0` and `/tmp/cli-proxy-api-build` exists.

- [ ] **Step 5: Verify formatting, patch integrity, and sensitive material**

Run:

```bash
gofmt -w internal/runtime/executor/codex_openai_images.go internal/runtime/executor/codex_openai_images_compat.go internal/runtime/executor/codex_openai_images_test.go internal/runtime/executor/codex_openai_images_extract_test.go internal/runtime/executor/codex_executor.go internal/runtime/executor/codex_executor_imagegen_test.go sdk/api/handlers/openai/openai_images_handlers.go sdk/api/handlers/openai/openai_images_handlers_test.go
git diff --check HEAD~2..HEAD
make -C .. codex-sensitive-scan
```

Expected: all commands exit `0`.

- [ ] **Step 6: Audit the final production diff and branch state**

Run:

```bash
git diff --name-status 8e096c5737f355255c73b8935b905bc32e6b6c70..HEAD
git status --short --branch
git log -4 --oneline --decorate
```

Expected production changes are limited to:

```text
internal/runtime/executor/codex_openai_images.go
internal/runtime/executor/codex_openai_images_compat.go
internal/runtime/executor/codex_executor.go
sdk/api/handlers/openai/openai_images_handlers.go
```

Expected test changes are limited to:

```text
internal/runtime/executor/codex_openai_images_test.go
internal/runtime/executor/codex_openai_images_extract_test.go
internal/runtime/executor/codex_executor_imagegen_test.go
sdk/api/handlers/openai/openai_images_handlers_test.go
```

Expected branch state:

```text
## sol-main
```

No uncommitted files may remain.
