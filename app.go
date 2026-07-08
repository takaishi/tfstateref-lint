package tfstateref

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

// ErrLintFailed is returned when checks ran successfully but found errors.
var ErrLintFailed = errors.New("lint errors found")

// UsageError indicates the tool was invoked incorrectly (exit code 2).
type UsageError struct {
	err error
}

func NewUsageError(err error) *UsageError { return &UsageError{err: err} }
func (e *UsageError) Error() string       { return e.err.Error() }
func (e *UsageError) Unwrap() error       { return e.err }

type App struct {
	CLI *CLI
}

func New(cli *CLI) *App {
	return &App{
		CLI: cli,
	}
}

func (app *App) Run(ctx context.Context) error {
	dirs := app.CLI.Dirs
	if len(dirs) == 0 {
		dirs = []string{"terraform/"}
	}

	checker := NewChecker(dirs, app.CLI.Verbose, app.CLI.Quiet)
	if err := checker.Build(); err != nil {
		return NewUsageError(err)
	}

	result := checker.Run(ctx)

	if app.CLI.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	}

	if !result.OK() {
		return ErrLintFailed
	}
	return nil
}
