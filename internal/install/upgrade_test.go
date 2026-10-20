package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpgrade(t *testing.T) {
	for _, mode := range []string{"latest", "pinned", "current", "installer checksum", "duplicate checksum", "archive checksum", "download failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			// Exercise custom paths, including spaces and quotes, with the real installer.
			bin := filepath.Join(f.home, "custom bin")
			install := filepath.Join(f.home, "custom releases")
			f.release(t, "v0.1.0", "linux_amd64")
			if output, err := f.install([]string{"KEEL_BIN_DIR=" + bin, "KEEL_INSTALL_DIR=" + install}, "v0.1.0"); err != nil {
				t.Fatal(err, output)
			}
			link := filepath.Join(bin, "keel")
			executable, err := filepath.EvalSymlinks(link)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := os.ReadFile(filepath.Join(f.home, ".zshrc"))
			if err != nil {
				t.Fatal(err)
			}
			f.release(t, "v0.2.0", "linux_amd64")
			script, err := os.ReadFile(f.installer)
			if err != nil {
				t.Fatal(err)
			}
			checksums, err := os.ReadFile(filepath.Join(f.assets, "checksums.txt"))
			if err != nil {
				t.Fatal(err)
			}
			checksums = append(checksums, fmt.Appendf(nil, "%x  install.sh\n", sha256.Sum256(script))...)
			write(t, filepath.Join(f.assets, "checksums.txt"), string(checksums), 0644)
			if mode == "archive checksum" {
				write(t, filepath.Join(f.assets, "keel_linux_amd64.tar.gz"), "corrupt archive", 0644)
			}
			if mode == "installer checksum" {
				checksums = []byte("invalid  install.sh\n")
			}
			if mode == "duplicate checksum" {
				checksums = append(checksums, checksums...)
			}
			requested, latest := "", "v0.2.0"
			if mode == "pinned" {
				requested = "v0.2.0"
			}
			if mode == "current" {
				latest = "v0.1.0"
			}
			var requests []string
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.URL.String())
				var data []byte
				status := 200
				switch r.URL.String() {
				case "https://api.github.com/repos/marcelsud/keel/releases/latest":
					data = []byte(`{"tag_name":"keel-` + latest + `"}`)
				case releases + "/download/keel-v0.2.0/install.sh":
					data = script
				case releases + "/download/keel-v0.2.0/checksums.txt":
					data = checksums
				default:
					t.Errorf("unexpected request: %s", r.URL)
					status = 404
				}
				if mode == "download failure" {
					status = 503
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
			})}
			for _, entry := range f.environment() {
				key, value, _ := strings.Cut(entry, "=")
				if strings.HasPrefix(key, "KEEL_TEST_") || key == "PATH" {
					t.Setenv(key, value)
				}
			}
			var output bytes.Buffer
			err = upgrade(context.Background(), requested, executable, link, client, &output)
			failed := strings.Contains(mode, "checksum") || mode == "download failure"
			if (err != nil) != failed {
				t.Fatalf("error=%v output=%s", err, output.String())
			}
			want := "v0.2.0"
			if failed || mode == "current" {
				want = "v0.1.0"
			}
			active, err := filepath.EvalSymlinks(link)
			if err != nil || active != filepath.Join(install, "releases", want, "bin/keel") {
				t.Fatalf("active=%s err=%v", active, err)
			}
			after, _ := os.ReadFile(filepath.Join(f.home, ".zshrc"))
			if !bytes.Equal(profile, after) {
				t.Fatal("upgrade modified shell profile")
			}
			if mode == "current" && (len(requests) != 1 || !strings.Contains(output.String(), "already installed")) {
				t.Fatal(requests, output.String())
			}
			if mode == "pinned" && len(requests) != 2 {
				t.Fatal("pinned upgrade queried latest release", requests)
			}
		})
	}
}

func TestUpgradeRejectsUnmanagedInstallationsAndInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"../../unsafe"}, {"v1.0.0", "extra"}, {"--unknown"}} {
		if err := Run(context.Background(), args, io.Discard); err == nil {
			t.Fatal("accepted arguments", args)
		}
	}
	if err := upgrade(context.Background(), "v0.2.0", filepath.Join(t.TempDir(), "keel"), "", nil, io.Discard); err == nil {
		t.Fatal("accepted unmanaged executable")
	}
}
