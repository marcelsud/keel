#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
native="$root/.keel/native"
source_dir="$root/.keel/build/dqlite-1.18.2"
archive="$root/.keel/build/dqlite-1.18.2.tar.gz"
output=${1:-"$root/.keel/bin/keel"}
digest=ce569a30958b6f673cbb7f8632706bc6868bc5b82ae4b04e0f63f3ad24125aaf

if [ "$(uname -s)" != Linux ]; then
  printf '%s\n' 'The embedded dqlite cluster build currently targets Linux (including WSL).' >&2
  exit 1
fi

if [ ! -f "$native/lib/libdqlite.a" ] || [ "$(cat "$native/source.sha256" 2>/dev/null || true)" != "$digest" ]; then
  pkg-config --exists 'sqlite3 >= 3.34.0' 'libuv >= 1.34.0' liblz4
  mkdir -p "$source_dir" "$native" "$(dirname -- "$archive")"
  curl -fsSL https://github.com/canonical/dqlite/archive/refs/tags/v1.18.2.tar.gz -o "$archive"
  printf '%s  %s\n' "$digest" "$archive" | sha256sum -c -
  tar -xzf "$archive" --strip-components=1 -C "$source_dir"
  (
    cd "$source_dir"
    autoreconf -fi
    ./configure --prefix="$native" --disable-shared --enable-static
    make -j"${KEEL_BUILD_JOBS:-4}"
    make install
  )
  printf '%s\n' "$digest" > "$native/source.sha256"
fi

mkdir -p "$(dirname -- "$output")"
cd "$root"
CGO_ENABLED=1 \
CGO_CFLAGS="-I$native/include ${CGO_CFLAGS:-}" \
CGO_LDFLAGS="-L$native/lib ${CGO_LDFLAGS:-}" \
go build -buildvcs=false -tags keel_dqlite,nosqlite3,netgo,osusergo \
  -ldflags '-s -w -linkmode external -extldflags "-static -lsqlite3 -luv -llz4 -lpthread -ldl -lrt -lm"' \
  -o "$output" ./cmd/keel
printf '%s\n' "$output"
