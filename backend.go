package tfstateref

import (
	"fmt"
	"path"
	"path/filepath"
)

const (
	defaultWorkspace          = "default"
	defaultWorkspaceKeyPrefix = "env:"
	defaultTFEHostname        = "app.terraform.io"
)

// buildStateURL builds a URL that tfstate.ReadURL can read, from a
// terraform_remote_state block's backend type and config. The workspace
// handling mirrors terraform's backend implementations (and tfstate-lookup's
// readXxxState functions). baseDir is the directory of the .tf file, used to
// resolve relative paths for the local backend.
func buildStateURL(backendType string, config map[string]any, workspace, baseDir string) (string, error) {
	if workspace == "" {
		workspace = defaultWorkspace
	}

	switch backendType {
	case "s3":
		bucket := cfgString(config, "bucket")
		key := cfgString(config, "key")
		if bucket == "" || key == "" {
			return "", fmt.Errorf("s3 backend requires bucket and key")
		}
		if workspace != defaultWorkspace {
			prefix := cfgStringDefault(config, "workspace_key_prefix", defaultWorkspaceKeyPrefix)
			key = path.Join(prefix, workspace, key)
		}
		return fmt.Sprintf("s3://%s/%s", bucket, key), nil

	case "gcs":
		bucket := cfgString(config, "bucket")
		if bucket == "" {
			return "", fmt.Errorf("gcs backend requires bucket")
		}
		key := path.Join(cfgString(config, "prefix"), workspace+".tfstate")
		return fmt.Sprintf("gs://%s/%s", bucket, key), nil

	case "azurerm":
		resourceGroup := cfgString(config, "resource_group_name")
		account := cfgString(config, "storage_account_name")
		container := cfgString(config, "container_name")
		key := cfgString(config, "key")
		if resourceGroup == "" || account == "" || container == "" || key == "" {
			return "", fmt.Errorf("azurerm backend requires resource_group_name, storage_account_name, container_name and key")
		}
		if workspace != defaultWorkspace {
			// terraform's azurerm backend appends "env:<workspace>" to the key
			key = key + cfgStringDefault(config, "workspace_key_prefix", defaultWorkspaceKeyPrefix) + workspace
		}
		return fmt.Sprintf("azurerm://%s/%s/%s/%s", resourceGroup, account, container, key), nil

	case "remote":
		hostname := cfgStringDefault(config, "hostname", defaultTFEHostname)
		organization := cfgString(config, "organization")
		if organization == "" {
			return "", fmt.Errorf("remote backend requires organization")
		}
		workspaces, ok := config["workspaces"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("remote backend requires workspaces")
		}
		name := cfgString(workspaces, "name")
		prefix := cfgString(workspaces, "prefix")
		switch {
		case name != "":
			return fmt.Sprintf("remote://%s/%s/%s", hostname, organization, name), nil
		case prefix != "":
			return fmt.Sprintf("remote://%s/%s/%s", hostname, organization, prefix+workspace), nil
		default:
			return "", fmt.Errorf("remote backend requires workspaces.name or workspaces.prefix")
		}

	case "local":
		p := cfgString(config, "path")
		if p == "" {
			return "", fmt.Errorf("local backend requires path")
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(baseDir, p)
		}
		return p, nil

	case "http":
		address := cfgString(config, "address")
		if address == "" {
			return "", fmt.Errorf("http backend requires address")
		}
		return address, nil

	default:
		return "", fmt.Errorf("unsupported backend type %q", backendType)
	}
}

// cfgString returns config[key] as a string, or "" if absent or not a string.
func cfgString(config map[string]any, key string) string {
	if s, ok := config[key].(string); ok {
		return s
	}
	return ""
}

// cfgStringDefault returns config[key] as a string, or def if absent or empty.
func cfgStringDefault(config map[string]any, key, def string) string {
	if s := cfgString(config, key); s != "" {
		return s
	}
	return def
}
