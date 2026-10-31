#!/bin/sh
# agentbox installer for Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/ThembinkosiThemba/agentbox/main/install.sh | sh
#
# Environment:
#   AGENTBOX_VERSION          version to install, e.g. 0.2.0 (default: latest)
#   AGENTBOX_INSTALL_DIR      where to put the binary (default: ~/.local/bin)
#   AGENTBOX_NO_MODIFY_PATH   set to 1 to leave shell config files alone
#   AGENTBOX_BASE_URL         download from this URL instead of GitHub releases
set -eu

REPO="ThembinkosiThemba/agentbox"
RAW="https://raw.githubusercontent.com/$REPO/main"

say() { printf '%s\n' "$*"; }
fail() { printf 'agentbox install: %s\n' "$*" >&2; exit 1; }

download() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		fail "curl or wget is required"
	fi
}

main() {
	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	MINGW* | MSYS* | CYGWIN*)
		# Git Bash and friends: install the Windows way so agentbox is on the
		# Windows PATH and works in every terminal, not just this one.
		say "Windows detected; running install.ps1"
		exec powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "irm $RAW/install.ps1 | iex"
		;;
	*) fail "unsupported OS: $(uname -s)" ;;
	esac

	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "unsupported CPU architecture: $(uname -m)" ;;
	esac

	version="${AGENTBOX_VERSION:-latest}"
	version="${version#v}"
	if [ -n "${AGENTBOX_BASE_URL:-}" ]; then
		base="$AGENTBOX_BASE_URL"
	elif [ "$version" = latest ]; then
		base="https://github.com/$REPO/releases/latest/download"
	else
		base="https://github.com/$REPO/releases/download/v$version"
	fi

	asset="agentbox-$os-$arch"
	dir="${AGENTBOX_INSTALL_DIR:-$HOME/.local/bin}"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Downloading $asset ($version)"
	download "$base/$asset" "$tmp/$asset" || fail "download failed: $base/$asset"
	verify "$base" "$asset" "$tmp"

	mkdir -p "$dir"
	chmod +x "$tmp/$asset"
	[ "$os" = darwin ] && xattr -d com.apple.quarantine "$tmp/$asset" 2>/dev/null || true
	mv -f "$tmp/$asset" "$dir/agentbox"
	say "Installed $dir/agentbox"

	setup_path "$dir"

	say ""
	"$dir/agentbox" version 2>/dev/null || true
	say "Run 'agentbox' to open the dashboard, or 'agentbox guide' to set up your agents."
}

verify() { # base asset tmpdir
	if ! download "$1/checksums.txt" "$3/checksums.txt" 2>/dev/null; then
		say "warning: checksums.txt not found; skipping verification"
		return
	fi
	want="$(awk -v f="$2" '$2 == f || $2 == "*" f { print $1 }' "$3/checksums.txt")"
	[ -n "$want" ] || fail "$2 is not listed in checksums.txt"
	if command -v sha256sum >/dev/null 2>&1; then
		got="$(sha256sum "$3/$2" | awk '{print $1}')"
	elif command -v shasum >/dev/null 2>&1; then
		got="$(shasum -a 256 "$3/$2" | awk '{print $1}')"
	else
		say "warning: no sha256sum or shasum; skipping verification"
		return
	fi
	[ "$got" = "$want" ] || fail "checksum mismatch for $2 (expected $want, got $got)"
	say "Checksum verified"
}

# setup_path makes sure new shells find agentbox, by adding the install
# directory to the user's shell config when it is not already on PATH.
setup_path() {
	dir="$1"
	case ":$PATH:" in
	*":$dir:"*) return ;;
	esac
	if [ "${AGENTBOX_NO_MODIFY_PATH:-}" = 1 ]; then
		say "Add $dir to your PATH to run agentbox from anywhere."
		return
	fi

	line="export PATH=\"$dir:\$PATH\""
	case "$(basename "${SHELL:-sh}")" in
	zsh) files="${ZDOTDIR:-$HOME}/.zshrc" ;;
	bash)
		files="$HOME/.bashrc"
		# macOS terminals start login shells, which read .bash_profile.
		[ "$(uname -s)" = Darwin ] && files="$files $HOME/.bash_profile"
		;;
	fish)
		files="${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/agentbox.fish"
		line="fish_add_path \"$dir\""
		;;
	*) files="$HOME/.profile" ;;
	esac

	for f in $files; do
		if [ -f "$f" ] && grep -qF "$line" "$f"; then
			continue
		fi
		mkdir -p "$(dirname "$f")"
		printf '\n# agentbox\n%s\n' "$line" >>"$f"
		say "Added $dir to PATH in $f"
	done
	say "Open a new terminal, or run:  $line"
}

main "$@"
