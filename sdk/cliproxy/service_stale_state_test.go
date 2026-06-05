package cliproxy

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestServiceApplyCoreAuthAddOrUpdate_DeleteReAddDoesNotInheritStaleRuntimeState(t *testing.T) {
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}

	authID := "service-stale-state-auth"
	modelID := "stale-model"
	lastRefreshedAt := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	nextRefreshAfter := lastRefreshedAt.Add(30 * time.Minute)

	t.Cleanup(func() {
		GlobalModelRegistry().UnregisterClient(authID)
	})

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:               authID,
		Provider:         "claude",
		Status:           coreauth.StatusActive,
		LastRefreshedAt:  lastRefreshedAt,
		NextRefreshAfter: nextRefreshAfter,
		ModelStates: map[string]*coreauth.ModelState{
			modelID: {
				Quota: coreauth.QuotaState{BackoffLevel: 7},
			},
		},
	})

	service.applyCoreAuthRemoval(context.Background(), authID)

	if _, ok := service.coreManager.GetByID(authID); ok {
		t.Fatalf("expected auth %q to be removed from runtime state", authID)
	}

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:       authID,
		Provider: "claude",
		Status:   coreauth.StatusActive,
	})

	updated, ok := service.coreManager.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected re-added auth to be present")
	}
	if updated.Disabled {
		t.Fatalf("expected re-added auth to be active")
	}
	if !updated.LastRefreshedAt.IsZero() {
		t.Fatalf("expected LastRefreshedAt to reset on delete -> re-add, got %v", updated.LastRefreshedAt)
	}
	if !updated.NextRefreshAfter.IsZero() {
		t.Fatalf("expected NextRefreshAfter to reset on delete -> re-add, got %v", updated.NextRefreshAfter)
	}
	if len(updated.ModelStates) != 0 {
		t.Fatalf("expected ModelStates to reset on delete -> re-add, got %d entries", len(updated.ModelStates))
	}
	if models := registry.GetGlobalRegistry().GetModelsForClient(authID); len(models) == 0 {
		t.Fatalf("expected re-added auth to re-register models in global registry")
	}
}

func TestServiceApplyCoreAuthAddOrUpdate_ResetsSessionAffinityBindings(t *testing.T) {
	selector := coreauth.NewSessionAffinitySelector(&coreauth.FillFirstSelector{})
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, selector, nil),
	}

	oldAuth := &coreauth.Auth{
		ID:         "auth-old",
		Provider:   "claude",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"priority": "1"},
	}
	newAuth := &coreauth.Auth{
		ID:         "auth-new",
		Provider:   "claude",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"priority": "0"},
	}
	service.applyCoreAuthAddOrUpdate(context.Background(), oldAuth)
	service.applyCoreAuthAddOrUpdate(context.Background(), newAuth)

	payload := []byte(`{"metadata":{"user_id":"user_xxx_account__session_ac980658-63bd-4fb3-97ba-8da64cb1e344"}}`)
	opts := cliproxyexecutor.Options{OriginalRequest: payload}
	auths := []*coreauth.Auth{oldAuth.Clone(), newAuth.Clone()}

	first, err := selector.Pick(context.Background(), "claude", "claude-3", opts, auths)
	if err != nil {
		t.Fatalf("initial Pick() error = %v", err)
	}
	if first == nil || first.ID != oldAuth.ID {
		t.Fatalf("initial Pick() auth.ID = %v, want %q", first, oldAuth.ID)
	}

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:         newAuth.ID,
		Provider:   "claude",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"priority": "2"},
	})

	updatedNew, ok := service.coreManager.GetByID(newAuth.ID)
	if !ok || updatedNew == nil {
		t.Fatalf("expected updated auth to exist")
	}

	second, err := selector.Pick(context.Background(), "claude", "claude-3", opts, []*coreauth.Auth{oldAuth.Clone(), updatedNew.Clone()})
	if err != nil {
		t.Fatalf("post-update Pick() error = %v", err)
	}
	if second == nil || second.ID != newAuth.ID {
		t.Fatalf("post-update Pick() auth.ID = %v, want %q", second, newAuth.ID)
	}
}
