package store

import cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"

func authDisabledStateFromMetadata(metadata map[string]any) (bool, cliproxyauth.Status) {
	disabled, _ := metadata["disabled"].(bool)
	if disabled {
		return true, cliproxyauth.StatusDisabled
	}
	return false, cliproxyauth.StatusActive
}

func syncAuthDisabledMetadata(auth *cliproxyauth.Auth) {
	if auth == nil || auth.Metadata == nil {
		return
	}
	auth.Metadata["disabled"] = auth.Disabled
}
