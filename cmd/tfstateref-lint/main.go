package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tfstateref "github.com/takaishi/tfstateref-lint"
)

var Version = "dev"
var Revision = "HEAD"

func init() {
	tfstateref.Version = Version
	tfstateref.Revision = Revision
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := tfstateref.RunCLI(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, tfstateref.ErrLintFailed) {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
}
