package tfstateref

import (
	"context"
	"fmt"

	"github.com/alecthomas/kong"
)

var Version = "dev"
var Revision = "HEAD"

type CLI struct {
	Dirs    []string    `arg:"" optional:"" help:"Directories to check (default: terraform/)."`
	Verbose bool        `name:"verbose" short:"v" help:"Verbose output (show OK results)."`
	Quiet   bool        `name:"quiet" short:"q" help:"Quiet mode (errors only, no progress)."`
	JSON    bool        `name:"json" help:"Output results as JSON to stdout."`
	Version VersionFlag `name:"version" help:"Show version."`
}

type VersionFlag string

func (v VersionFlag) Decode(ctx *kong.DecodeContext) error { return nil }
func (v VersionFlag) IsBool() bool                         { return true }
func (v VersionFlag) BeforeApply(app *kong.Kong, vars kong.Vars) error {
	fmt.Printf("%s-%s\n", Version, Revision)
	app.Exit(0)
	return nil
}

func RunCLI(ctx context.Context, args []string) error {
	cli := CLI{}
	parser, err := kong.New(&cli)
	if err != nil {
		return NewUsageError(err)
	}
	if _, err := parser.Parse(args); err != nil {
		return NewUsageError(err)
	}
	if cli.Verbose && cli.Quiet {
		return NewUsageError(fmt.Errorf("-v and -q cannot be used together"))
	}
	app := New(&cli)
	return app.Run(ctx)
}
