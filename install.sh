#!/bin/sh
set -eu

die() { printf 'keel: %s\n' "$*" >&2; exit 1; }

valid_version() {
    case "$1" in
        *[!a-zA-Z0-9._-]* | '') return 1 ;;
        v[0-9]*) return 0 ;;
        *) return 1 ;;
    esac
}

add_path() {
    profile=$1
    if ! grep -Fqx "$path_line" "$profile" 2>/dev/null; then
        printf '\n# Keel\n%s\n' "$path_line" >> "$profile"
        printf 'Added Keel to PATH in %s\n' "$profile"
    fi
}

main() {
    case "${1:-}" in
        -h | --help)
            printf 'Usage: sh install.sh [vX.Y.Z]\n\nDefaults to the latest stable GitHub release.\nKEEL_BIN_DIR changes ~/.local/bin; KEEL_INSTALL_DIR changes ~/.local/share/keel.\nSet KEEL_NO_MODIFY_PATH=1 to leave shell profiles unchanged.\n'
            return ;;
    esac
    [ "$#" -le 1 ] || die 'expected at most one version argument'
    requested=${1:-latest}
    if [ "$requested" != latest ]; then
        valid_version "$requested" || die 'version must look like v0.1.0'
    fi
    case "$(uname -s)" in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        *) die 'supported systems: Linux, macOS, and WSL' ;;
    esac
    case "$(uname -m)" in
        x86_64 | amd64) arch=amd64 ;;
        arm64 | aarch64) arch=arm64 ;;
        *) die 'supported architectures: amd64 and arm64' ;;
    esac
    [ "$os/$arch" != darwin/amd64 ] || die 'SQL-enabled CLI releases support macOS Apple Silicon; Intel Macs are not supported'
    for dependency in curl tar awk mktemp; do
        command -v "$dependency" >/dev/null 2>&1 || die "$dependency is required"
    done
    if command -v sha256sum >/dev/null 2>&1; then
        hash_command=sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        hash_command=shasum
    else
        die 'sha256sum or shasum is required'
    fi
    : "${HOME:?HOME must be set}"
    install_dir=${KEEL_INSTALL_DIR:-$HOME/.local/share/keel}
    bin_dir=${KEEL_BIN_DIR:-$HOME/.local/bin}
    case "$install_dir:$bin_dir" in
        *'
'*) die 'installation paths must not contain newlines' ;;
    esac
    case "$install_dir" in /*) ;; *) die 'KEEL_INSTALL_DIR must be absolute' ;; esac
    case "$bin_dir" in /*) ;; *) die 'KEEL_BIN_DIR must be absolute' ;; esac

    download=https://github.com/marcelsud/keel/releases
    if [ "$requested" = latest ]; then
        download=$download/latest/download
    else
        download=$download/download/keel-$requested
    fi
    asset=keel_${os}_${arch}.tar.gz
    temporary=$(mktemp -d)
    trap 'rm -rf "$temporary"' EXIT
    trap 'exit 1' HUP INT TERM
    printf 'Downloading Keel for %s/%s...\n' "$os" "$arch"
    curl -fLsS --retry 3 --connect-timeout 15 --max-time 180 "$download/$asset" -o "$temporary/$asset" || die 'release download failed; check that a CLI release has been published'
    curl -fLsS --retry 3 --connect-timeout 15 --max-time 60 "$download/checksums.txt" -o "$temporary/checksums.txt" || die 'checksum download failed'
    expected=$(awk -v asset="$asset" '$2 == asset { print $1; count++ } END { if (count != 1) exit 1 }' "$temporary/checksums.txt") || die 'release checksum is missing or ambiguous'
    if [ "$hash_command" = sha256sum ]; then
        actual=$(sha256sum "$temporary/$asset" | awk '{ print $1 }')
    else
        actual=$(shasum -a 256 "$temporary/$asset" | awk '{ print $1 }')
    fi
    [ "$actual" = "$expected" ] || die 'release checksum mismatch; installation unchanged'
    mkdir "$temporary/unpack"
    tar -xzf "$temporary/$asset" -C "$temporary/unpack"
    release=$(cat "$temporary/unpack/VERSION")
    valid_version "$release" || die 'invalid release version'
    [ "$requested" = latest ] || [ "$release" = "$requested" ] || die 'downloaded release version does not match the requested version'
    for tool in keel buf protoc-gen-go protoc-gen-es; do
        [ -x "$temporary/unpack/bin/$tool" ] || die "release is missing $tool"
    done
    [ -f "$temporary/unpack/lib/protoc-gen-es.cjs" ] || die 'release is missing the TypeScript generator'
    [ "$("$temporary/unpack/bin/keel" --version)" = "keel $release" ] || die 'release executable version does not match its archive'

    mkdir -p "$install_dir/releases" "$bin_dir"
    [ ! -d "$bin_dir/keel" ] || die "$bin_dir/keel is a directory"
    destination=$install_dir/releases/$release
    printf '%s\n' "$actual" > "$temporary/unpack/.archive.sha256"
    if [ -e "$destination" ]; then
        cmp -s "$temporary/unpack/.archive.sha256" "$destination/.archive.sha256" || die "$destination already contains a different installation"
    else
        mv "$temporary/unpack" "$destination"
    fi
    ln -s "$destination/bin/keel" "$temporary/keel"
    mv -f "$temporary/keel" "$bin_dir/keel"
    printf 'Installed Keel %s in %s\n' "$release" "$bin_dir"

    case ":$PATH:" in
        *":$bin_dir:"*) ;;
        *)
            quoted_bin=$(printf '%s' "$bin_dir" | sed "s/'/'\\\\''/g")
            path_line="export PATH='$quoted_bin':\"\$PATH\""
            if [ "${KEEL_NO_MODIFY_PATH:-0}" != 1 ]; then
                case "${SHELL:-}" in
                    */zsh)
                        mkdir -p "${ZDOTDIR:-$HOME}"
                        add_path "${ZDOTDIR:-$HOME}/.zshrc" ;;
                    */bash)
                        add_path "$HOME/.bashrc"
                        if [ -f "$HOME/.bash_profile" ]; then
                            add_path "$HOME/.bash_profile"
                        elif [ -f "$HOME/.bash_login" ]; then
                            add_path "$HOME/.bash_login"
                        else
                            add_path "$HOME/.profile"
                        fi ;;
                    *) add_path "$HOME/.profile" ;;
                esac
                printf 'Open a new terminal, then run: keel --version\n'
            else
                printf 'Add Keel to PATH:\n%s\n' "$path_line"
            fi ;;
    esac
}

main "$@"
