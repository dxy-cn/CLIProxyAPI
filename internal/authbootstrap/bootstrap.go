package authbootstrap

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// Options controls one-time auth JSON bootstrap into the configured token store.
type Options struct {
	Dir       string
	File      string
	Overwrite bool
}

// Result summarizes an auth bootstrap run without exposing credential contents.
type Result struct {
	Imported int
	Skipped  int
}

// Import copies local auth JSON files into target. Existing IDs are skipped unless Overwrite is true.
func Import(ctx context.Context, target coreauth.Store, opts Options) (Result, error) {
	var result Result
	if target == nil {
		return result, fmt.Errorf("auth bootstrap: target store is nil")
	}
	sources := make([]string, 0, 2)
	if dir := strings.TrimSpace(opts.Dir); dir != "" {
		sources = append(sources, dir)
	}
	if file := strings.TrimSpace(opts.File); file != "" {
		sources = append(sources, file)
	}
	if len(sources) == 0 {
		return result, nil
	}

	existing, err := existingIDs(ctx, target)
	if err != nil {
		return result, err
	}
	for _, source := range sources {
		imported, skipped, errImport := importSource(ctx, target, existing, source, opts.Overwrite)
		result.Imported += imported
		result.Skipped += skipped
		if errImport != nil {
			return result, errImport
		}
	}
	return result, nil
}

func existingIDs(ctx context.Context, target coreauth.Store) (map[string]struct{}, error) {
	auths, err := target.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth bootstrap: list target store: %w", err)
	}
	ids := make(map[string]struct{}, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		if id := strings.TrimSpace(auth.ID); id != "" {
			ids[filepath.ToSlash(filepath.Clean(id))] = struct{}{}
		}
	}
	return ids, nil
}

func importSource(ctx context.Context, target coreauth.Store, existing map[string]struct{}, source string, overwrite bool) (imported, skipped int, err error) {
	store := sdkauth.NewFileTokenStore()
	if strings.HasSuffix(strings.ToLower(source), ".json") {
		store.SetBaseDir(filepath.Dir(source))
	} else {
		store.SetBaseDir(source)
	}
	auths, err := store.List(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("auth bootstrap: read source %s: %w", source, err)
	}
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		if strings.HasSuffix(strings.ToLower(source), ".json") && filepath.Clean(auth.ID) != filepath.Base(source) {
			continue
		}
		id := filepath.ToSlash(filepath.Clean(auth.ID))
		if _, ok := existing[id]; ok && !overwrite {
			skipped++
			continue
		}
		copyAuth := auth.Clone()
		copyAuth.ID = id
		copyAuth.FileName = id
		if copyAuth.Attributes != nil {
			delete(copyAuth.Attributes, "path")
			if len(copyAuth.Attributes) == 0 {
				copyAuth.Attributes = nil
			}
		}
		if _, errSave := target.Save(ctx, copyAuth); errSave != nil {
			return imported, skipped, fmt.Errorf("auth bootstrap: save %s: %w", id, errSave)
		}
		existing[id] = struct{}{}
		imported++
	}
	return imported, skipped, nil
}
