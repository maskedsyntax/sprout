// Command sprout generates Spring Boot projects using Spring Initializr.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/maskedsyntax/sprout/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Execute(ctx, &cli.App{}, os.Args[1:])
	stop()
	os.Exit(code)
}
