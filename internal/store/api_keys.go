package store

import (
	"encoding/json"
	"os"

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

func extractAPIKeyRecordsFromFile(path string) ([]apikeys.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return apikeys.ExtractYAMLRecords(data)
}
