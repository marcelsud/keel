package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/marcelsud/keel/core"
	"github.com/marcelsud/keel/sdk"
)

func secret() string {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func ensureCredentials(root string, config Config) (core.Credentials, string, error) {
	name := filepath.Join(root, ".keel", "credentials.json")
	credentials := core.Credentials{Admin: secret(), Services: map[string]string{}}
	data, err := os.ReadFile(name)
	if err == nil {
		if err := json.Unmarshal(data, &credentials); err != nil {
			return credentials, name, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return credentials, name, err
	}
	if credentials.Services == nil {
		credentials.Services = map[string]string{}
	}
	for _, service := range append(config.Services, sortedKeys(config.SQL)...) {
		if credentials.Services[service] == "" {
			credentials.Services[service] = secret()
		}
	}
	data, err = json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return credentials, name, err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return credentials, name, err
	}
	if err := os.WriteFile(name, append(data, '\n'), 0600); err != nil {
		return credentials, name, err
	}
	if err := os.Chmod(name, 0600); err != nil {
		return credentials, name, err
	}
	return credentials, name, nil
}

func migrationFiles(root, directory string) ([]sdk.Migration, error) {
	var result []sdk.Migration
	entries, err := os.ReadDir(absolute(root, directory))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(absolute(root, directory), entry.Name()))
		if err != nil {
			return nil, err
		}
		result = append(result, sdk.Migration{Name: entry.Name(), SQL: string(data)})
	}
	return result, nil
}

func applySQL(ctx context.Context, root string, config Config, url string, credentials core.Credentials, output io.Writer) error {
	app, err := sdk.Connect(sdk.Options{Name: core.AdminUser, User: core.AdminUser, Token: credentials.Admin, URL: url})
	if err != nil {
		return err
	}
	defer app.Stop()
	for _, service := range sortedKeys(config.SQL) {
		files, err := migrationFiles(root, config.SQL[service])
		if err != nil {
			return err
		}
		if err := app.SQL.Migrate(ctx, service, files); err != nil {
			return fmt.Errorf("migrate %s: %w", service, err)
		}
		fmt.Fprintf(output, "SQL migrations ready: %s\n", service)
	}
	return nil
}

// Migrate manages project-owned SQL files through the running core's administrator identity.
func Migrate(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(output, "Usage: keel migrate up|status|create <name> [--service name] [--credentials file]\n\nApply or inspect SQL migrations on the running environment; create writes a new local migration.")
		return nil
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	config, err := load(root)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: keel migrate up|status|create <name> [--service name]")
	}
	operation, args := args[0], args[1:]
	if operation != "up" && operation != "status" && operation != "create" {
		return errors.New("migration operation must be up, status, or create")
	}
	var name string
	if operation == "create" && len(args) > 0 {
		name, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("keel migrate", flag.ContinueOnError)
	flags.SetOutput(output)
	service := flags.String("service", "", "service owning these migrations")
	credentialsFile := flags.String("credentials", os.Getenv("KEEL_CREDENTIALS_FILE"), "private administrator credentials JSON file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("unexpected migration arguments")
	}
	if *service != "" {
		directory, ok := config.SQL[*service]
		if !ok {
			return errors.New("SQL service is not configured in keel.toml or keel.json")
		}
		config.SQL = map[string]string{*service: directory}
	}
	if len(config.SQL) == 0 {
		return errors.New("declare service migration directories in the sql section of keel.toml or keel.json")
	}
	if operation == "create" {
		if len(config.SQL) != 1 || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
			return errors.New("select --service and give a migration name containing letters, numbers, underscores, or hyphens")
		}
		directory := absolute(root, config.SQL[sortedKeys(config.SQL)[0]])
		if err := os.MkdirAll(directory, 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(directory, time.Now().UTC().Format("20060102150405")+"_"+name+".sql"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := file.WriteString("-- +goose Up\n"); err != nil {
			return err
		}
		fmt.Fprintln(output, file.Name())
		return nil
	}
	credentials := core.Credentials{}
	if *credentialsFile != "" {
		var data []byte
		data, err = os.ReadFile(*credentialsFile)
		if err == nil {
			err = json.Unmarshal(data, &credentials)
		}
	} else {
		credentials, _, err = ensureCredentials(root, config)
	}
	if err != nil {
		return err
	}
	if operation == "up" {
		return applySQL(ctx, root, config, os.Getenv("KEEL_URL"), credentials, output)
	}
	if operation != "status" {
		return errors.New("migration operation must be up, status, or create")
	}
	app, err := sdk.Connect(sdk.Options{Name: core.AdminUser, User: core.AdminUser, Token: credentials.Admin, URL: os.Getenv("KEEL_URL")})
	if err != nil {
		return err
	}
	defer app.Stop()
	for _, service := range sortedKeys(config.SQL) {
		files, err := migrationFiles(root, config.SQL[service])
		if err != nil {
			return err
		}
		status, err := app.SQL.MigrationStatus(ctx, service, files)
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(status, "", "  ")
		fmt.Fprintf(output, "%s: %s\n", service, data)
	}
	return nil
}
