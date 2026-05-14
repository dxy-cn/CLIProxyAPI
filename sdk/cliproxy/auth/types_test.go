package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToolPrefixDisabled(t *testing.T) {
	var a *Auth
	if a.ToolPrefixDisabled() {
		t.Error("nil auth should return false")
	}

	a = &Auth{}
	if a.ToolPrefixDisabled() {
		t.Error("empty auth should return false")
	}

	a = &Auth{Metadata: map[string]any{"tool_prefix_disabled": true}}
	if !a.ToolPrefixDisabled() {
		t.Error("should return true when set to true")
	}

	a = &Auth{Metadata: map[string]any{"tool_prefix_disabled": "true"}}
	if !a.ToolPrefixDisabled() {
		t.Error("should return true when set to string 'true'")
	}

	a = &Auth{Metadata: map[string]any{"tool-prefix-disabled": true}}
	if !a.ToolPrefixDisabled() {
		t.Error("should return true with kebab-case key")
	}

	a = &Auth{Metadata: map[string]any{"tool_prefix_disabled": false}}
	if a.ToolPrefixDisabled() {
		t.Error("should return false when set to false")
	}
}

func TestEnsureIndexUsesCredentialIdentity(t *testing.T) {
	t.Parallel()

	geminiAuth := &Auth{
		Provider: "gemini",
		Attributes: map[string]string{
			"api_key": "shared-key",
			"source":  "config:gemini[abc123]",
		},
	}
	compatAuth := &Auth{
		Provider: "bohe",
		Attributes: map[string]string{
			"api_key":      "shared-key",
			"compat_name":  "bohe",
			"provider_key": "bohe",
			"source":       "config:bohe[def456]",
		},
	}
	geminiAltBase := &Auth{
		Provider: "gemini",
		Attributes: map[string]string{
			"api_key":  "shared-key",
			"base_url": "https://alt.example.com",
			"source":   "config:gemini[ghi789]",
		},
	}
	geminiDuplicate := &Auth{
		Provider: "gemini",
		Attributes: map[string]string{
			"api_key": "shared-key",
			"source":  "config:gemini[abc123-1]",
		},
	}

	geminiIndex := geminiAuth.EnsureIndex()
	compatIndex := compatAuth.EnsureIndex()
	altBaseIndex := geminiAltBase.EnsureIndex()
	duplicateIndex := geminiDuplicate.EnsureIndex()

	if geminiIndex == "" {
		t.Fatal("gemini index should not be empty")
	}
	if compatIndex == "" {
		t.Fatal("compat index should not be empty")
	}
	if altBaseIndex == "" {
		t.Fatal("alt base index should not be empty")
	}
	if duplicateIndex == "" {
		t.Fatal("duplicate index should not be empty")
	}
	if geminiIndex == compatIndex {
		t.Fatalf("shared api key produced duplicate auth_index %q", geminiIndex)
	}
	if geminiIndex == altBaseIndex {
		t.Fatalf("same provider/key with different base_url produced duplicate auth_index %q", geminiIndex)
	}
	if geminiIndex == duplicateIndex {
		t.Fatalf("duplicate config entries should be separated by source-derived seed, got %q", geminiIndex)
	}
}

func TestRecentRequestsSnapshotEmptyReturnsTwentyBuckets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).In(time.Local)
	a := &Auth{}

	got := a.RecentRequestsSnapshot(now)
	if len(got) != recentRequestBucketCount {
		t.Fatalf("len = %d, want %d", len(got), recentRequestBucketCount)
	}

	currentBucketID := now.Unix() / recentRequestBucketSeconds
	baseBucketID := currentBucketID - int64(recentRequestBucketCount-1)
	for i, bucket := range got {
		if bucket.Success != 0 || bucket.Failed != 0 {
			t.Fatalf("bucket[%d] counts = %d/%d, want 0/0", i, bucket.Success, bucket.Failed)
		}
		if strings.TrimSpace(bucket.Time) == "" {
			t.Fatalf("bucket[%d] time label is empty", i)
		}
		expectedBucketID := baseBucketID + int64(i)
		start := time.Unix(expectedBucketID*recentRequestBucketSeconds, 0).In(time.Local)
		end := start.Add(10 * time.Minute)
		expected := start.Format("15:04") + "-" + end.Format("15:04")
		if bucket.Time != expected {
			t.Fatalf("bucket[%d] time = %q, want %q", i, bucket.Time, expected)
		}
	}
}

func TestRecentRequestsSnapshotIncludesCounts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).In(time.Local)
	a := &Auth{}

	a.recordRecentRequest(now, true)
	a.recordRecentRequest(now, false)

	got := a.RecentRequestsSnapshot(now)
	if len(got) != recentRequestBucketCount {
		t.Fatalf("len = %d, want %d", len(got), recentRequestBucketCount)
	}

	newest := got[len(got)-1]
	if newest.Success != 1 || newest.Failed != 1 {
		t.Fatalf("newest bucket = success=%d failed=%d, want 1/1", newest.Success, newest.Failed)
	}
}

func TestRecentRequestsSnapshotBucketAdvanceMovesCounts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).In(time.Local)
	next := now.Add(10 * time.Minute)
	a := &Auth{}

	a.recordRecentRequest(now, true)
	a.recordRecentRequest(next, false)

	got := a.RecentRequestsSnapshot(next)
	if len(got) != recentRequestBucketCount {
		t.Fatalf("len = %d, want %d", len(got), recentRequestBucketCount)
	}

	secondNewest := got[len(got)-2]
	newest := got[len(got)-1]
	if secondNewest.Success != 1 || secondNewest.Failed != 0 {
		t.Fatalf("second newest bucket = success=%d failed=%d, want 1/0", secondNewest.Success, secondNewest.Failed)
	}
	if newest.Success != 0 || newest.Failed != 1 {
		t.Fatalf("newest bucket = success=%d failed=%d, want 0/1", newest.Success, newest.Failed)
	}
}

func TestStableIdentityUsesCodexChatGPTAccountID(t *testing.T) {
	t.Parallel()

	auth := &Auth{
		Provider: "codex",
		FileName: "codex-apache777@foxmail.com-pro.json",
		Metadata: map[string]any{
			"id_token": testJWT(t, map[string]any{
				"email": "apache777@foxmail.com",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "2fdb860f-9028-4f92-be37-195478c9cf8a",
				},
			}),
		},
	}

	got := auth.StableIdentity()
	want := "codex:chatgpt:2fdb860f-9028-4f92-be37-195478c9cf8a"
	if got != want {
		t.Fatalf("StableIdentity() = %q, want %q", got, want)
	}
}

func TestStableIdentityDoesNotFollowCodexFileName(t *testing.T) {
	t.Parallel()

	token := testJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-stable",
		},
	})
	before := &Auth{Provider: "codex", FileName: "codex-old.json", Metadata: map[string]any{"id_token": token}}
	after := &Auth{Provider: "codex", FileName: "codex-new.json", Metadata: map[string]any{"id_token": token}}

	if before.EnsureIndex() == after.EnsureIndex() {
		t.Fatalf("test setup must use file names that produce different auth_index values")
	}
	if before.StableIdentity() != after.StableIdentity() {
		t.Fatalf("same Codex account must keep one stable identity across file name changes: before=%q after=%q", before.StableIdentity(), after.StableIdentity())
	}
}

func TestStableIdentityUsesOAuthEmailForNonCodexProviders(t *testing.T) {
	t.Parallel()

	auth := &Auth{
		Provider: "claude",
		FileName: "claude-old.json",
		Metadata: map[string]any{
			"email": "User@Example.COM",
		},
	}
	renamed := auth.Clone()
	renamed.FileName = "claude-new.json"

	const want = "claude:oauth:user@example.com"
	if got := auth.StableIdentity(); got != want {
		t.Fatalf("StableIdentity() = %q, want %q", got, want)
	}
	if got := renamed.StableIdentity(); got != want {
		t.Fatalf("renamed StableIdentity() = %q, want %q", got, want)
	}
}

func TestStableIdentityIncludesOAuthProjectWhenPresent(t *testing.T) {
	t.Parallel()

	auth := &Auth{
		Provider: "gemini",
		Metadata: map[string]any{
			"email":      "user@example.com",
			"project_id": "project-a",
		},
	}

	const want = "gemini:oauth:user@example.com:project:project-a"
	if got := auth.StableIdentity(); got != want {
		t.Fatalf("StableIdentity() = %q, want %q", got, want)
	}
}

func TestStableIdentityUsesOAuthDeviceIDWhenEmailIsUnavailable(t *testing.T) {
	t.Parallel()

	auth := &Auth{
		Provider: "kimi",
		Metadata: map[string]any{
			"device_id": "device-123",
		},
	}

	const want = "kimi:oauth-device:device-123"
	if got := auth.StableIdentity(); got != want {
		t.Fatalf("StableIdentity() = %q, want %q", got, want)
	}
}

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
