package tfstateref

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseRemoteStateBlocks(t *testing.T) {
	content := `
data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = "example-terraform-state"
    key    = "env/network/terraform.tfstate"
    region = "ap-northeast-1"
  }
}

data "terraform_remote_state" "shared" {
  backend = "s3"
  config = {
    bucket = "example-terraform-state"
    key    = "env/shared/terraform.tfstate"
    region = "ap-northeast-1"
  }
}
`
	got := parseRemoteStateBlocks([]byte(content), "remote_state.tf")
	expected := []remoteStateEntry{
		{Label: "network", URL: "s3://example-terraform-state/env/network/terraform.tfstate"},
		{Label: "shared", URL: "s3://example-terraform-state/env/shared/terraform.tfstate"},
	}

	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestParseRemoteStateBlocks_MissingBucket(t *testing.T) {
	content := `
data "terraform_remote_state" "incomplete" {
  backend = "s3"
  config = {
    key    = "env/app/terraform.tfstate"
    region = "ap-northeast-1"
  }
}
`
	got := parseRemoteStateBlocks([]byte(content), "remote_state.tf")
	if len(got) != 0 {
		t.Errorf("expected no entries for missing bucket, got %d", len(got))
	}
}

func TestParseRemoteStateBlocks_Backends(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []remoteStateEntry
	}{
		{
			name: "s3 with workspace",
			content: `
data "terraform_remote_state" "app" {
  backend   = "s3"
  workspace = "stg"
  config = {
    bucket = "example-terraform-state"
    key    = "app/terraform.tfstate"
  }
}
`,
			expected: []remoteStateEntry{
				{Label: "app", URL: "s3://example-terraform-state/env:/stg/app/terraform.tfstate"},
			},
		},
		{
			name: "gcs",
			content: `
data "terraform_remote_state" "app" {
  backend = "gcs"
  config = {
    bucket = "example-terraform-state"
    prefix = "env"
  }
}
`,
			expected: []remoteStateEntry{
				{Label: "app", URL: "gs://example-terraform-state/env/default.tfstate"},
			},
		},
		{
			name: "azurerm",
			content: `
data "terraform_remote_state" "app" {
  backend = "azurerm"
  config = {
    resource_group_name  = "example-rg"
    storage_account_name = "examplestorage"
    container_name       = "tfstate"
    key                  = "app.terraform.tfstate"
  }
}
`,
			expected: []remoteStateEntry{
				{Label: "app", URL: "azurerm://example-rg/examplestorage/tfstate/app.terraform.tfstate"},
			},
		},
		{
			name: "remote with workspaces.name",
			content: `
data "terraform_remote_state" "app" {
  backend = "remote"
  config = {
    organization = "example-org"
    workspaces = {
      name = "app-production"
    }
  }
}
`,
			expected: []remoteStateEntry{
				{Label: "app", URL: "remote://app.terraform.io/example-org/app-production"},
			},
		},
		{
			name: "remote with hostname and workspaces.prefix",
			content: `
data "terraform_remote_state" "app" {
  backend   = "remote"
  workspace = "stg"
  config = {
    hostname     = "tfe.example.com"
    organization = "example-org"
    workspaces = {
      prefix = "app-"
    }
  }
}
`,
			expected: []remoteStateEntry{
				{Label: "app", URL: "remote://tfe.example.com/example-org/app-stg"},
			},
		},
		{
			name: "local relative path",
			content: `
data "terraform_remote_state" "app" {
  backend = "local"
  config = {
    path = "../app/terraform.tfstate"
  }
}
`,
			expected: []remoteStateEntry{
				{Label: "app", URL: "envs/app/terraform.tfstate"},
			},
		},
		{
			name: "http",
			content: `
data "terraform_remote_state" "app" {
  backend = "http"
  config = {
    address = "https://state.example.com/app/terraform.tfstate"
  }
}
`,
			expected: []remoteStateEntry{
				{Label: "app", URL: "https://state.example.com/app/terraform.tfstate"},
			},
		},
		{
			name: "unsupported backend is skipped",
			content: `
data "terraform_remote_state" "app" {
  backend = "consul"
  config = {
    path = "app/terraform.tfstate"
  }
}
`,
			expected: nil,
		},
		{
			name: "missing backend attribute is skipped",
			content: `
data "terraform_remote_state" "app" {
  config = {
    bucket = "example-terraform-state"
    key    = "app/terraform.tfstate"
  }
}
`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRemoteStateBlocks([]byte(tt.content), "envs/stg/remote_state.tf")
			if diff := cmp.Diff(tt.expected, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtractOutputReferences_DotNotation(t *testing.T) {
	content := `
locals {
  vpc_id = data.terraform_remote_state.network.outputs.vpc.vpc_id
  sg_id  = data.terraform_remote_state.storage.outputs.storage.security_group_id
}
`
	got := extractOutputReferences([]byte(content), "main.tf")

	gotSet := make(map[string]bool)
	for _, r := range got {
		gotSet[r.Label+"."+r.OutputName] = true
	}

	if len(got) != 2 {
		t.Errorf("expected 2 references, got %d: %+v", len(got), got)
	}
	// Full nested path should be extracted
	if !gotSet["network.vpc.vpc_id"] {
		t.Errorf("expected reference to network.vpc.vpc_id, got: %+v", got)
	}
	if !gotSet["storage.storage.security_group_id"] {
		t.Errorf("expected reference to storage.storage.security_group_id, got: %+v", got)
	}
}

func TestExtractOutputReferences_LookupPattern(t *testing.T) {
	content := `
locals {
  aaa = lookup(data.terraform_remote_state.workflow.outputs, "aaa", "mock_aaa")
}
`
	got := extractOutputReferences([]byte(content), "variables.tf")
	expected := []outputReference{
		{File: "variables.tf", Label: "workflow", OutputName: "aaa"},
	}

	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractOutputReferences_LookupWithDotNotation(t *testing.T) {
	content := `
locals {
  vpc_id = lookup(data.terraform_remote_state.network.outputs.vpc, "vpc_id", "mock_vpc_id")
}
`
	got := extractOutputReferences([]byte(content), "variables.tf")
	expected := []outputReference{
		{File: "variables.tf", Label: "network", OutputName: "vpc"},
	}

	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractOutputReferences_BracketNotation(t *testing.T) {
	content := `
locals {
  bbb = data.terraform_remote_state.network.outputs["bbb"]
}
`
	got := extractOutputReferences([]byte(content), "main.tf")
	expected := []outputReference{
		{File: "main.tf", Label: "network", OutputName: "bbb"},
	}

	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractOutputReferences_TryPattern(t *testing.T) {
	content := `
locals {
  ccc = try(data.terraform_remote_state.storage.outputs.cache.cache_alb_security_group_id, "")
}
`
	got := extractOutputReferences([]byte(content), "main.tf")
	expected := []outputReference{
		{File: "main.tf", Label: "storage", OutputName: "cache.cache_alb_security_group_id"},
	}

	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractOutputReferences_DynamicIndexStops(t *testing.T) {
	// A dynamic index ([0], [var.key]) cannot be verified statically, so path
	// building must stop there instead of skipping the index and appending the
	// following attributes (which would produce a nonexistent path).
	content := `
locals {
  id = data.terraform_remote_state.network.outputs.subnets[0].id
}
`
	got := extractOutputReferences([]byte(content), "main.tf")
	expected := []outputReference{
		{File: "main.tf", Label: "network", OutputName: "subnets"},
	}

	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractOutputReferences_Deduplication(t *testing.T) {
	content := `
locals {
  a = data.terraform_remote_state.shared.outputs.shared.attr1
  b = data.terraform_remote_state.shared.outputs.shared.attr2
}
`
	got := extractOutputReferences([]byte(content), "main.tf")
	// Each full path is unique, so both should be present
	if len(got) != 2 {
		t.Errorf("expected 2 references (different full paths), got %d: %+v", len(got), got)
	}
}

func TestExtractOutputReferences_NoReferences(t *testing.T) {
	content := `
locals {
  name = "test"
  value = var.something
}
`
	got := extractOutputReferences([]byte(content), "main.tf")
	if len(got) != 0 {
		t.Errorf("expected no references, got %d", len(got))
	}
}

func TestExtractOutputReferences_LocalAliasLookup(t *testing.T) {
	// A local aliases a remote_state output map and the map key is read
	// indirectly via lookup(local.X, "KEY").
	content := `
locals {
  batch_cluster = try(data.terraform_remote_state.workflow.outputs.batch_cluster, {})
  my_batch_sg_id = lookup(local.batch_cluster, "my_batch_sg_id", "")
}
`
	got := extractOutputReferences([]byte(content), "variables.tf")

	gotSet := make(map[string]bool)
	for _, r := range got {
		gotSet[r.Label+"."+r.OutputName] = true
	}

	// Both the aliased top-level output and the map key resolved through the
	// indirect reference should be checked.
	if !gotSet["workflow.batch_cluster"] {
		t.Errorf("expected reference to workflow.batch_cluster, got: %+v", got)
	}
	if !gotSet["workflow.batch_cluster.my_batch_sg_id"] {
		t.Errorf("expected reference to workflow.batch_cluster.my_batch_sg_id, got: %+v", got)
	}
}

func TestExtractOutputReferences_LocalAliasDotAccess(t *testing.T) {
	// Direct dot access via local.ALIAS.KEY (not lookup) is also resolved.
	content := `
locals {
  batch_cluster = data.terraform_remote_state.workflow.outputs.batch_cluster
  sg = local.batch_cluster.some_sg_id
}
`
	got := extractOutputReferences([]byte(content), "variables.tf")

	gotSet := make(map[string]bool)
	for _, r := range got {
		gotSet[r.Label+"."+r.OutputName] = true
	}
	if !gotSet["workflow.batch_cluster.some_sg_id"] {
		t.Errorf("expected reference to workflow.batch_cluster.some_sg_id, got: %+v", got)
	}
}

func TestExtractOutputReferences_LocalAliasUnknownLocal(t *testing.T) {
	// A lookup on a local not derived from remote_state is not treated as a
	// reference (avoids false positives).
	content := `
locals {
  plain = { a = "x" }
  v = lookup(local.plain, "a", "")
}
`
	got := extractOutputReferences([]byte(content), "variables.tf")
	if len(got) != 0 {
		t.Errorf("expected no references for non-remote_state local, got %d: %+v", len(got), got)
	}
}

func TestExtractOutputReferences_ModuleBlock(t *testing.T) {
	content := `
module "app" {
  source = "../../modules/app"
  vpc_id = data.terraform_remote_state.network.outputs.vpc.vpc_id
  aaa    = lookup(data.terraform_remote_state.workflow.outputs, "aaa", "mock")
}
`
	got := extractOutputReferences([]byte(content), "main.tf")
	expected := []outputReference{
		{File: "main.tf", Label: "network", OutputName: "vpc.vpc_id"},
		{File: "main.tf", Label: "workflow", OutputName: "aaa"},
	}

	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}
