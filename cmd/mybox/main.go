// Command mybox is a command-line client for the Naver MYBOX Open API.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/overworks/mybox-cli/internal/cli"
)

func main() {
	// Ctrl-C cancels the in-flight request rather than killing the process mid
	// write, so partially downloaded files get cleaned up.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
