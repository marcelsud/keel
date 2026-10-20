package install

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const releases = "https://github.com/marcelsud/keel/releases"

var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$`)

// Run upgrades the active release installation, including its bundled generators.
func Run(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("keel upgrade", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: keel upgrade [vX.Y.Z]\n\nInstall the latest stable CLI release, or a specific release.\nUpdates the active installation and its bundled generators without changing shell profiles.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("usage: keel upgrade [vX.Y.Z]")
	}
	requested := flags.Arg(0)
	if requested != "" && !releaseVersion.MatchString(requested) {
		return errors.New("version must look like v0.2.1")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return errors.New("keel upgrade supports Linux, macOS, and WSL")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	var link string
	for _, candidate := range []string{os.Args[0], "keel", filepath.Join(os.Getenv("KEEL_BIN_DIR"), "keel")} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		path, err = filepath.Abs(path)
		if err != nil {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 || filepath.Base(path) != "keel" {
			continue
		}
		target, err := filepath.EvalSymlinks(path)
		if err == nil && target == executable {
			link = path
			break
		}
	}
	return upgrade(ctx, requested, executable, link, &http.Client{Timeout: 3 * time.Minute}, output)
}

func upgrade(ctx context.Context, requested, executable, link string, client *http.Client, output io.Writer) error {
	releaseDir := filepath.Dir(filepath.Dir(executable))
	current, err := os.ReadFile(filepath.Join(releaseDir, "VERSION"))
	version := strings.TrimSpace(string(current))
	if err != nil || !releaseVersion.MatchString(version) || filepath.Base(releaseDir) != version || filepath.Base(filepath.Dir(releaseDir)) != "releases" || filepath.Base(filepath.Dir(executable)) != "bin" || filepath.Base(executable) != "keel" {
		return errors.New("keel upgrade requires a release installation; install the release CLI with install.sh")
	}
	if _, err := os.Stat(filepath.Join(releaseDir, ".archive.sha256")); err != nil {
		return fmt.Errorf("not an installer-managed release: %w", err)
	}
	if link == "" {
		return errors.New("cannot find the active keel installation symlink; run the installed keel from PATH")
	}
	target, err := filepath.EvalSymlinks(link)
	if err != nil || target != executable {
		return errors.New("the active keel symlink does not match the running CLI")
	}
	if requested == "" {
		fmt.Fprintln(output, "Checking the latest stable Keel release...")
		data, err := download(ctx, client, "https://api.github.com/repos/marcelsud/keel/releases/latest")
		if err != nil {
			return err
		}
		var release struct {
			Tag string `json:"tag_name"`
		}
		if err := json.Unmarshal(data, &release); err != nil {
			return fmt.Errorf("read latest release: %w", err)
		}
		requested = strings.TrimPrefix(release.Tag, "keel-")
		if release.Tag != "keel-"+requested || !releaseVersion.MatchString(requested) {
			return errors.New("latest GitHub release is not a Keel CLI release")
		}
	}
	if requested == version {
		fmt.Fprintf(output, "Keel %s is already installed.\n", version)
		return nil
	}
	fmt.Fprintf(output, "Upgrading Keel %s to %s...\n", version, requested)
	base := releases + "/download/keel-" + requested
	script, err := download(ctx, client, base+"/install.sh")
	if err != nil {
		return err
	}
	checksums, err := download(ctx, client, base+"/checksums.txt")
	if err != nil {
		return err
	}
	expected, count := "", 0
	for line := range strings.SplitSeq(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "install.sh" {
			expected = fields[0]
			count++
		}
	}
	if count != 1 || expected != fmt.Sprintf("%x", sha256.Sum256(script)) {
		return errors.New("installer checksum mismatch or missing; installation unchanged")
	}
	directory, err := os.MkdirTemp("", "keel-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	filename := filepath.Join(directory, "install.sh")
	if err := os.WriteFile(filename, script, 0600); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "sh", filename, requested)
	command.Env = append(os.Environ(),
		"KEEL_INSTALL_DIR="+filepath.Dir(filepath.Dir(releaseDir)),
		"KEEL_BIN_DIR="+filepath.Dir(link), "KEEL_NO_MODIFY_PATH=1")
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return fmt.Errorf("install Keel %s: %w", requested, err)
	}
	return nil
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "keel-upgrade")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, response.StatusCode)
	}
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("release metadata exceeds 2 MiB")
	}
	return data, nil
}
