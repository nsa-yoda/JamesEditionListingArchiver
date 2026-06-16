package main

import (
	"context"
	"os"
	"os/signal"

	"listing-archiver/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(app.RunWithInput(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
