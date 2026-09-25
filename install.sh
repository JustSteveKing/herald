#!/usr/bin/env sh

# herald installer
#
# From a checkout:
#   ./install.sh
#
# From anywhere, once there is a release:
#   curl -fsSL https://raw.githubusercontent.com/JustSteveKing/herald/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/JustSteveKing/herald/main/install.sh | sh -s -- --version v0.1.0
#
# There are two ways in and the script picks whichever can work. If it is
# sitting in a checkout and Go is installed, it builds from source, which needs
# no release and no network. Otherwise it downloads a release built by
# GoReleaser, checks it against that release's checksums.txt, and installs the
# binary onto your PATH.
#
# The download needs no account. If the gh CLI is signed in it is used anyway,
# which is what makes this work unchanged against a private fork, where an
# unauthenticated request for a release asset comes back 404 rather than 403.

set -eu
printf '\n'

BOLD="$(tput bold 2>/dev/null || printf '')"
GREY="$(tput setaf 0 2>/dev/null || printf '')"
UNDERLINE="$(tput smul 2>/dev/null || printf '')"
RED="$(tput setaf 1 2>/dev/null || printf '')"
GREEN="$(tput setaf 2 2>/dev/null || printf '')"
YELLOW="$(tput setaf 3 2>/dev/null || printf '')"
BLUE="$(tput setaf 4 2>/dev/null || printf '')"
MAGENTA="$(tput setaf 5 2>/dev/null || printf '')"
NO_COLOR="$(tput sgr0 2>/dev/null || printf '')"

PROJECT_NAME="herald"
BIN_NAME="herald"
GITHUB_REPO="JustSteveKing/herald"

# Mirrors goos/goarch in .goreleaser.yaml.
SUPPORTED_TARGETS="darwin/amd64 darwin/arm64 \
                   linux/amd64 linux/arm64 \
                   windows/amd64 windows/arm64"

info() { printf '%s\n' "${BOLD}${GREY}>${NO_COLOR} $*"; }
warn() { printf '%s\n' "${YELLOW}! $*${NO_COLOR}"; }
error() { printf '%s\n' "${RED}x $*${NO_COLOR}" >&2; }
completed() { printf '%s\n' "${GREEN}+${NO_COLOR} $*"; }

has() { command -v "$1" 1>/dev/null 2>&1; }

curl_is_snap() {
	curl_path="$(command -v curl)"
	case "$curl_path" in
	/snap/*) return 0 ;;
	*) return 1 ;;
	esac
}

verify_shell_is_posix_or_exit() {
	if [ -n "${ZSH_VERSION+x}" ]; then
		error "Running this with \`zsh\` is known to cause errors. Use \`sh\`."
		exit 1
	elif [ -n "${BASH_VERSION+x}" ] && [ -z "${POSIXLY_CORRECT+x}" ]; then
		error "Running this with non-POSIX \`bash\` may cause errors. Use \`sh\`."
		exit 1
	fi
}

get_tmpfile() {
	suffix="$1"
	if has mktemp; then
		printf "%s.%s" "$(mktemp)" "${suffix}"
	else
		printf "/tmp/%s.%s" "${PROJECT_NAME}" "${suffix}"
	fi
}

test_writable() {
	path="${1:-}/.herald-write-test"
	if touch "${path}" 2>/dev/null; then
		rm -f "${path}"
		return 0
	fi
	return 1
}

on_path() {
	dir="${1%/}"
	IFS=:
	for entry in $PATH; do
		if [ "${entry%/}" = "${dir}" ]; then
			unset IFS
			return 0
		fi
	done
	unset IFS
	return 1
}

# checkout_root prints the directory of a herald checkout this script is
# sitting in, and nothing at all when piped from curl.
checkout_root() {
	case "${0-}" in
	sh | -sh | bash | dash | *"/sh" | *"/bash" | *"/dash" | "") return 1 ;;
	esac

	dir="$(CDPATH='' cd -- "$(dirname -- "$0")" 2>/dev/null && pwd)" || return 1

	if [ -f "${dir}/go.mod" ] && grep -q "^module github.com/JustSteveKing/herald$" "${dir}/go.mod" 2>/dev/null; then
		printf '%s' "${dir}"
		return 0
	fi
	return 1
}

download() {
	file="$1"
	url="$2"

	if has curl && curl_is_snap; then
		warn "curl installed through snap cannot download ${PROJECT_NAME}."
	fi

	if has curl && ! curl_is_snap; then
		cmd="curl --fail --silent --location --output $file $url"
	elif has wget; then
		cmd="wget --quiet --output-document=$file $url"
	else
		error "No HTTP download program (curl, wget) found."
		return 1
	fi

	$cmd && return 0 || rc=$?
	error "Command failed (exit code $rc): ${BLUE}${cmd}${NO_COLOR}"
	return "$rc"
}

fetch_stdout() {
	url="$1"
	if has curl && ! curl_is_snap; then
		curl --fail --silent --location "$url"
	elif has wget; then
		wget --quiet --output-document=- "$url"
	else
		return 1
	fi
}

# gh_ready is true when the gh CLI is present and signed in. Using it when it
# is there costs nothing and is the only way to reach a release on a private
# fork without hand rolling token handling.
gh_ready() {
	has gh && gh auth status >/dev/null 2>&1
}

resolve_latest_tag() {
	if gh_ready; then
		gh release view --repo "${GITHUB_REPO}" --json tagName --jq .tagName 2>/dev/null && return 0
	fi

	body="$(fetch_stdout "https://api.github.com/repos/${GITHUB_REPO}/releases/latest")" || return 1
	printf '%s' "$body" |
		grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' |
		head -n 1 |
		sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'
}

compute_sha256() {
	file="$1"
	if has sha256sum; then
		sha256sum "$file" | awk '{ print $1 }'
	elif has shasum; then
		shasum -a 256 "$file" | awk '{ print $1 }'
	elif has openssl; then
		openssl dgst -sha256 "$file" | awk '{ print $NF }'
	else
		return 1
	fi
}

verify_checksum() {
	archive_path="$1"
	archive_name="$2"

	if [ -n "${NO_VERIFY-}" ]; then
		warn "Skipping checksum verification (--no-verify)."
		return 0
	fi

	actual="$(compute_sha256 "$archive_path")" || {
		warn "No sha256 tool found. Skipping checksum verification."
		return 0
	}

	checksums=""
	if [ -n "${USED_GH-}" ]; then
		checksums="$(gh release download "${TAG}" --repo "${GITHUB_REPO}" --pattern checksums.txt --output - 2>/dev/null)" || checksums=""
	fi
	if [ -z "$checksums" ]; then
		checksums="$(fetch_stdout "${BASE_URL}/download/${TAG}/checksums.txt")" || {
			error "Could not download checksums.txt from the release."
			error "Re-run with --no-verify to bypass (not recommended)."
			return 1
		}
	fi

	expected="$(printf '%s\n' "$checksums" | grep " ${archive_name}\$" | head -n 1 | awk '{ print $1 }')"

	if [ -z "$expected" ]; then
		error "No checksum entry for ${archive_name} in checksums.txt."
		return 1
	fi

	if [ "$expected" != "$actual" ]; then
		error "Checksum mismatch for ${archive_name}."
		error "  expected: ${expected}"
		error "  actual:   ${actual}"
		error "Refusing to install a corrupted or tampered download."
		return 1
	fi

	completed "Checksum verified (sha256)."
}

# Extract only the binary. The archive also carries README.md and LICENSE, and
# unpacking the lot would leave both of those in your bin directory.
unpack() {
	archive=$1
	bin_dir=$2
	sudo=${3-}

	case "$archive" in
	*.tar.gz)
		flags=$(test -n "${VERBOSE-}" && echo "-xzvof" || echo "-xzof")
		${sudo} tar "${flags}" "${archive}" -C "${bin_dir}" "${BIN_NAME}"
		return 0
		;;
	*.zip)
		flags=$(test -z "${VERBOSE-}" && echo "-qqo" || echo "-o")
		UNZIP="${flags}" ${sudo} unzip "${archive}" "${BIN_NAME}.exe" -d "${bin_dir}"
		return 0
		;;
	esac

	error "Unknown package extension."
	return 1
}

usage() {
	printf "%s\n" \
		"install.sh [option]" \
		"" \
		"Install ${BIN_NAME}, building from source in a checkout or downloading a" \
		"release otherwise."

	printf "\n%s\n" "Options"
	printf "\t%s\n\t\t%s\n\n" \
		"-V, --verbose" "Enable verbose output" \
		"-f, -y, --force, --yes" "Skip the confirmation prompt" \
		"--no-verify" "Skip sha256 checksum verification (not recommended)" \
		"--source" "Build from source even if a release could be downloaded" \
		"--download" "Download a release even when sitting in a checkout" \
		"-p, --platform" "Override the detected OS [default: ${PLATFORM}]" \
		"-b, --bin-dir" "Override the install directory [default: ${BIN_DIR}]" \
		"-a, --arch" "Override the detected architecture [default: ${ARCH}]" \
		"-B, --base-url" "Override the release download base URL" \
		"-v, --version" "Install a specific version (e.g. v0.1.0) [default: ${VERSION}]" \
		"-h, --help" "Display this help message"
}

elevate_priv() {
	if ! has sudo; then
		error 'Could not find "sudo", needed to write to the install directory.'
		info "Install somewhere you own instead: ${BOLD}--bin-dir \$HOME/.local/bin${NO_COLOR}"
		exit 1
	fi
	if ! sudo -v; then
		error "Superuser not granted, aborting installation"
		exit 1
	fi
}

sudo_for_bin_dir() {
	if test_writable "${BIN_DIR}"; then
		printf ''
	else
		warn "Escalated permissions are required to install to ${BIN_DIR}" >&2
		elevate_priv
		printf 'sudo'
	fi
}

build_from_source() {
	root="$1"

	if ! has go; then
		error "Building from source needs Go, and it is not on this machine."
		info "Install Go, or use ${BOLD}--download${NO_COLOR} to fetch a release instead."
		exit 1
	fi

	info "Building from source in ${root}"
	info "Go: $(go version | awk '{print $3}')"

	# A source build has no release tag to stamp, so it says where it came
	# from. `herald --version` printing a commit rather than a version is how
	# you know this binary was built here and not downloaded.
	described="dev"
	if has git && git -C "${root}" rev-parse --git-dir >/dev/null 2>&1; then
		described="$(git -C "${root}" describe --tags --always --dirty 2>/dev/null || printf 'dev')"
	fi

	tmp="$(get_tmpfile bin)"
	( cd "${root}" && go build -ldflags "-s -w -X main.version=${described}" -o "${tmp}" ./cmd/herald )

	sudo="$(sudo_for_bin_dir)"
	${sudo} install -m 0755 "${tmp}" "${BIN_DIR%/}/${BIN_NAME}"
	rm -f "${tmp}"
}

install_release() {
	ext="$1"
	sudo="$(sudo_for_bin_dir)"

	info "Installing ${BIN_NAME} ${TAG}"
	archive=$(get_tmpfile "$ext")

	if gh_ready; then
		USED_GH=1
		info "Downloading with the gh CLI, which works against a private fork too."
		gh release download "${TAG}" --repo "${GITHUB_REPO}" --pattern "${ARCHIVE}" --output "${archive}" --clobber
	else
		download "${archive}" "${URL}"
	fi

	verify_checksum "${archive}" "${ARCHIVE}"
	unpack "${archive}" "${BIN_DIR}" "${sudo}"
	rm -f "${archive}"
}

detect_platform() {
	platform="$(uname -s | tr '[:upper:]' '[:lower:]')"
	case "${platform}" in
	msys_nt* | cygwin_nt* | mingw*) platform="windows" ;;
	esac
	printf '%s' "${platform}"
}

detect_arch() {
	arch="$(uname -m | tr '[:upper:]' '[:lower:]')"
	case "${arch}" in
	x86_64 | amd64) arch="amd64" ;;
	aarch64 | arm64) arch="arm64" ;;
	i386 | i686) arch="386" ;;
	armv* | arm) arch="arm" ;;
	esac
	printf '%s' "${arch}"
}

confirm() {
	if [ -z "${FORCE-}" ]; then
		printf "%s " "${MAGENTA}?${NO_COLOR} $* ${BOLD}[y/N]${NO_COLOR}"
		set +e
		read -r yn </dev/tty
		rc=$?
		set -e
		if [ $rc -ne 0 ]; then
			error "Error reading from prompt (re-run with --yes)"
			exit 1
		fi
		if [ "$yn" != "y" ] && [ "$yn" != "yes" ]; then
			error 'Aborting (please answer "yes" to continue)'
			exit 1
		fi
	fi
}

check_bin_dir() {
	if [ ! -d "$BIN_DIR" ]; then
		info "Creating ${BIN_DIR}"
		mkdir -p "$BIN_DIR" 2>/dev/null || {
			error "Could not create ${BIN_DIR}"
			exit 1
		}
	fi

	if ! on_path "${BIN_DIR}"; then
		warn "${BIN_DIR} is not on your \$PATH, so the shell will not find ${BIN_NAME}."
	fi
}

is_build_available() {
	target="$1/$2"
	for t in ${SUPPORTED_TARGETS}; do
		[ "${t}" = "${target}" ] && return 0
	done

	error "${2} builds for ${1} are not available for ${BIN_NAME}"
	info "Build from source instead: ${BOLD}--source${NO_COLOR}"
	exit 1
}

# defaults
[ -z "${PLATFORM-}" ] && PLATFORM="$(detect_platform)"
[ -z "${ARCH-}" ] && ARCH="$(detect_arch)"
[ -z "${BASE_URL-}" ] && BASE_URL="https://github.com/${GITHUB_REPO}/releases"
[ -z "${VERSION-}" ] && VERSION="latest"

# Prefer a directory you own. Needing sudo to install a webhook sender is a
# poor trade, and ~/.local/bin is on the PATH of most modern setups.
if [ -z "${BIN_DIR-}" ]; then
	if on_path "${HOME}/.local/bin" || [ -d "${HOME}/.local/bin" ]; then
		BIN_DIR="${HOME}/.local/bin"
	else
		BIN_DIR=/usr/local/bin
	fi
fi

verify_shell_is_posix_or_exit

while [ "$#" -gt 0 ]; do
	case "$1" in
	-p | --platform) PLATFORM="$2" && shift 2 ;;
	-b | --bin-dir) BIN_DIR="$2" && shift 2 ;;
	-a | --arch) ARCH="$2" && shift 2 ;;
	-B | --base-url) BASE_URL="$2" && shift 2 ;;
	-v | --version) VERSION="$2" && shift 2 ;;
	-V | --verbose) VERBOSE=1 && shift 1 ;;
	-f | -y | --force | --yes) FORCE=1 && shift 1 ;;
	--no-verify) NO_VERIFY=1 && shift 1 ;;
	--source) MODE=source && shift 1 ;;
	--download) MODE=download && shift 1 ;;
	-h | --help) usage && exit ;;
	-p=* | --platform=*) PLATFORM="${1#*=}" && shift 1 ;;
	-b=* | --bin-dir=*) BIN_DIR="${1#*=}" && shift 1 ;;
	-a=* | --arch=*) ARCH="${1#*=}" && shift 1 ;;
	-B=* | --base-url=*) BASE_URL="${1#*=}" && shift 1 ;;
	-v=* | --version=*) VERSION="${1#*=}" && shift 1 ;;
	*)
		error "Unknown option: $1"
		usage
		exit 1
		;;
	esac
done

ROOT="$(checkout_root || true)"

# Pick a route unless one was asked for. A checkout is preferred because it
# installs what is in front of you, which is almost always what you meant when
# you ran a script out of a working copy.
if [ -z "${MODE-}" ]; then
	if [ -n "${ROOT}" ] && has go; then
		MODE=source
	else
		MODE=download
	fi
fi

printf "  %s\n" "${UNDERLINE}Configuration${NO_COLOR}"
info "${BOLD}Bin directory${NO_COLOR}: ${GREEN}${BIN_DIR}${NO_COLOR}"
info "${BOLD}Platform${NO_COLOR}:      ${GREEN}${PLATFORM}/${ARCH}${NO_COLOR}"

if [ "${MODE}" = "source" ]; then
	if [ -z "${ROOT}" ]; then
		error "--source needs to be run from a herald checkout, and this is not one."
		exit 1
	fi
	info "${BOLD}Source${NO_COLOR}:        ${GREEN}${ROOT}${NO_COLOR}"
	printf '\n'
	confirm "Build and install ${BIN_NAME} to ${BOLD}${GREEN}${BIN_DIR}${NO_COLOR}?"
	check_bin_dir
	build_from_source "${ROOT}"
else
	is_build_available "${PLATFORM}" "${ARCH}"

	if [ "${VERSION}" = "latest" ]; then
		info "Resolving the latest release tag..."
		TAG="$(resolve_latest_tag)" || TAG=""
		if [ -z "${TAG}" ]; then
			error "No release found for ${GITHUB_REPO}."
			if [ -n "${ROOT}" ]; then
				info "There is a checkout here. Build from it with ${BOLD}--source${NO_COLOR}."
			elif ! gh_ready; then
				info "If this is a private fork, sign in with ${BOLD}gh auth login${NO_COLOR} and try again."
			fi
			exit 1
		fi
	else
		TAG="${VERSION}"
	fi

	VER_NUM="${TAG#v}"
	info "${BOLD}Version${NO_COLOR}:       ${GREEN}${TAG}${NO_COLOR}"

	EXT=tar.gz
	[ "${PLATFORM}" = "windows" ] && EXT=zip

	# GoReleaser archive template: {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
	ARCHIVE="${PROJECT_NAME}_${VER_NUM}_${PLATFORM}_${ARCH}.${EXT}"
	URL="${BASE_URL}/download/${TAG}/${ARCHIVE}"

	printf '\n'
	confirm "Install ${BIN_NAME} ${GREEN}${TAG}${NO_COLOR} to ${BOLD}${GREEN}${BIN_DIR}${NO_COLOR}?"
	check_bin_dir
	install_release "${EXT}"
fi

# Run what was installed. A binary that is present but cannot start, because it
# is the wrong architecture or was truncated, should be caught here and not on
# first use.
installed="${BIN_DIR%/}/${BIN_NAME}"
[ "${PLATFORM}" = "windows" ] && installed="${installed}.exe"

if reported="$("${installed}" --version 2>/dev/null)"; then
	completed "${reported} installed to ${BIN_DIR}"
else
	warn "${BIN_NAME} was installed to ${BIN_DIR}, but running it failed."
	warn "Try: ${installed} --version"
	exit 1
fi

printf '\n'
info "Run ${BOLD}${BIN_NAME}${NO_COLOR} to browse providers, or"
info "${BOLD}${BIN_NAME} send stripe payment_intent.succeeded --to http://localhost:8000/stripe${NO_COLOR}"
info "${BOLD}${BIN_NAME} sync${NO_COLOR} pulls the current provider packs."
printf '\n'
