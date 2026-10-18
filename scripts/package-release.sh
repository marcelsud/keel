#!/usr/bin/env bash
set -euo pipefail

[[ $# -ge 3 && $# -le 4 ]] || { printf 'Usage: bash scripts/package-release.sh vX.Y.Z linux|darwin amd64|arm64 [output]\n' >&2; exit 1; }
release_version=$1
target_os=$2
target_arch=$3
[[ $release_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]] || { printf 'Invalid release version\n' >&2; exit 1; }
case "$target_os" in linux) buf_os=Linux ;; darwin) buf_os=Darwin ;; *) exit 1 ;; esac
case "$target_arch" in amd64) buf_arch=x86_64 ;; arm64) buf_arch=aarch64; [[ $target_os != darwin ]] || buf_arch=arm64 ;; *) exit 1 ;; esac

cd "$(dirname "$0")/.."
output=${4:-dist/release}
mkdir -p "$output"
output=$(cd "$output" && pwd)
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
bundle=$temporary/bundle
mkdir -p "$bundle/bin" "$bundle/lib" "$bundle/licenses"

CGO_ENABLED=0 GOOS=$target_os GOARCH=$target_arch go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$release_version" -o "$bundle/bin/keel" ./cmd/keel
CGO_ENABLED=0 GOOS=$target_os GOARCH=$target_arch go build -trimpath -ldflags='-s -w' -o "$bundle/bin/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go

buf_version=v1.65.0
buf_asset=buf-$buf_os-$buf_arch
buf_url=https://github.com/bufbuild/buf/releases/download/$buf_version
curl -fLsS --retry 3 "$buf_url/$buf_asset" -o "$temporary/$buf_asset"
curl -fLsS --retry 3 "$buf_url/sha256.txt" -o "$temporary/sha256.txt"
expected=$(awk -v asset="$buf_asset" '$2 == asset { print $1; count++ } END { if (count != 1) exit 1 }' "$temporary/sha256.txt")
actual=$(shasum -a 256 "$temporary/$buf_asset" | awk '{ print $1 }')
[[ $expected == "$actual" ]] || { printf 'Buf checksum mismatch\n' >&2; exit 1; }
mv "$temporary/$buf_asset" "$bundle/bin/buf"
chmod +x "$bundle/bin/buf"

# Bundle the existing locked SDK generator and its dependencies into one Node entry point.
bun -e '
const result = await Bun.build({
  entrypoints: ["./sdks/typescript/node_modules/@bufbuild/protoc-gen-es/bin/protoc-gen-es"],
  target: "node", format: "cjs",
  plugins: [{ name: "node-cli", setup(build) {
    build.onLoad({ filter: /\/bin\/protoc-gen-es$/ }, async args => ({ contents: await Bun.file(args.path).text(), loader: "js" }));
  } }],
});
if (!result.success || result.outputs.length !== 1) throw new AggregateError(result.logs, "generator bundling failed");
await Bun.write(process.argv[1], result.outputs[0]);
' "$bundle/lib/protoc-gen-es.cjs"
cat > "$bundle/bin/protoc-gen-es" <<'SH'
#!/bin/sh
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
command -v node >/dev/null 2>&1 || { printf 'TypeScript generation requires Node.js 22 or newer\n' >&2; exit 1; }
exec node "$directory/../lib/protoc-gen-es.cjs" "$@"
SH
chmod +x "$bundle/bin/protoc-gen-es"

cp LICENSE "$bundle/LICENSE"
curl -fLsS --retry 3 "https://raw.githubusercontent.com/bufbuild/buf/$buf_version/LICENSE" -o "$bundle/licenses/buf.txt"
curl -fLsS --retry 3 'https://raw.githubusercontent.com/bufbuild/protobuf-es/v2.16.0/LICENSE' -o "$bundle/licenses/protobuf-es.txt"
protobuf_module=$(go list -m -f '{{.Dir}}' google.golang.org/protobuf)
cp "$protobuf_module/LICENSE" "$bundle/licenses/protobuf-go.txt"
cp sdks/typescript/node_modules/@bufbuild/protoplugin/node_modules/typescript/LICENSE.txt "$bundle/licenses/typescript.txt"
cp sdks/typescript/node_modules/@typescript/vfs/LICENSE "$bundle/licenses/typescript-vfs.txt"
printf '%s\n' "$release_version" > "$bundle/VERSION"
tar -czf "$output/keel_${target_os}_${target_arch}.tar.gz" -C "$bundle" bin lib licenses LICENSE VERSION
printf 'Built %s/keel_%s_%s.tar.gz\n' "$output" "$target_os" "$target_arch"
