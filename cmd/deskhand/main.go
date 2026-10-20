// deskhand is the command line of the Deskhand daemon.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/YangTaeyoung/deskhand/internal/cli"
)

// version is set with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	app := cli.New(version)
	code := app.Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
