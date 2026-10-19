package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/marcelsud/keel/core"
	"github.com/marcelsud/keel/internal/project"
	"github.com/marcelsud/keel/internal/pull"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil && ctx.Err() == nil {
		slog.Error("keel failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return project.Init(ctx, args[1:], output)
		case "version", "--version":
			fmt.Fprintln(output, "keel "+version)
			return nil
		case "generate", "use", "dev":
			return project.Run(ctx, args[0], args[1:], output)
		case "pull":
			return pull.Run(ctx, args[1:], output)
		case "help", "-h", "--help":
			fmt.Fprintln(output, "Usage: keel [command] [options]\n\nCommands:\n  init      Create a runnable service and install its SDK\n  generate  Generate bindings from local contracts and dependencies\n  use       Add a local project's contracts\n  dev       Start the environment and service, and watch changes\n  pull      Download a contract from a running service\n  version   Print the installed version\n\nWithout a command, start the local environment. Use keel <command> --help for options.")
			return nil
		}
		if !strings.HasPrefix(args[0], "-") {
			return fmt.Errorf("unknown command %q; run keel --help", args[0])
		}
	}
	var options core.Options
	flags := flag.NewFlagSet("keel", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.Host, "host", "127.0.0.1", "local environment address")
	flags.IntVar(&options.Port, "port", 4222, "local environment port (-1 chooses a free port)")
	flags.StringVar(&options.DataDir, "data", "./data", "persistent data directory")
	options.Token = os.Getenv("KEEL_TOKEN")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(flags.Args()) > 0 {
		return errors.New("unexpected positional arguments")
	}
	c, err := core.Start(options)
	if err != nil {
		return err
	}
	defer c.Close()
	slog.Info("keel ready", "url", c.URL())
	<-ctx.Done()
	return nil
}
