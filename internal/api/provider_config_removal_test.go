package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readRepositoryFile(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate current test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	path := filepath.Join(append([]string{root}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestProviderManagementRouteBoundary(t *testing.T) {
	source := readRepositoryFile(t, "internal", "api", "server.go")

	for _, forbidden := range []string{
		`"/ampcode`,
		`"/vertex/import"`,
		"ampModule",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("server.go contains excluded provider backend logic %q", forbidden)
		}
	}

	for _, required := range []string{
		`"/xai-api-key"`,
		`"/openai-compatibility"`,
		`"/vertex-api-key"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("server.go is missing restored upstream provider route %q", required)
		}
	}
}

func TestRestoredProviderRuntimeSynthesis(t *testing.T) {
	source := readRepositoryFile(t, "internal", "watcher", "synthesizer", "config.go")

	for _, required := range []string{
		"synthesizeXAIKeys",
		"synthesizeOpenAICompat",
		"synthesizeVertexCompat",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("config synthesizer is missing restored upstream provider logic %q", required)
		}
	}
}

func TestRemovedVertexImportCommand(t *testing.T) {
	source := readRepositoryFile(t, "cmd", "server", "main.go")

	for _, forbidden := range []string{
		"vertex-import",
		"DoVertexImport",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("main.go still contains removed Vertex import command logic %q", forbidden)
		}
	}
}
