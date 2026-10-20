package project

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcelsud/keel/internal/pull"
)

// Run executes generate, use, or dev in the current project.
func Run(ctx context.Context, operation string, args []string, output io.Writer) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("keel "+operation, flag.ContinueOnError)
	flags.SetOutput(output)
	var language, positionalLanguage, destination, goPrefix, goPlugin, esPlugin, alias, source string
	flags.StringVar(&language, "lang", "", "generation language: go or typescript (default: detect project)")
	flags.StringVar(&destination, "out", "", "generated output directory (default: generated)")
	flags.StringVar(&goPrefix, "go-package-prefix", "", "Go import prefix (default: infer from go.mod)")
	flags.StringVar(&goPlugin, "go-plugin", "", "Go protobuf generator")
	flags.StringVar(&esPlugin, "es-plugin", "", "TypeScript protobuf generator")
	if operation == "generate" {
		flags.Usage = func() {
			fmt.Fprintln(output, "Usage: keel generate [go|typescript] [options]\n\nOptions:")
			flags.PrintDefaults()
		}
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			positionalLanguage, args = args[0], args[1:]
		}
	}
	var dev DevOptions
	if operation == "dev" {
		flags.IntVar(&dev.Nodes, "nodes", 1, "local development members (1 or 3)")
		flags.IntVar(&dev.Core.Port, "port", 4222, "local environment port (-1 chooses a free port)")
		flags.StringVar(&dev.Core.Host, "host", "127.0.0.1", "local environment address")
		flags.StringVar(&dev.Core.DataDir, "data", filepath.Join(root, ".keel", "data"), "local data directory")
		flags.BoolVar(&dev.NoCore, "no-core", false, "use an existing local environment")
		flags.BoolVar(&dev.WatchOnly, "watch-only", false, "only regenerate contracts; do not start the environment or service")
		dev.Core.Token = os.Getenv("KEEL_TOKEN")
	}
	if operation == "use" {
		flags.StringVar(&alias, "as", "", "dependency name (default: project directory name)")
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			source, args = args[0], args[1:]
		}
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if operation == "generate" && positionalLanguage == "" && len(flags.Args()) == 1 {
		positionalLanguage = flags.Arg(0)
	} else if operation == "use" && source == "" && len(flags.Args()) == 1 {
		source = flags.Arg(0)
	} else if operation != "dev" && len(flags.Args()) != 0 {
		return errors.New("unexpected positional arguments")
	}
	if positionalLanguage != "" {
		if positionalLanguage != "go" && positionalLanguage != "typescript" {
			return fmt.Errorf("unsupported language %q; use go or typescript", positionalLanguage)
		}
		if language != "" && language != positionalLanguage {
			return errors.New("positional language conflicts with --lang; choose one language")
		}
		language = positionalLanguage
	}
	config, err := load(root)
	if err != nil {
		return err
	}
	if language != "" || destination != "" || goPrefix != "" || goPlugin != "" || esPlugin != "" {
		dev.targetOverride = true
		if len(config.Targets) > 1 && language == "" {
			return errors.New("select --lang when overriding multiple generation targets")
		}
		target := Target{Language: language, Output: destination, GoPackagePrefix: goPrefix, GoPlugin: goPlugin, ESPlugin: esPlugin}
		if len(config.Targets) == 1 {
			base := config.Targets[0]
			if target.Language == "" {
				target.Language = base.Language
			}
			if target.Output == "" {
				target.Output = base.Output
			}
			if target.GoPackagePrefix == "" {
				target.GoPackagePrefix = base.GoPackagePrefix
			}
			if target.GoPlugin == "" {
				target.GoPlugin = base.GoPlugin
			}
			if target.ESPlugin == "" {
				target.ESPlugin = base.ESPlugin
			}
		}
		if target.Language == "" {
			if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
				target.Language = "typescript"
			} else {
				target.Language = "go"
			}
		}
		config.Targets = []Target{target}
	}
	switch operation {
	case "generate":
		_, _, err := Generate(ctx, root, config, output)
		return err
	case "use":
		if source == "" || strings.HasPrefix(source, "github:") || strings.Contains(source, "://") {
			return errors.New("usage: keel use <local-project-directory>; GitHub release sources are not implemented yet")
		}
		dependency := absolute(root, source)
		if alias == "" {
			alias = filepath.Base(dependency)
		}
		relative, err := filepath.Rel(root, dependency)
		if err != nil {
			return err
		}
		if config.Dependencies == nil {
			config.Dependencies = map[string]string{}
		}
		if existing, exists := config.Dependencies[alias]; exists && absolute(root, existing) != dependency {
			return fmt.Errorf("dependency %q already uses another directory; choose a different --as name", alias)
		}
		config.Dependencies[alias] = filepath.ToSlash(relative)
		if _, _, err := Generate(ctx, root, config, output); err != nil {
			return err
		}
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return err
		}
		if err := pull.WriteFile(root, configName, append(data, '\n')); err != nil {
			return err
		}
		fmt.Fprintf(output, "using local contracts from %s as %s\n", relative, alias)
		return nil
	case "dev":
		if len(flags.Args()) > 0 {
			config.Command = flags.Args()
			dev.commandOverride = true
		}
		return Dev(ctx, root, config, dev, output)
	default:
		return fmt.Errorf("unknown project command %q", operation)
	}
}
