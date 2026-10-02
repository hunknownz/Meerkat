// Command meerkat is the Meerkat local daemon and CLI.
package main

import (
	"context"
	"os"

	"github.com/hunknownz/Meerkat/internal/cli"
)

func main() {
	os.Exit(cli.Run(cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Ctx: context.Background()}, os.Args[1:]))
}
