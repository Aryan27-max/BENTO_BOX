// Command bento turns a fresh machine into a developer machine.
//
// Bento is written in Go, but Go is a build-time dependency only. The released
// binaries are self-contained: a user downloads one file, runs it, and needs
// no runtime, interpreter or toolchain of any kind installed first.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/Aryan27-max/bento-box/internal/cli"
)

func main() {
	// Ctrl+C stops Bento between steps rather than killing it mid-install.
	// The command currently running is given the chance to finish, so a
	// package manager is never left with a half-written database.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		<-ctx.Done()
		fmt.Fprintln(os.Stderr, "\nInterrupted. Finishing the current step, then stopping…")
	}()

	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
