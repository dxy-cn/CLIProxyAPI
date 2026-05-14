package wsrelay

import "testing"

func TestNewManagerEnablesCompression(t *testing.T) {
	mgr := NewManager(Options{})
	if !mgr.upgrader.EnableCompression {
		t.Fatal("wsrelay upgrader must enable compression")
	}
}
