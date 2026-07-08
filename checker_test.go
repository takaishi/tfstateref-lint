package tfstateref

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRemoteState_CollectsFromAnyTFFile(t *testing.T) {
	dir := t.TempDir()
	content := `
data "terraform_remote_state" "base" {
  backend = "s3"
  config = {
    bucket = "example-terraform-state"
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

	wantURL := "s3://example-terraform-state/env/base/terraform.tfstate"
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
