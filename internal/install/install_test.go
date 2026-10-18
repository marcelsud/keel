package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fixture struct {
	home, assets, mocks, installer, os, arch string
}

func write(t *testing.T, filename, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell installer supports Linux, macOS, and WSL")
	}
	_, file, _, _ := runtime.Caller(0)
	root := t.TempDir()
	f := fixture{home: filepath.Join(root, "user's home"), assets: filepath.Join(root, "assets"), mocks: filepath.Join(root, "mocks"), installer: filepath.Join(filepath.Dir(file), "../../install.sh"), os: "Linux", arch: "x86_64"}
	write(t, filepath.Join(f.home, ".keep"), "", 0644)
	write(t, filepath.Join(f.mocks, "uname"), "#!/bin/sh\ncase \"$1\" in -s) printf '%s\\n' \"$KEEL_TEST_OS\" ;; -m) printf '%s\\n' \"$KEEL_TEST_ARCH\" ;; esac\n", 0755)
	write(t, filepath.Join(f.mocks, "curl"), `#!/bin/sh
set -eu
url= output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        https://*) url=$1 ;;
        -o) shift; output=$1 ;;
    esac
    shift
done
printf '%s\n' "$url" >> "$KEEL_TEST_ASSETS/requests"
cp "$KEEL_TEST_ASSETS/${url##*/}" "$output" 2>/dev/null || exit 22
`, 0755)
	return f
}

func (f fixture) environment() []string {
	return append(os.Environ(), "HOME="+f.home, "ZDOTDIR="+f.home, "SHELL=/bin/zsh", "PATH="+f.mocks+":"+os.Getenv("PATH"), "KEEL_TEST_ASSETS="+f.assets, "KEEL_TEST_OS="+f.os, "KEEL_TEST_ARCH="+f.arch, "KEEL_BIN_DIR=", "KEEL_INSTALL_DIR=", "KEEL_NO_MODIFY_PATH=0")
}

func (f fixture) saveArchive(t *testing.T, data []byte, platform string) {
	t.Helper()
	asset := "keel_" + platform + ".tar.gz"
	write(t, filepath.Join(f.assets, asset), string(data), 0644)
	write(t, filepath.Join(f.assets, "checksums.txt"), fmt.Sprintf("%x  %s\n", sha256.Sum256(data), asset), 0644)
}

func (f fixture) release(t *testing.T, version, platform string) {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tarball := tar.NewWriter(gz)
	for filename, content := range map[string]string{
		"VERSION": version + "\n", "bin/keel": "#!/bin/sh\nprintf 'keel " + version + "\\n'\n",
		"bin/buf": "#!/bin/sh\nexit 0\n", "bin/protoc-gen-go": "#!/bin/sh\nexit 0\n",
		"bin/protoc-gen-es": "#!/bin/sh\nexit 0\n", "lib/protoc-gen-es.cjs": "// generator\n",
	} {
		mode := int64(0644)
		if strings.HasPrefix(filename, "bin/") {
			mode = 0755
		}
		if err := tarball.WriteHeader(&tar.Header{Name: filename, Mode: mode, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.saveArchive(t, archive.Bytes(), platform)
}

func (f fixture) install(extra []string, args ...string) (string, error) {
	command := exec.Command("sh", append([]string{f.installer}, args...)...)
	command.Env = append(f.environment(), extra...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestInstallUpgradeAndPath(t *testing.T) {
	for _, platform := range []struct{ os, arch, asset string }{{"Linux", "x86_64", "linux_amd64"}, {"Darwin", "arm64", "darwin_arm64"}} {
		t.Run(platform.asset, func(t *testing.T) {
			f := newFixture(t)
			f.os, f.arch = platform.os, platform.arch
			f.release(t, "v0.1.0", platform.asset)
			for range 2 {
				if output, err := f.install(nil, "v0.1.0"); err != nil {
					t.Fatal(err, output)
				}
			}
			profile, err := os.ReadFile(filepath.Join(f.home, ".zshrc"))
			if err != nil || strings.Count(string(profile), "# Keel") != 1 {
				t.Fatal("profile was not updated exactly once", string(profile), err)
			}
			f.release(t, "v0.2.0", platform.asset)
			if output, err := f.install(nil); err != nil {
				t.Fatal(err, output)
			}
			command := exec.Command("sh", "-c", `. "$HOME/.zshrc"; keel --version`)
			command.Env = f.environment()
			if output, err := command.CombinedOutput(); err != nil || string(output) != "keel v0.2.0\n" {
				t.Fatal(err, string(output))
			}
			requests, _ := os.ReadFile(filepath.Join(f.assets, "requests"))
			if !strings.Contains(string(requests), "/download/keel-v0.1.0/keel_"+platform.asset+".tar.gz") || !strings.Contains(string(requests), "/latest/download/") {
				t.Fatal("incorrect release URLs", string(requests))
			}
		})
	}
}

func TestFailedDownloadLeavesInstallationUnchanged(t *testing.T) {
	f := newFixture(t)
	f.release(t, "v0.1.0", "linux_amd64")
	if output, err := f.install(nil); err != nil {
		t.Fatal(err, output)
	}
	f.release(t, "v0.2.0", "linux_amd64")
	write(t, filepath.Join(f.assets, "keel_linux_amd64.tar.gz"), "corrupt archive", 0644)
	if output, err := f.install(nil); err == nil || !strings.Contains(output, "checksum mismatch") {
		t.Fatal(err, output)
	}
	command := exec.Command(filepath.Join(f.home, ".local/bin/keel"), "--version")
	if output, err := command.CombinedOutput(); err != nil || string(output) != "keel v0.1.0\n" {
		t.Fatal("previous installation was replaced", err, string(output))
	}
	if err := os.Remove(filepath.Join(f.assets, "keel_linux_amd64.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if output, err := f.install(nil); err == nil || !strings.Contains(output, "release download failed") {
		t.Fatal(err, output)
	}
}

func TestProfileOptOutAndUnsupportedPlatform(t *testing.T) {
	f := newFixture(t)
	f.release(t, "v0.1.0", "linux_amd64")
	if output, err := f.install([]string{"KEEL_NO_MODIFY_PATH=1"}); err != nil || !strings.Contains(output, "export PATH=") {
		t.Fatal(err, output)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("opt-out changed the shell profile", err)
	}
	f.arch = "riscv64"
	if output, err := f.install(nil); err == nil || !strings.Contains(output, "supported architectures") {
		t.Fatal(err, output)
	}
	if output, err := f.install(nil, "../../invalid"); err == nil || !strings.Contains(output, "version must look") {
		t.Fatal(err, output)
	}
}

func TestBashProfileAndCustomBinDirectory(t *testing.T) {
	for _, profile := range []string{".bash_profile", ".bash_login", ".profile"} {
		t.Run(profile, func(t *testing.T) {
			f := newFixture(t)
			f.release(t, "v0.1.0", "linux_amd64")
			write(t, filepath.Join(f.home, profile), "export KEEP_ME=existing\n", 0644)
			bin := filepath.Join(f.home, "custom bin")
			if output, err := f.install([]string{"SHELL=/bin/bash", "KEEL_BIN_DIR=" + bin}); err != nil {
				t.Fatal(err, output)
			}
			for _, name := range []string{".bashrc", profile} {
				data, err := os.ReadFile(filepath.Join(f.home, name))
				if err != nil || !strings.Contains(string(data), "# Keel") {
					t.Fatal("missing PATH setup", name, err)
				}
			}
			command := exec.Command("sh", "-c", `. "$1"; test "$KEEP_ME" = existing && keel --version`, "check-profile", filepath.Join(f.home, profile))
			command.Env = f.environment()
			if output, err := command.CombinedOutput(); err != nil || string(output) != "keel v0.1.0\n" {
				t.Fatal("custom PATH or existing profile content was lost", err, string(output))
			}
		})
	}
}

func TestReleaseBundleGeneratesWithoutGlobalTools(t *testing.T) {
	filename := os.Getenv("KEEL_TEST_RELEASE_ARCHIVE")
	if filename == "" {
		t.Skip("set KEEL_TEST_RELEASE_ARCHIVE to test an actual release bundle")
	}
	f := newFixture(t)
	if runtime.GOOS == "darwin" {
		f.os = "Darwin"
	}
	if runtime.GOARCH == "arm64" {
		f.arch = "arm64"
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	f.saveArchive(t, data, runtime.GOOS+"_"+runtime.GOARCH)
	if output, err := f.install(nil); err != nil {
		t.Fatal(err, output)
	}
	installed, err := filepath.EvalSymlinks(filepath.Join(f.home, ".local/bin/keel"))
	if err != nil {
		t.Fatal(err)
	}
	notice, err := os.ReadFile(filepath.Join(filepath.Dir(installed), "../licenses/typescript-vfs.txt"))
	if err != nil || !strings.Contains(string(notice), "Copyright (c) Microsoft Corporation") {
		t.Fatal("bundled VFS license notice is missing", err)
	}
	project := filepath.Join(t.TempDir(), "project")
	write(t, filepath.Join(project, "go.mod"), "module example.com/notes\n\ngo 1.26.0\n", 0644)
	write(t, filepath.Join(project, "keel.json"), `{"targets":[{"language":"go","out":"generated/go"},{"language":"typescript","out":"generated/ts"}]}`, 0644)
	write(t, filepath.Join(project, "proto/notes/v1/notes.proto"), `syntax="proto3"; package notes.v1; service NotesService { rpc Create(Note) returns (Note); } message Note { string text=1; }`, 0644)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"buf", "protoc-gen-go", "protoc-gen-es"} {
		write(t, filepath.Join(f.mocks, tool), "#!/bin/sh\nexit 77\n", 0755)
	}
	command := exec.Command(filepath.Join(f.home, ".local/bin/keel"), "generate")
	command.Dir = project
	command.Env = append(f.environment(), "PATH="+f.mocks+":"+filepath.Dir(node)+":/usr/bin:/bin")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	for _, filename := range []string{"generated/go/notes/v1/notes.pb.go", "generated/go/notes/v1/notes.keel.go", "generated/ts/notes/v1/notes_pb.ts", "generated/ts/notes/v1/notes_keel.ts"} {
		file, err := os.Open(filepath.Join(project, filename))
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil || len(content) == 0 {
			t.Fatal("empty generated file", filename, err)
		}
	}
}
