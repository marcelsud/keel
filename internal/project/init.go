package project

import (
	"bufio"
	"context"
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

// Init creates an empty service or a runnable JSON or protobuf example.
func Init(ctx context.Context, args []string, output io.Writer) error {
	return initProject(ctx, args, os.Stdin, output, stdinIsTerminal())
}

func initProject(ctx context.Context, args []string, input io.Reader, output io.Writer, interactive bool) error {
	flags := flag.NewFlagSet("keel init", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: keel init [directory] [options]\n\nOptions:")
		flags.PrintDefaults()
	}
	language := flags.String("lang", "", "service language: go or typescript (default typescript)")
	starter := flags.String("template", "", "project starter: empty, json, or protobuf (default json)")
	yes := flags.Bool("yes", false, "use defaults without prompting")
	flags.BoolVar(yes, "y", false, "use defaults without prompting")
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
	if *language != "" && *language != "go" && *language != "typescript" {
		return errors.New("language must be go or typescript")
	}
	if *starter != "" && *starter != "empty" && *starter != "json" && *starter != "protobuf" {
		return errors.New("template must be empty, json, or protobuf")
	}
	if interactive && !*yes {
		reader := bufio.NewReader(input)
		var err error
		if directory == "" {
			directory, err = initAnswer(ctx, reader, output, "Project directory (current directory or a new path)", ".", nil)
			if err != nil {
				return err
			}
		}
		if *language == "" {
			*language, err = initAnswer(ctx, reader, output, "Language (typescript/go)", "typescript", []string{"typescript", "go"})
			if err != nil {
				return err
			}
		}
		if *starter == "" {
			*starter, err = initAnswer(ctx, reader, output, "Starter (empty/json/protobuf)", "json", []string{"empty", "json", "protobuf"})
			if err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *language == "" {
		*language = "typescript"
	}
	if *starter == "" {
		*starter = "json"
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
	command, install := []string{"bun", "run", "src/service.ts"}, []string{"bun", "install"}
	files := map[string]string{
		"src/service.ts": strings.ReplaceAll(typescriptServiceStarter, "{{name}}", name),
		"src/client.ts":  strings.ReplaceAll(typescriptClientStarter, "{{name}}", name),
		"package.json": `{
  "private": true,
  "type": "module",
  "scripts": { "check": "tsc" },
  "dependencies": { "@marcelsud/keel.ts": "0.2.0" },
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
  },
  "include": ["src/**/*.ts"]
}
`,
		".gitignore": "node_modules/\n.keel/\ndist/\n",
	}
	if *starter == "empty" {
		files["src/service.ts"] = strings.ReplaceAll(typescriptEmptyStarter, "{{name}}", name)
		delete(files, "src/client.ts")
	} else if *starter == "protobuf" {
		files["src/service.ts"] = strings.ReplaceAll(typescriptProtobufStarter, "{{name}}", name)
		files["src/client.ts"] = strings.ReplaceAll(typescriptProtobufClientStarter, "{{name}}", name)
	}
	if *language == "go" {
		starterSource := goStarter
		if *starter == "empty" {
			starterSource = goEmptyStarter
		}
		if *starter == "protobuf" {
			starterSource = goProtobufStarter
		}
		source, err := format.Source([]byte(strings.ReplaceAll(starterSource, "{{name}}", name)))
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
	if *starter == "protobuf" {
		files["proto/greet/v1/greet.proto"] = greetingProto
	}
	if !*skipInstall {
		if _, err := exec.LookPath(install[0]); err != nil {
			return fmt.Errorf("%s not found; install it or use --skip-install", install[0])
		}
	}
	config := Config{Command: command}
	if *language == "typescript" {
		config.Targets = []Target{{Language: "typescript", Output: "src/generated"}}
	}
	data, err := marshalConfig(configName, config)
	if err != nil {
		return err
	}
	files[configName] = string(data)
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	for _, filename := range sortedKeys(files) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filename)), 0755); err != nil {
			return err
		}
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
	fmt.Fprintf(output, "created %s (%s, %s)\n", root, *language, *starter)
	if !*skipInstall {
		if *starter == "protobuf" {
			if _, _, err := Generate(ctx, root, config, output); err != nil {
				return fmt.Errorf("project created, but contracts were not generated: %w; run keel generate, then %s in %s", err, strings.Join(install, " "), root)
			}
		}
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
		if *starter == "protobuf" {
			fmt.Fprintln(output, "  keel generate")
		}
		fmt.Fprintf(output, "  %s\n", strings.Join(install, " "))
	}
	fmt.Fprintln(output, "  keel dev")
	return nil
}

func initAnswer(ctx context.Context, reader *bufio.Reader, output io.Writer, label, fallback string, choices []string) (string, error) {
	type answer struct {
		value string
		err   error
	}
	for {
		fmt.Fprintf(output, "%s [%s]: ", label, fallback)
		response := make(chan answer, 1)
		go func() {
			value, err := reader.ReadString('\n')
			response <- answer{value, err}
		}()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case result := <-response:
			if result.err != nil {
				return "", fmt.Errorf("initialization canceled: %w; use --yes for defaults", result.err)
			}
			value := strings.TrimSpace(result.value)
			if value == "" {
				return fallback, nil
			}
			if len(choices) == 0 {
				return value, nil
			}
			value = strings.ToLower(value)
			for _, choice := range choices {
				if value == choice {
					return value, nil
				}
			}
			fmt.Fprintf(output, "Choose one of: %s.\n", strings.Join(choices, ", "))
		}
	}
}

const typescriptEmptyStarter = `import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "{{name}}",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

await app.start();
console.log("{{name}} ready");
`

const greetingProto = `syntax = "proto3";
package greet.v1;

service GreeterService {
  rpc Greet(GreetRequest) returns (GreetReply);
}

message GreetRequest { string name = 1; }
message GreetReply { string message = 1; }
`

const typescriptProtobufStarter = `import { createKeel, ValidationError } from "@marcelsud/keel.ts";
import { GreeterServiceContract } from "./generated/greet/v1/greet_keel.js";

const app = await createKeel({
  name: "{{name}}",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

app.service(GreeterServiceContract, {
  greet(_ctx, input) {
    if (!input.name.trim()) throw new ValidationError("name is required");
    return { message: "Hello, " + input.name + "!" };
  },
});

await app.start();
console.log("{{name}} ready");
console.log("Try: bun run src/client.ts");
`

const typescriptProtobufClientStarter = `import { createKeel } from "@marcelsud/keel.ts";
import { GreeterServiceContract } from "./generated/greet/v1/greet_keel.js";

const app = await createKeel({
  name: "{{name}}-client",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

try {
  const result = await app.client(GreeterServiceContract).greet({ name: "Ada" });
  console.log(result.message);
} finally {
  await app.stop();
}
`

const typescriptServiceStarter = `import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "{{name}}",
  url: process.env.KEEL_URL,
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
console.log("Try: bun run src/client.ts");
`

const typescriptClientStarter = `import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "{{name}}-client",
  url: process.env.KEEL_URL,
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
	app, err := sdk.Connect(sdk.Options{Name: name, URL: os.Getenv("KEEL_URL"), Token: os.Getenv("KEEL_TOKEN")})
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

const goEmptyStarter = `package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/marcelsud/keel/sdk"
)

func main() {
	if err := run(); err != nil { log.Fatal(err) }
}

func run() error {
	app, err := sdk.Connect(sdk.Options{Name: "{{name}}", URL: os.Getenv("KEEL_URL"), Token: os.Getenv("KEEL_TOKEN")})
	if err != nil { return err }
	defer app.Stop()
	if err := app.Start(context.Background()); err != nil { return err }
	fmt.Println("{{name}} ready")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}
`

const goProtobufStarter = `package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	greetv1 "{{name}}/generated/greet/v1"
	"github.com/marcelsud/keel/sdk"
)

type greeter struct{}

func (greeter) Greet(_ *sdk.Context, input *greetv1.GreetRequest) (*greetv1.GreetReply, error) {
	if strings.TrimSpace(input.GetName()) == "" {
		return nil, &sdk.Error{Code: 400, Message: "name is required"}
	}
	return &greetv1.GreetReply{Message: "Hello, " + input.GetName() + "!"}, nil
}

func main() {
	if err := run(); err != nil { log.Fatal(err) }
}

func run() error {
	client := flag.Bool("client", false, "call the greeting service and exit")
	flag.Parse()
	name := "{{name}}"
	if *client { name += "-client" }
	app, err := sdk.Connect(sdk.Options{Name: name, URL: os.Getenv("KEEL_URL"), Token: os.Getenv("KEEL_TOKEN")})
	if err != nil { return err }
	defer app.Stop()
	if *client {
		result, err := greetv1.NewGreeterServiceClient(app).Greet(context.Background(), &greetv1.GreetRequest{Name: "Ada"})
		if err != nil { return err }
		fmt.Println(result.GetMessage())
		return nil
	}
	if err := greetv1.RegisterGreeterService(app, greeter{}, sdk.ContractOptions{}); err != nil { return err }
	if err := app.Start(context.Background()); err != nil { return err }
	fmt.Println("{{name}} ready")
	fmt.Println("Try: go run . -client")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}
`
