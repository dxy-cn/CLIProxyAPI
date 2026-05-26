package store

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
)

const defaultAPIKeyTable = "api_key_store"

func apiKeyTagsJSON(tags []string) string {
	data, err := json.Marshal(apikeys.NormalizeTags(tags))
	if err != nil {
		return "[]"
	}
	return string(data)
}

func parseAPIKeyTags(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil
	}
	return apikeys.NormalizeTags(tags)
}
