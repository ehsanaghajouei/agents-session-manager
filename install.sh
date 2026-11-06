#!/bin/sh
# Installs asm from the latest GitHub release into ~/.local/bin, and installs
# tmux with the system package manager when it is missing.
#
#   curl -fsSL https://raw.githubusercontent.com/ehsanaghajouei/agents-session-manager/main/install.sh | sh
#
# Supports Linux and macOS on amd64 and arm64. Run it again to upgrade.
set -eu

repo=ehsanaghajouei/agents-session-manager
bin_dir="$HOME/.local/bin"

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}
has() { command -v "$1" >/dev/null 2>&1; }

detect_platform() {
	case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported OS $(uname -s); asm runs on Linux and macOS" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported CPU $(uname -m); asm is built for amd64 and arm64" ;;
	esac
}

download() { # url file
	if has curl; then
		curl -fsSL -o "$2" "$1" || die "downloading $1 failed"
	else
		wget -q -O "$2" "$1" || die "downloading $1 failed"
	fi
}

sha256() {
	if has sha256sum; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

cleanup() {
	rm -rf "$tmp"
	rm -f "$new"
}

install_asm() {
	archive="asm_${os}_${arch}.tar.gz"
	url="https://github.com/$repo/releases/latest/download"
	say "Downloading $archive"
	download "$url/$archive" "$tmp/$archive"
	download "$url/checksums.txt" "$tmp/checksums.txt"
	want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "$archive is not listed in checksums.txt"
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || die "checksum mismatch for $archive"
	tar -xzf "$tmp/$archive" -C "$tmp" asm

	# Copy next to the target and rename, so a running asm is replaced safely.
	mkdir -p "$bin_dir"
	new=$(mktemp "$bin_dir/.asm.XXXXXX")
	cp "$tmp/asm" "$new"
	chmod 755 "$new"
	mv -f "$new" "$bin_dir/asm"
	say "Installed $("$bin_dir/asm" -version) to $bin_dir/asm"
}

install_tmux() {
	has tmux && return
	if [ "$os" = darwin ]; then
		if ! has brew; then
			warn "tmux is missing and Homebrew is not installed; install tmux yourself"
			return
		fi
		say "Installing tmux with Homebrew"
		brew install tmux || warn "installing tmux failed"
		return
	fi
	sudo=
	if [ "$(id -u)" -ne 0 ]; then
		if ! has sudo; then
			warn "tmux is missing and sudo is not available; install tmux as root"
			return
		fi
		sudo=sudo
	fi
	for pm in apt-get dnf yum pacman zypper apk; do
		has "$pm" || continue
		say "Installing tmux with $pm"
		case $pm in
		apt-get) $sudo apt-get update && $sudo apt-get install -y tmux ;;
		pacman) $sudo pacman -S --needed --noconfirm tmux ;;
		apk) $sudo apk add tmux ;;
		*) $sudo "$pm" install -y tmux ;;
		esac || warn "installing tmux failed"
		return
	done
	warn "tmux is missing and no supported package manager was found; install tmux yourself"
}

main() {
	detect_platform
	has curl || has wget || die "curl or wget is required"
	has sha256sum || has shasum || die "sha256sum or shasum is required"
	has tar || die "tar is required"

	tmp=$(mktemp -d)
	new=
	trap cleanup EXIT
	install_asm
	install_tmux

	has claude || warn "claude is not in PATH; asm needs Claude Code: https://claude.com/claude-code"
	case ":$PATH:" in
	*":$bin_dir:"*) ;;
	*) warn "$bin_dir is not in PATH; add it to PATH in your shell's startup file" ;;
	esac
}

main "$@"
