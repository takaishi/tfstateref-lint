package tfstateref

import "testing"

func TestBuildStateURL_Errors(t *testing.T) {
	tests := []struct {
		name        string
		backendType string
		config      map[string]any
	}{
		{"s3 missing key", "s3", map[string]any{"bucket": "b"}},
		{"s3 missing bucket", "s3", map[string]any{"key": "k"}},
		{"gcs missing bucket", "gcs", map[string]any{"prefix": "p"}},
		{"azurerm missing container", "azurerm", map[string]any{
			"resource_group_name":  "rg",
			"storage_account_name": "sa",
			"key":                  "k",
		}},
		{"remote missing organization", "remote", map[string]any{
			"workspaces": map[string]any{"name": "w"},
		}},
		{"remote missing workspaces", "remote", map[string]any{"organization": "org"}},
		{"remote empty workspaces", "remote", map[string]any{
			"organization": "org",
			"workspaces":   map[string]any{},
		}},
		{"local missing path", "local", map[string]any{}},
		{"http missing address", "http", map[string]any{}},
		{"unsupported backend", "consul", map[string]any{"path": "p"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if url, err := buildStateURL(tt.backendType, tt.config, "", "."); err == nil {
				t.Errorf("expected error, got url %q", url)
			}
		})
	}
}
