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

func TestRemovedProviderManagementRoutes(t *testing.T) {
	source := readRepositoryFile(t, "internal", "api", "server.go")

	for _, forbidden := range []string{
		`"/ampcode`,
		`"/openai-compatibility"`,
		`"/vertex-api-key"`,
		`"/vertex/import"`,
		"ampModule",
		"OpenAI-compat",
		"Vertex-compat",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("server.go still contains removed provider backend logic %q", forbidden)
		}
	}
}

func TestRemovedProviderRuntimeSynthesis(t *testing.T) {
	source := readRepositoryFile(t, "internal", "watcher", "synthesizer", "config.go")

	for _, forbidden := range []string{
		"synthesizeOpenAICompat",
		"synthesizeVertexCompat",
		"OpenAI-compat",
		"Vertex-compat",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("config synthesizer still contains removed provider backend logic %q", forbidden)
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
