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
#   --force             SEELEGENT_FORCE=1          install a release that does not report which database schema
#                                                  it knows (a release known to be too old is refused even then)
# SEELEGENT_INSTALL_URL (else SEELEGENT_UPDATE_URL) replaces the release information address
# https://api.github.com/repos/NovaSeele/Seelegent/releases/latest, for example with a local test server.
#
# POSIX sh (dash, bash, zsh, busybox). Downloads use curl, else GNU wget. Any failure exits non-zero. See
# docs/release.md.

set -eu

REPO_LATEST="https://api.github.com/repos/NovaSeele/Seelegent/releases/latest"
USER_AGENT="seelegent-installer"
MAX_REDIRECTS=5
NL='
'

say() { printf '%s\n' "$*"; }
warn() { printf 'Warning: %s\n' "$*" >&2; }
die() {
	printf 'Error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Usage: install.sh [--version vX.Y.Z] [--install-dir DIR] [--no-modify-path] [--no-setup] [--force]

  --version vX.Y.Z    the release to install (SEELEGENT_VERSION; default: the latest)
  --install-dir DIR   where the binary goes (SEELEGENT_INSTALL_DIR; default: ~/.local/bin)
  --no-modify-path    do not add the folder to PATH (SEELEGENT_NO_MODIFY_PATH=1)
  --no-setup          do not run `seelegent setup` (SEELEGENT_NO_SETUP=1)
  --force             install a release that does not report which database schema it knows
                      (SEELEGENT_FORCE=1)

SEELEGENT_INSTALL_URL (else SEELEGENT_UPDATE_URL) replaces the release information address.
EOF
}

# url_authority URL prints the authority of URL (what lies between :// and the next /, ? or #).
url_authority() {
	ua_rest=${1#*://}
	ua_rest=${ua_rest%%/*}
	ua_rest=${ua_rest%%\?*}
	printf '%s\n' "${ua_rest%%#*}"
}

# check_url accepts https, and http only on this machine (a local test server): exactly 127.0.0.1, localhost or
# [::1], with an optional numeric port. An address with user information (user@host) is refused, so
# http://127.0.0.1@example.com/ cannot pass as local.
check_url() {
	cu_auth=$(url_authority "$1")
	case "$cu_auth" in
	'' | *@* | *\\* | *' '*) die "not an allowed address: $1" ;;
	esac
	case "$1" in
	https://*) return 0 ;;
	http://*)
		cu_host=$cu_auth
		cu_port=""
		case "$cu_auth" in
		'[::1]') ;;
		'[::1]:'*)
			cu_host='[::1]'
			cu_port=${cu_auth#'[::1]:'}
			;;
		*:*)
			cu_host=${cu_auth%%:*}
			cu_port=${cu_auth#*:}
			;;
		esac
		case "$cu_port" in
		*[!0-9]*) ;;
		*)
			case "$cu_host" in
			127.0.0.1 | localhost | '[::1]') return 0 ;;
			esac
			;;
		esac
		;;
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

# fetch URL FILE downloads URL into FILE with curl or GNU wget. Redirects (at most MAX_REDIRECTS) are followed here,
# not by the tool, so every address on the way passes check_url: https, or http only on this machine (old C-B15).
fetch() {
	f_url=$1
	f_hops=0
	while :; do
		check_url "$f_url"
		if command -v curl >/dev/null 2>&1; then
			f_res=$(curl -fsS --proto '=http,https' -A "$USER_AGENT" -H 'Accept: application/vnd.github+json, */*' \
				-o "$2" -w '%{http_code} %{redirect_url}' "$f_url") || return 1
			f_code=${f_res%% *}
			f_next=${f_res#* }
		elif command -v wget >/dev/null 2>&1; then
			wget -q -S --max-redirect=0 -U "$USER_AGENT" -O "$2" "$f_url" 2>"$2.headers" || true
			f_code=$(sed -n 's/^ *HTTP\/[0-9.]* \([0-9][0-9][0-9]\).*/\1/p' "$2.headers" | tail -n 1)
			f_next=$(sed -n 's/^ *[Ll]ocation: *//p' "$2.headers" | tr -d '\r' | tail -n 1)
			rm -f "$2.headers"
		else
			die "neither curl nor wget is installed"
		fi
		case "$f_code" in
		2??) return 0 ;;
		301 | 302 | 303 | 307 | 308) ;;
		*) return 1 ;;
		esac
		case "$f_next" in
		*://*) ;;
		/*) f_next="${f_url%%://*}://$(url_authority "$f_url")$f_next" ;;
		*) die "unsupported redirect from $f_url to '$f_next'" ;;
		esac
		f_hops=$((f_hops + 1))
		[ "$f_hops" -le "$MAX_REDIRECTS" ] || die "$1 redirects more than $MAX_REDIRECTS times"
		f_url=$f_next
	done
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
	# Each of the three characters becomes a line break: the repeated \n is deliberate (one per character, the
	# portable spelling; POSIX leaves a shorter second set unspecified), not a word replacement.
	# shellcheck disable=SC2020
	tr ',{}' '\n\n\n' <"$2" |
		sed -n "s/^[[:space:]]*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

# check_install_dir DIR stops when DIR holds a character the shell would expand inside the double quotes of the PATH
# line written to a shell profile. It runs on the folder as given and again once links are resolved (pwd -P).
check_install_dir() {
	case "$1" in
	*'"'* | *'$'* | *'`'* | *\\* | *"$NL"*)
		die "the install folder may not contain \", \$, \`, a backslash or a line break: $1"
		;;
	esac
}

# url_ending NAME prints the first line of stdin that ends with /NAME (an exact file name, so seelegent_x.tar.gz
# never picks seelegent_x.tar.gz.sig).
url_ending() {
	awk -v want="/$1" '{
		if (length($0) >= length(want) && substr($0, length($0) - length(want) + 1) == want) { print; exit }
	}'
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

# add_to_path DIR appends a PATH line for DIR to the shell's startup file, once. Prints the file it changed. DIR
# was checked to hold no character the shell would expand inside double quotes.
add_to_path() {
	ap_file=$(profile_file)
	case "$ap_file" in
	*.fish) ap_line="fish_add_path \"$1\"" ;;
	*) ap_line="export PATH=\"$1:\$PATH\"" ;;
	esac
	if [ -f "$ap_file" ] && grep -Fqx "$ap_line" "$ap_file"; then
		return 0
	fi
	mkdir -p "$(dirname "$ap_file")"
	printf '\n# Added by the Seelegent installer\n%s\n' "$ap_line" >>"$ap_file"
	printf '%s\n' "$ap_file"
}

# install_binary NEW EXE puts NEW in place of EXE; the replaced binary is kept as EXE.previous for
# `seelegent update --rollback`. Renames only, so a running seelegent is replaced safely, and an older kept binary
# is deleted only once the new one is in place.
install_binary() {
	ib_staged="$2.new"
	ib_kept=""
	rm -f "$ib_staged"
	cp "$1" "$ib_staged" || die "could not copy the new seelegent next to $2; nothing was changed"
	chmod 755 "$ib_staged"
	if [ -e "$2" ]; then
		if [ -e "$2.previous" ]; then
			ib_kept="$2.old-$$"
			mv -f "$2.previous" "$ib_kept" || {
				rm -f "$ib_staged"
				die "could not move the older $2.previous aside; nothing was changed"
			}
		fi
		if ! mv -f "$2" "$2.previous"; then
			[ -z "$ib_kept" ] || mv -f "$ib_kept" "$2.previous"
			rm -f "$ib_staged"
			die "could not move the installed $2 aside; nothing was changed"
		fi
		previous="$2.previous"
	fi
	if ! mv -f "$ib_staged" "$2"; then
		rm -f "$ib_staged"
		if [ -n "$previous" ]; then
			mv -f "$previous" "$2" || die "could not put the new seelegent in place, nor the old one back: rename $previous to $2 by hand"
			[ -z "$ib_kept" ] || mv -f "$ib_kept" "$2.previous" || warn "the older kept binary stays as $ib_kept"
			previous=""
		fi
		die "could not put the new seelegent in place; the old one is back"
	fi
	[ -z "$ib_kept" ] || rm -f "$ib_kept"
}

# install_companion NEW COMPANION installs the companion launcher seelegent-launch next to seelegent the way the
# binary is installed (the replaced one kept as COMPANION.previous, for `seelegent update --rollback`), or, for a
# release without one (NEW empty), moves the installed one to COMPANION.previous, so no launcher of another build
# stays next to the binary. The daemon uses the companion only when it is of its own build and runs launches as
# `seelegent __launch` otherwise, so a failure here is a warning, never a failed install.
install_companion() {
	ic_previous="$2.previous"
	ic_note="launches run as \`seelegent __launch\` (the same, with more memory per running task)"
	rm -f "$2".old-* "$2.new"
	if [ -z "$1" ]; then
		if [ -e "$2" ]; then
			mv -f "$2" "$ic_previous" || warn "could not move the companion launcher $2 aside"
		fi
		return 0
	fi
	# A kept companion with no installed one belongs to an older install, not to the previous binary.
	if [ ! -e "$2" ]; then
		rm -f "$ic_previous"
	fi
	if ! cp "$1" "$2.new" || ! chmod 755 "$2.new"; then
		rm -f "$2.new"
		warn "could not copy the companion launcher next to $2; $ic_note"
		return 0
	fi
	if [ -e "$2" ] && ! mv -f "$2" "$ic_previous"; then
		rm -f "$2.new"
		warn "could not move the companion launcher $2 aside; the old one stays and $ic_note"
		return 0
	fi
	if ! mv -f "$2.new" "$2"; then
		rm -f "$2.new"
		[ ! -e "$ic_previous" ] || mv -f "$ic_previous" "$2" || true
		warn "could not put the new companion launcher in place; $ic_note"
	fi
	return 0
}

# cleanup runs on every exit: it removes the download folder and, when the script stopped a daemon and then failed
# before the new binary was in place, starts that daemon again with the binary that is installed.
cleanup() {
	rm -rf "$tmp"
	if [ "$restart_on_exit" = 1 ]; then
		restart_on_exit=0
		warn "the install failed; starting the daemon again ..."
		"$exe" restart >/dev/null 2>&1 || warn "the daemon could not be started again; run: $exe restart"
	fi
}

main() {
	version=${SEELEGENT_VERSION:-}
	install_dir=${SEELEGENT_INSTALL_DIR:-}
	no_path=${SEELEGENT_NO_MODIFY_PATH:-0}
	no_setup=${SEELEGENT_NO_SETUP:-0}
	force=${SEELEGENT_FORCE:-0}
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
		--force)
			force=1
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

	install_dir=${install_dir:-$HOME/.local/bin}
	check_install_dir "$install_dir"
	data_dir=${SEELEGENT_HOME:-$HOME/.seelegent}

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
	exe=""
	restart_on_exit=0
	trap cleanup EXIT
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
	asset_url=$(printf '%s\n' "$urls" | url_ending "$asset")
	sums_url=$(printf '%s\n' "$urls" | url_ending checksums.txt)
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
	# The companion launcher seelegent-launch (releases from before it have none).
	new_companion=""
	for candidate in "$tmp/x/seelegent-launch" "$tmp"/x/*/seelegent-launch; do
		if [ -f "$candidate" ]; then
			new_companion=$candidate
			break
		fi
	done
	reported=$("$new_bin" version --json 2>/dev/null | sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' || true)
	if [ "$reported" != "$rel_tag" ]; then
		die "the downloaded seelegent does not run here or is not $rel_tag (it reported '$reported'); nothing was installed"
	fi
	# A release that knows fewer database migrations than the database has could not open it (migrations are never
	# undone). The downloaded binary checks the database itself, read-only.
	# Only a build from before the check (it answers --check-database with code "usage") is "unknown"; any other
	# failure (a database that cannot be read, say) refuses, --force or not, with the binary's own error.
	if [ -e "$data_dir/seelegent.db" ]; then
		schema_status=0
		schema_out=$("$new_bin" version --check-database --json 2>&1) || schema_status=$?
		if [ "$schema_status" -ne 0 ]; then
			schema_code=$(printf '%s\n' "$schema_out" | sed -n 's/^[[:space:]]*"code"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
			case "$schema_code" in
			schema_too_new)
				die "the database in $data_dir is newer than $rel_tag can open (migrations are never undone); install a newer release. Nothing was installed"
				;;
			usage)
				if [ "$force" = 1 ]; then
					warn "$rel_tag does not report which database schema it knows; installing it anyway (--force)"
				else
					die "$rel_tag does not report which database schema it knows, so it may not open the database in $data_dir; --force installs it anyway. Nothing was installed"
				fi
				;;
			*)
				schema_err=$(printf '%s\n' "$schema_out" | sed -n 's/^[[:space:]]*"error"[[:space:]]*:[[:space:]]*"\(.*\)",\{0,1\}[[:space:]]*$/\1/p' | head -n 1)
				[ -n "$schema_err" ] || schema_err=$(printf '%s' "$schema_out" | tr '\n' ' ')
				die "$rel_tag could not check the database in $data_dir (exit $schema_status): $schema_err. Nothing was installed"
				;;
			esac
		fi
	fi

	mkdir -p "$install_dir"
	# The trailing "." keeps a resolved name that ends in a line break intact (command substitution strips them).
	install_dir=$(cd "$install_dir" && pwd -P && echo .) || die "cannot enter the install folder $install_dir"
	install_dir=${install_dir%.}
	install_dir=${install_dir%"$NL"}
	# The resolved folder is what goes into the profile: a link may point at a name the first check never saw.
	check_install_dir "$install_dir"
	exe="$install_dir/seelegent"
	was_running=0
	previous=""
	if [ -e "$exe" ]; then
		rm -f "$exe".old-* "$exe.new"
		if stop_out=$("$exe" stop --json 2>/dev/null); then
			if printf '%s\n' "$stop_out" | grep -Eq '"stopped"[[:space:]]*:[[:space:]]*true'; then
				was_running=1
				restart_on_exit=1
				say "Stopped the running daemon."
			fi
		else
			warn "the running Seelegent daemon could not be stopped: $stop_out"
		fi
	fi
	say "Installing $exe ..."
	install_binary "$new_bin" "$exe"
	restart_on_exit=0
	if [ -n "$previous" ]; then
		say "The previous binary is kept as $previous (seelegent update --rollback)."
	fi
	install_companion "$new_companion" "$install_dir/seelegent-launch"

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
	old_data=0
	for marker in settings.json accounts.json roles.json memory; do
		[ -e "$data_dir/$marker" ] && old_data=1
	done
	if [ "$old_data" = 1 ] && [ ! -e "$data_dir/seelegent.db" ]; then
		say "Importing the old version's data from $data_dir (seelegent migrate) ..."
		"$exe" migrate || failed="$failed seelegent-migrate"
	elif [ "$old_data" = 1 ] && [ -e "$data_dir/migrate-errors.json" ]; then
		# migrate leaves this file when items failed and removes it on a run without errors.
		warn "the last seelegent migrate did not import everything (see $data_dir/migrate-errors.json). Fix what it reported, stop the daemon (seelegent stop) and run seelegent migrate again."
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
