package tfstateref

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Result is the top-level output structure.
type Result struct {
	Dirs            []string     `json:"dirs"`
	Errors          []CheckError `json:"errors"`
	StateExistence  *CheckResult `json:"state_existence,omitempty"`
	OutputReference *CheckResult `json:"output_reference,omitempty"`
}

// OK returns true if no errors were found.
func (r *Result) OK() bool {
	return len(r.Errors) == 0
}

// CheckResult holds results for a single check.
type CheckResult struct {
	Checked int          `json:"checked"`
	Errors  []CheckError `json:"errors"`
}

// CheckError represents a single lint error.
type CheckError struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// Checker holds the precomputed data and runs the checks.
type Checker struct {
	dirs    []string
	verbose bool
	quiet   bool

	// all terraform_remote_state blocks found, in walk order
	stateRefs []stateRef

	// directory + remote_state label → state URL.
	// key: "dir\tlabel"
	labelToStateURL map[string]string

	// state URL → loaded state cache
	stateCache map[string]*tfstateLookup

	// state URL → read error cache (e.g. state not found)
	stateErrors map[string]error
}

// NewChecker creates a new Checker.
func NewChecker(dirs []string, verbose, quiet bool) *Checker {
	normalized := make([]string, len(dirs))
	for i, dir := range dirs {
		if dir != "" && !strings.HasSuffix(dir, "/") {
			dir += "/"
		}
		normalized[i] = dir
	}

	return &Checker{
		dirs:            normalized,
		verbose:         verbose,
		quiet:           quiet,
		labelToStateURL: make(map[string]string),
		stateCache:      make(map[string]*tfstateLookup),
		stateErrors:     make(map[string]error),
	}
}

// log prints a message to stderr (suppressed in quiet mode).
func (c *Checker) log(format string, a ...any) {
	if !c.quiet {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}

// logVerbose prints a message to stderr only in verbose mode.
func (c *Checker) logVerbose(format string, a ...any) {
	if c.verbose {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}

// walkDirs calls fn for each file under all configured directories.
// Directories that do not exist are skipped instead of causing an error.
func (c *Checker) walkDirs(fn filepath.WalkFunc) error {
	for _, dir := range c.dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			c.logVerbose("skip: %s (does not exist)", dir)
			continue
		}
		if err := filepath.Walk(dir, fn); err != nil {
			return err
		}
	}
	return nil
}

// Build scans the directories to build data needed for checks.
func (c *Checker) Build() error {
	return c.buildRemoteState()
}

// Run executes the remote-state checks, returning the result.
func (c *Checker) Run(ctx context.Context) *Result {
	result := &Result{
		Dirs:   c.dirs,
		Errors: []CheckError{},
	}

	c.runRemoteStateChecks(ctx, result)

	if result.OK() {
		c.log("All checks passed. OK.")
	} else {
		c.log("Found %d error(s).", len(result.Errors))
	}

	return result
}
