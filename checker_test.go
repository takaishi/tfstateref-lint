package tfstateref

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles writes the given path → content map under dir, creating parent
// directories as needed.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const testTFState = `{
  "version": 4,
  "terraform_version": "1.5.0",
  "serial": 1,
  "lineage": "test",
  "outputs": {
    "vpc_id": {"value": "vpc-123", "type": "string"}
  },
  "resources": []
}`

func TestRun_DetectsMissingOutput(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"base/terraform.tfstate": testTFState,
		"app/remote_state.tf": `
data "terraform_remote_state" "base" {
  backend = "local"
  config = {
    path = "../base/terraform.tfstate"
  }
}
`,
		"app/main.tf": `
locals {
  ok      = data.terraform_remote_state.base.outputs.vpc_id
  missing = data.terraform_remote_state.base.outputs.no_such_output
}
`,
	})

	c := NewChecker([]string{dir}, false, true)
	if err := c.Build(); err != nil {
		t.Fatal(err)
	}
	result := c.Run(context.Background())

	if result.OK() {
		t.Fatal("expected lint errors, got OK")
	}
	if result.StateExistence.Checked != 1 || len(result.StateExistence.Errors) != 0 {
		t.Errorf("state_existence = %+v, want 1 checked / 0 errors", result.StateExistence)
	}
	if result.OutputReference.Checked != 2 || len(result.OutputReference.Errors) != 1 {
		t.Fatalf("output_reference = %+v, want 2 checked / 1 error", result.OutputReference)
	}
	if msg := result.OutputReference.Errors[0].Message; !strings.Contains(msg, "no_such_output") {
		t.Errorf("error message %q does not mention no_such_output", msg)
	}
}

func TestRun_DetectsMissingState(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"app/remote_state.tf": `
data "terraform_remote_state" "base" {
  backend = "local"
  config = {
    path = "../base/terraform.tfstate"
  }
}
`,
		"app/main.tf": `
locals {
  v = data.terraform_remote_state.base.outputs.vpc_id
}
`,
	})

	c := NewChecker([]string{dir}, false, true)
	if err := c.Build(); err != nil {
		t.Fatal(err)
	}
	result := c.Run(context.Background())

	if result.OK() {
		t.Fatal("expected lint errors, got OK")
	}
	if result.StateExistence.Checked != 1 || len(result.StateExistence.Errors) != 1 {
		t.Errorf("state_existence = %+v, want 1 checked / 1 error", result.StateExistence)
	}
	// the output reference is skipped (state unavailable), not double-reported
	if len(result.OutputReference.Errors) != 0 {
		t.Errorf("output_reference errors = %+v, want none", result.OutputReference.Errors)
	}
}

func TestRun_AllOK(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"base/terraform.tfstate": testTFState,
		"app/remote_state.tf": `
data "terraform_remote_state" "base" {
  backend = "local"
  config = {
    path = "../base/terraform.tfstate"
  }
}
`,
		"app/main.tf": `
locals {
  v = data.terraform_remote_state.base.outputs.vpc_id
}
`,
	})

	c := NewChecker([]string{dir}, false, true)
	if err := c.Build(); err != nil {
		t.Fatal(err)
	}
	result := c.Run(context.Background())

	if !result.OK() {
		t.Fatalf("expected OK, got errors: %+v", result.Errors)
	}
	if result.OutputReference.Checked != 1 {
		t.Errorf("output_reference checked = %d, want 1", result.OutputReference.Checked)
	}
}

func TestBuildRemoteState_CollectsFromAnyTFFile(t *testing.T) {
	dir := t.TempDir()
	content := `
data "terraform_remote_state" "base" {
  backend = "s3"
  config = {
    bucket = "amzn-s3-demo-bucket"
    key    = "env/base/terraform.tfstate"
  }
}
`
	// terraform_remote_state blocks are not required to live in remote_state.tf
	if err := os.WriteFile(filepath.Join(dir, "data.tf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewChecker([]string{dir}, false, true)
	if err := c.Build(); err != nil {
		t.Fatal(err)
	}

	wantURL := "s3://amzn-s3-demo-bucket/env/base/terraform.tfstate"
	if got := c.labelToStateURL[dir+"\tbase"]; got != wantURL {
		t.Errorf("labelToStateURL[%q] = %q, want %q", dir+"\tbase", got, wantURL)
	}
	if len(c.stateRefs) != 1 || c.stateRefs[0].URL != wantURL {
		t.Errorf("stateRefs = %+v, want single entry with URL %q", c.stateRefs, wantURL)
	}
}

func TestNewChecker_DoesNotMutateInput(t *testing.T) {
	dirs := []string{"terraform"}
	NewChecker(dirs, false, true)
	if dirs[0] != "terraform" {
		t.Errorf("input slice was mutated: %q", dirs[0])
	}
}
