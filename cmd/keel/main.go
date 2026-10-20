package main

import (
	"context"
	"encoding/json"
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
		case "migrate":
			return project.Migrate(ctx, args[1:], output)
		case "help", "-h", "--help":
			fmt.Fprintln(output, "Usage: keel [command] [options]\n\nCommands:\n  init      Create a runnable service and install its SDK\n  generate  Generate bindings from local contracts and dependencies\n  use       Add a local project's contracts\n  dev       Start the environment and service, and watch changes\n  pull      Download a contract from a running service\n  migrate   Create, apply, and inspect service SQL migrations\n  version   Print the installed version\n\nWithout a command, start the local environment. Use keel <command> --help for options.")
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
	credentials := flags.String("credentials", "", "private per-service credentials JSON file")
	flags.BoolVar(&options.SQL, "sql", false, "enable embedded SQL (native prototype build)")
	flags.DurationVar(&options.SQLTransactionTimeout, "sql-transaction-timeout", 0, "SQL transaction lease duration (default 30s)")
	clusterConfig := flags.String("cluster-config", "", "JSON configuration for this embedded cluster member")
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
	if *credentials != "" {
		data, err := os.ReadFile(*credentials)
		if err != nil {
			return err
		}
		options.Credentials = new(core.Credentials)
		if err := json.Unmarshal(data, options.Credentials); err != nil {
			return err
		}
	}
	if *clusterConfig != "" {
		data, err := os.ReadFile(*clusterConfig)
		if err != nil {
			return err
		}
		options.Cluster = new(core.ClusterOptions)
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(options.Cluster); err != nil {
			return err
		}
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
