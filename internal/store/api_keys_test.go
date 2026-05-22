package store

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
)

func TestAPIKeyStoreIsPostgresOnly(t *testing.T) {
	if _, ok := any(&PostgresStore{}).(apikeys.Store); !ok {
		t.Fatal("PostgresStore must implement apikeys.Store")
	}
	if _, ok := any(&MySQLStore{}).(apikeys.Store); ok {
		t.Fatal("MySQLStore must not implement apikeys.Store")
	}
}
