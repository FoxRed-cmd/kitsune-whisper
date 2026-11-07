package servers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAdminConfigGoldenFixture decodes the open-ended /admin/config payload
// that the Server side validates too. Its keys come from the user's YAML, so
// the schema keeps additionalProperties and the fixture is the contract.
func TestAdminConfigGoldenFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "admin-config.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var config ServerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	processing, ok := config.Config["processing"].(map[string]any)
	if !ok {
		t.Fatalf("config processing = %#v", config.Config["processing"])
	}
	if processing["api_key"] != "***" {
		t.Fatalf("api_key = %#v, want the redaction placeholder", processing["api_key"])
	}
	if len(config.Redacted) != 1 || config.Redacted[0] != "server.processing.api_key" {
		t.Fatalf("redacted = %v", config.Redacted)
	}
	port, ok := config.Fields["server.port"]
	if !ok || port.Value != float64(8765) || port.Mode != "restart" {
		t.Fatalf("server.port field = %#v", port)
	}
	if _, ok := config.Fields["server.processing.api_key"]; !ok {
		t.Fatal("the redacted field is missing from fields")
	}
}
