package project

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcelsud/keel/internal/protocol"
)

// Init creates a runnable JSON-action service without requiring contracts.
func Init(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("keel init", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: keel init [directory] [options]\n\nOptions:")
		flags.PrintDefaults()
	}
	language := flags.String("lang", "typescript", "service language: go or typescript")
	skipInstall := flags.Bool("skip-install", false, "create files without installing dependencies")
	directory := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		directory, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if directory == "" && len(flags.Args()) == 1 {
		directory = flags.Arg(0)
	} else if len(flags.Args()) != 0 {
		return errors.New("usage: keel init [directory] [--lang go|typescript]")
	}
	if *language != "go" && *language != "typescript" {
		return errors.New("language must be go or typescript")
	}
	if directory == "" {
		directory = "."
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	name := filepath.Base(root)
	if !protocol.ValidName(name) {
		return errors.New("project directory name must contain only letters, numbers, hyphens, or underscores")
	}
	if name == "keel-core" {
		return errors.New("keel-core is reserved; choose another project directory name")
	}
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s must be a directory, not a file or symlink", root)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return fmt.Errorf("%s is not empty; choose a new or empty directory", root)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	command, install := []string{"bun", "run", "service.ts"}, []string{"bun", "install"}
	files := map[string]string{
		"service.ts": strings.ReplaceAll(typescriptServiceStarter, "{{name}}", name),
		"client.ts":  strings.ReplaceAll(typescriptClientStarter, "{{name}}", name),
		"package.json": `{
  "private": true,
  "type": "module",
  "scripts": { "check": "tsc" },
  "dependencies": { "@marcelsud/keel.ts": "0.2.1" },
  "devDependencies": { "typescript": "5.9.3", "@types/bun": "^1.3.14" }
}
`,
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "Preserve",
    "moduleResolution": "Bundler",
    "strict": true,
    "skipLibCheck": true,
    "types": ["bun"],
    "noEmit": true
  }
}
`,
		".gitignore": "node_modules/\n.keel/\ndist/\n",
	}
	if *language == "go" {
		source, err := format.Source([]byte(strings.ReplaceAll(goStarter, "{{name}}", name)))
		if err != nil {
			return err
		}
		command, install = []string{"go", "run", "."}, []string{"go", "mod", "tidy"}
		files = map[string]string{
			"main.go":    string(source),
			"go.mod":     "module " + name + "\n\ngo 1.26.0\n\nrequire github.com/marcelsud/keel v0.0.0-20261018201408-ad180a271040\n",
			".gitignore": ".keel/\n",
		}
	}
	if !*skipInstall {
		if _, err := exec.LookPath(install[0]); err != nil {
			return fmt.Errorf("%s not found; install it or use --skip-install", install[0])
		}
	}
	config, err := json.MarshalIndent(Config{Command: command}, "", "  ")
	if err != nil {
		return err
	}
	files[configName] = string(config) + "\n"
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	for _, filename := range sortedKeys(files) {
		file, err := os.OpenFile(filepath.Join(root, filename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(files[filename])
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	fmt.Fprintf(output, "created %s (%s)\n", root, *language)
	if !*skipInstall {
		process := exec.CommandContext(ctx, install[0], install[1:]...)
		process.Dir, process.Stdout, process.Stderr = root, output, output
		if err := process.Run(); err != nil {
			return fmt.Errorf("project created, but dependencies were not installed: %w; run %s in %s, then keel dev", err, strings.Join(install, " "), root)
		}
	}
	fmt.Fprintln(output, "\nNext:")
	if directory != "." {
		fmt.Fprintf(output, "  cd '%s'\n", strings.ReplaceAll(directory, "'", "'\\''"))
	}
	if *skipInstall {
		fmt.Fprintf(output, "  %s\n", strings.Join(install, " "))
	}
	fmt.Fprintln(output, "  keel dev")
	return nil
}

const typescriptServiceStarter = `import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "{{name}}",
  url: process.env.KEEL_URL,
  user: process.env.KEEL_USER,
  token: process.env.KEEL_TOKEN,
});

app.action({
  name: "greet",
  args: app.schema.object({ name: app.schema.string() }),
  handler(_ctx, input) {
    return { message: "Hello, " + input.name + "!" };
  },
});

await app.start();
console.log("{{name}} ready");
console.log("Try: bun run client.ts");
`

const typescriptClientStarter = `import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "{{name}}-client",
  url: process.env.KEEL_URL,
  user: process.env.KEEL_USER,
  token: process.env.KEEL_TOKEN,
});

try {
  const result = await app.call<{ message: string }>("{{name}}", "greet", { name: "Ada" });
  console.log(result.message);
} finally {
  await app.stop();
}
`

const goStarter = `package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/marcelsud/keel/sdk"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	client := flag.Bool("client", false, "call the greeting service and exit")
	flag.Parse()
	const serviceName = "{{name}}"
	name := serviceName
	if *client {
		name += "-client"
	}
	app, err := sdk.Connect(sdk.Options{Name: name, URL: os.Getenv("KEEL_URL"), User: os.Getenv("KEEL_USER"), Token: os.Getenv("KEEL_TOKEN")})
	if err != nil {
		return err
	}
	defer app.Stop()
	if *client {
		var result struct { Message string }
		if err := app.Call(context.Background(), serviceName, "greet", map[string]string{"name": "Ada"}, &result); err != nil {
			return err
		}
		fmt.Println(result.Message)
		return nil
	}
	if err := app.Action("greet", func(_ *sdk.Context, raw json.RawMessage) (any, error) {
		var input struct { Name string }
		if json.Unmarshal(raw, &input) != nil || strings.TrimSpace(input.Name) == "" {
			return nil, &sdk.Error{Code: 400, Message: "name is required"}
		}
		return map[string]string{"message": "Hello, " + input.Name + "!"}, nil
	}); err != nil {
		return err
	}
	if err := app.Start(context.Background()); err != nil {
		return err
	}
	fmt.Println(serviceName + " ready")
	fmt.Println("Try: go run . -client")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}
`
