#!/bin/sh
# Installs Seelegent on Linux and macOS: downloads a release, verifies its SHA-256, installs the seelegent binary,
# puts its folder on PATH, imports the old (Node) version's data and registers Seelegent with the lead hosts.
#
#   curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version v1.2.3 --no-modify-path
#
# Options (or environment variables):
#   --version vX.Y.Z    SEELEGENT_VERSION          the release to install (default: the latest)
#   --install-dir DIR   SEELEGENT_INSTALL_DIR      where the binary goes (default: ~/.local/bin)
#   --no-modify-path    SEELEGENT_NO_MODIFY_PATH=1 do not add the folder to PATH in your shell's profile
#   --no-setup          SEELEGENT_NO_SETUP=1       do not run `seelegent setup`
# SEELEGENT_INSTALL_URL (else SEELEGENT_UPDATE_URL) replaces the release information address
# https://api.github.com/repos/NovaSeele/Seelegent/releases/latest, for example with a local test server.
#
# POSIX sh (dash, bash, zsh, busybox). Any failure exits non-zero. See docs/release.md.

set -eu

REPO_LATEST="https://api.github.com/repos/NovaSeele/Seelegent/releases/latest"
USER_AGENT="seelegent-installer"

say() { printf '%s\n' "$*"; }
warn() { printf 'Warning: %s\n' "$*" >&2; }
die() {
	printf 'Error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Usage: install.sh [--version vX.Y.Z] [--install-dir DIR] [--no-modify-path] [--no-setup]

  --version vX.Y.Z    the release to install (SEELEGENT_VERSION; default: the latest)
  --install-dir DIR   where the binary goes (SEELEGENT_INSTALL_DIR; default: ~/.local/bin)
  --no-modify-path    do not add the folder to PATH (SEELEGENT_NO_MODIFY_PATH=1)
  --no-setup          do not run `seelegent setup` (SEELEGENT_NO_SETUP=1)

SEELEGENT_INSTALL_URL (else SEELEGENT_UPDATE_URL) replaces the release information address.
EOF
}

# check_url accepts https, and http only on this machine (a local test server).
check_url() {
	case "$1" in
	https://*) return 0 ;;
	http://127.* | http://localhost[:/]* | http://localhost | http://\[::1\]*) return 0 ;;
	esac
	die "only https addresses (or http on this machine) are allowed: $1"
}

# normalize_tag prints vX.Y.Z[-pre] for X.Y.Z or vX.Y.Z (build metadata dropped), or fails.
normalize_tag() {
	nt_v=${1#v}
	nt_v=${nt_v#V}
	nt_v=${nt_v%%+*}
	if ! printf '%s\n' "$nt_v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
		die "'$1' is not a version like v1.2.3"
	fi
	printf 'v%s\n' "$nt_v"
}

# fetch URL FILE downloads URL into FILE (curl or wget), following a few redirects.
fetch() {
	check_url "$1"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --max-redirs 5 -A "$USER_AGENT" -H 'Accept: application/vnd.github+json, */*' -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q --max-redirect=5 -U "$USER_AGENT" -O "$2" "$1"
	else
		die "neither curl nor wget is installed"
	fi
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/^.*= *//'
	else
		die "no SHA-256 tool (sha256sum, shasum or openssl) is installed"
	fi
}

# json_strings KEY FILE prints every string value of KEY in a JSON file, one per line. Enough for GitHub's
# release JSON (no escaped quotes in tags, names or URLs), pretty-printed or on one line.
json_strings() {
	tr ',{}' '\n\n\n' <"$2" |
		sed -n "s/^[[:space:]]*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

detect_platform() {
	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "this system ($(uname -s)) has no Seelegent build; on Windows use install.ps1" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "this processor ($(uname -m)) has no Seelegent build; releases exist for amd64 and arm64" ;;
	esac
	# A shell running under Rosetta reports x86_64 on an Apple silicon Mac.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
		arch=arm64
	fi
}

# path_has DIR reports whether DIR is on PATH.
path_has() {
	case ":${PATH}:" in
	*":$1:"*) return 0 ;;
	esac
	return 1
}

# profile_file prints the startup file of the user's shell that should get the PATH line.
profile_file() {
	case "$(basename "${SHELL:-sh}")" in
	zsh) printf '%s\n' "${ZDOTDIR:-$HOME}/.zshrc" ;;
	bash)
		if [ "$os" = darwin ]; then
			printf '%s\n' "$HOME/.bash_profile"
		else
			printf '%s\n' "$HOME/.bashrc"
		fi
		;;
	fish) printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/seelegent.fish" ;;
	*) printf '%s\n' "$HOME/.profile" ;;
	esac
}

# add_to_path DIR appends a PATH line for DIR to the shell's startup file, once. Prints the file it changed.
add_to_path() {
	ap_file=$(profile_file)
	if [ -f "$ap_file" ] && grep -Fq "$1" "$ap_file"; then
		return 0
	fi
	mkdir -p "$(dirname "$ap_file")"
	case "$ap_file" in
	*.fish) ap_line="fish_add_path \"$1\"" ;;
	*) ap_line="export PATH=\"$1:\$PATH\"" ;;
	esac
	printf '\n# Added by the Seelegent installer\n%s\n' "$ap_line" >>"$ap_file"
	printf '%s\n' "$ap_file"
}

# install_binary NEW EXE puts NEW in place of EXE; the replaced binary is kept as EXE.previous for
# `seelegent update --rollback`. Renames only, so a running seelegent is replaced safely.
install_binary() {
	ib_staged="$2.new"
	rm -f "$ib_staged"
	cp "$1" "$ib_staged"
	chmod 755 "$ib_staged"
	if [ -e "$2" ]; then
		rm -f "$2.previous"
		mv -f "$2" "$2.previous" || {
			rm -f "$ib_staged"
			die "could not move the installed $2 aside; nothing was changed"
		}
		previous="$2.previous"
	fi
	if ! mv -f "$ib_staged" "$2"; then
		[ -n "$previous" ] && mv -f "$previous" "$2"
		die "could not put the new seelegent in place; the old one is back"
	fi
}

main() {
	version=${SEELEGENT_VERSION:-}
	install_dir=${SEELEGENT_INSTALL_DIR:-}
	no_path=${SEELEGENT_NO_MODIFY_PATH:-0}
	no_setup=${SEELEGENT_NO_SETUP:-0}
	while [ $# -gt 0 ]; do
		case "$1" in
		--version)
			[ $# -ge 2 ] || die "--version needs a value"
			version=$2
			shift 2
			;;
		--version=*)
			version=${1#*=}
			shift
			;;
		--install-dir)
			[ $# -ge 2 ] || die "--install-dir needs a value"
			install_dir=$2
			shift 2
			;;
		--install-dir=*)
			install_dir=${1#*=}
			shift
			;;
		--no-modify-path)
			no_path=1
			shift
			;;
		--no-setup)
			no_setup=1
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		*) die "unknown option $1 (see --help)" ;;
		esac
	done
	[ -n "${HOME:-}" ] || die "HOME is not set"
	# A worker must not replace the binary or stop the daemon that runs it (old C-B12).
	[ "${SEELEGENT_WORKER:-}" != 1 ] || die "Seelegent is not available inside a Seelegent worker."

	detect_platform
	tag=""
	if [ -n "$version" ]; then
		tag=$(normalize_tag "$version")
	fi
	meta_url=${SEELEGENT_INSTALL_URL:-${SEELEGENT_UPDATE_URL:-$REPO_LATEST}}
	if [ -n "$tag" ]; then
		case "$meta_url" in
		*/releases/latest | */releases/latest/) meta_url="${meta_url%/}" && meta_url="${meta_url%/releases/latest}/releases/tags/$tag" ;;
		esac
	fi

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t seelegent-install)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' HUP INT TERM

	say "Reading the release information from $meta_url ..."
	fetch "$meta_url" "$tmp/release.json" || die "could not read the release information from $meta_url"
	raw_tag=$(json_strings tag_name "$tmp/release.json" | head -n 1)
	[ -n "$raw_tag" ] || raw_tag=$(json_strings name "$tmp/release.json" | head -n 1)
	[ -n "$raw_tag" ] || die "the release information at $meta_url names no version"
	rel_tag=$(normalize_tag "$raw_tag")
	if [ -n "$tag" ] && [ "$rel_tag" != "$tag" ]; then
		die "the release at $meta_url is $rel_tag, not $tag"
	fi
	asset="seelegent_${rel_tag#v}_${os}_${arch}.tar.gz"
	urls=$(json_strings browser_download_url "$tmp/release.json")
	asset_url=$(printf '%s\n' "$urls" | grep -F "/$asset" | head -n 1 || true)
	sums_url=$(printf '%s\n' "$urls" | grep -F "/checksums.txt" | head -n 1 || true)
	[ -n "$asset_url" ] || die "release $rel_tag has no $asset"
	[ -n "$sums_url" ] || die "release $rel_tag has no checksums.txt; refusing to install a file that cannot be verified"

	say "Downloading $asset ..."
	fetch "$asset_url" "$tmp/$asset" || die "could not download $asset_url"
	fetch "$sums_url" "$tmp/checksums.txt" || die "could not download $sums_url"
	want=$(awk -v name="$asset" '{ n = $2; sub(/^\*/, "", n); if (n == name) { print tolower($1); exit } }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "checksums.txt does not list $asset; refusing to install a file that cannot be verified"
	got=$(sha256_of "$tmp/$asset")
	if [ "$got" != "$want" ]; then
		die "SHA-256 mismatch for $asset: expected $want, got $got. The download is damaged or was changed; nothing was installed"
	fi
	say "Checksum verified (SHA-256)."

	mkdir -p "$tmp/x"
	tar -xzf "$tmp/$asset" -C "$tmp/x"
	new_bin=""
	for candidate in "$tmp/x/seelegent" "$tmp"/x/*/seelegent; do
		if [ -f "$candidate" ]; then
			new_bin=$candidate
			break
		fi
	done
	[ -n "$new_bin" ] || die "$asset holds no seelegent binary"
	chmod 755 "$new_bin"
	reported=$("$new_bin" version --json 2>/dev/null | sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' || true)
	if [ "$reported" != "$rel_tag" ]; then
		die "the downloaded seelegent does not run here or is not $rel_tag (it reported '$reported'); nothing was installed"
	fi

	install_dir=${install_dir:-$HOME/.local/bin}
	mkdir -p "$install_dir"
	install_dir=$(cd "$install_dir" && pwd -P)
	exe="$install_dir/seelegent"
	was_running=0
	previous=""
	if [ -e "$exe" ]; then
		rm -f "$exe".old-* "$exe.new"
		if stop_out=$("$exe" stop --json 2>/dev/null); then
			if printf '%s\n' "$stop_out" | grep -Eq '"stopped"[[:space:]]*:[[:space:]]*true'; then
				was_running=1
				say "Stopped the running daemon."
			fi
		else
			warn "the running Seelegent daemon could not be stopped: $stop_out"
		fi
	fi
	say "Installing $exe ..."
	install_binary "$new_bin" "$exe"
	if [ -n "$previous" ]; then
		say "The previous binary is kept as $previous (seelegent update --rollback)."
	fi

	path_file=""
	if [ "$no_path" = 1 ]; then
		say "PATH left alone (--no-modify-path)."
	elif ! path_has "$install_dir"; then
		path_file=$(add_to_path "$install_dir")
		if [ -n "$path_file" ]; then
			say "Added $install_dir to PATH in $path_file."
		fi
	fi
	if path_has "$install_dir"; then
		found=$(command -v seelegent || true)
		if [ "$found" != "$exe" ]; then
			warn "another seelegent comes first on PATH: $found. Remove it or move $install_dir before it."
		fi
	fi

	failed=""
	data_dir=${SEELEGENT_HOME:-$HOME/.seelegent}
	old_data=0
	for marker in settings.json accounts.json roles.json memory; do
		[ -e "$data_dir/$marker" ] && old_data=1
	done
	if [ "$old_data" = 1 ] && [ ! -e "$data_dir/seelegent.db" ]; then
		say "Importing the old version's data from $data_dir (seelegent migrate) ..."
		"$exe" migrate || failed="$failed seelegent-migrate"
	fi
	if [ "$no_setup" = 1 ]; then
		say "Lead hosts left alone (--no-setup)."
	else
		say "Registering Seelegent with the lead hosts (seelegent setup) ..."
		"$exe" setup || failed="$failed seelegent-setup"
	fi
	if [ "$was_running" = 1 ]; then
		"$exe" restart || failed="$failed seelegent-restart"
	fi
	if ! status_out=$("$exe" status --json 2>/dev/null); then
		warn "the daemon cannot start yet: $(printf '%s\n' "$status_out" | sed -n 's/^[[:space:]]*"error"[[:space:]]*:[[:space:]]*"\(.*\)",\{0,1\}$/\1/p')"
	fi

	say ""
	say "Seelegent $rel_tag is installed: $exe"
	say "Next steps:"
	if [ -n "$path_file" ]; then
		say "  - Open a new terminal (or run: . \"$path_file\") so it picks up the new PATH."
	elif ! path_has "$install_dir"; then
		say "  - Add $install_dir to PATH, or run $exe directly."
	fi
	[ "$no_setup" = 1 ] && say "  - Run \`seelegent setup\` to register Seelegent with your lead hosts."
	say "  - Restart your lead hosts (Claude Code, Codex, ...) so they load the Seelegent MCP server."
	say "  - \`seelegent doctor\` checks the installation; \`seelegent ui\` opens the dashboard."
	say "  - \`seelegent update\` checks for new releases later."
	if [ -n "$failed" ]; then
		die "Seelegent $rel_tag is installed, but these steps failed:$failed. Run them again after fixing what they reported"
	fi
}

main "$@"
