#!/usr/bin/env bash
# Cut a release: verify the tree, run the checks, create an annotated tag.
#
#   scripts/release.sh v0.1.0             # check and tag locally
#   scripts/release.sh --push v0.1.0      # ... and push main and the tag
#   scripts/release.sh --dry-run v0.1.0   # run every check, change nothing
#
# The version of the binary (--version, matter_exporter_build_info) comes from
# the tag, through the build information recorded by the Go toolchain; nothing
# has to be edited in the source.
set -euo pipefail

readonly BRANCH=main
readonly REMOTE=origin

usage() {
	sed -n '2,10s/^# \{0,1\}//p' "$0" >&2
	exit 2
}

die() {
	echo "release: $*" >&2
	exit 1
}

step() {
	echo "==> $*"
}

push=false
dry_run=false
version=
for arg in "$@"; do
	case "$arg" in
		--push) push=true ;;
		--dry-run) dry_run=true ;;
		-h | --help) usage ;;
		-*) die "unknown option: $arg" ;;
		*)
			[[ -z $version ]] || die "more than one version given"
			version=$arg
			;;
	esac
done
[[ -n $version ]] || usage

# Semantic version with a leading "v", as Go modules require.
[[ $version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] ||
	die "'$version' is not a version like v1.2.3 or v1.2.3-rc.1"

cd "$(git rev-parse --show-toplevel)"

step "Checking the working tree"
current=$(git symbolic-ref --quiet --short HEAD || true)
[[ $current == "$BRANCH" ]] || die "on '${current:-detached HEAD}', releases are cut from $BRANCH"
[[ -z $(git status --porcelain) ]] || die "the working tree has uncommitted or untracked files"

step "Fetching $REMOTE"
git fetch --quiet --tags "$REMOTE"
if git rev-parse --verify --quiet "refs/remotes/$REMOTE/$BRANCH" >/dev/null; then
	git merge-base --is-ancestor "$REMOTE/$BRANCH" HEAD ||
		die "$BRANCH does not contain $REMOTE/$BRANCH: pull or rebase first"
	unpushed=$(git rev-list --count "$REMOTE/$BRANCH..HEAD")
else
	unpushed=$(git rev-list --count HEAD)
fi

git rev-parse --verify --quiet "refs/tags/$version" >/dev/null && die "tag $version already exists"

# The new version must be the highest: a lower tag is almost always a typo.
previous=$(git -c versionsort.suffix=- tag --list 'v*' --sort=-version:refname | head -n 1)
if [[ -n $previous ]]; then
	highest=$(printf '%s\n%s\n' "$previous" "$version" | sort --version-sort | tail -n 1)
	# sort -V ranks v1.0.0-rc.1 above v1.0.0; a final release after its own
	# pre-release is fine.
	[[ $highest == "$version" || $previous == "$version"-* ]] ||
		die "$version is not newer than the latest tag $previous"
fi

step "Running checks"
unformatted=$(gofmt -l .)
[[ -z $unformatted ]] || die "gofmt needed on:"$'\n'"$unformatted"
go mod tidy -diff >/dev/null || die "go.mod or go.sum are not tidy: run 'go mod tidy'"
go vet ./...
go test -race -count=1 ./...
go build -o /dev/null ./cmd/matter_exporter

# Tag message: the commits since the previous release.
if [[ -n $previous ]]; then
	range="$previous..HEAD"
else
	range=HEAD
fi
message=$(
	echo "matter_exporter $version"
	echo
	git log --no-merges --reverse --format='* %s' "$range"
)

echo
echo "$message"
echo
echo "Commit: $(git log -1 --format='%h %s')"
if ((unpushed > 0)); then
	echo "Note:   $unpushed commit(s) on $BRANCH are not on $REMOTE yet."
fi
echo

if $dry_run; then
	step "Dry run: all checks passed, nothing was tagged"
	exit 0
fi

step "Tagging $version"
# Annotated tag; it is signed too if tag.gpgSign is set in the git config.
git tag --annotate "$version" --message "$message"

# The toolchain must now stamp exactly this version into the binary.
bin=$(mktemp)
trap 'rm -f "$bin"' EXIT
go build -o "$bin" ./cmd/matter_exporter
stamped=$("$bin" --version 2>&1)
if [[ $stamped != *"version $version "* ]]; then
	echo "release: warning: the binary reports an unexpected version:" >&2
	echo "  $stamped" >&2
fi

if $push; then
	step "Pushing $BRANCH and $version to $REMOTE"
	git push --atomic "$REMOTE" "$BRANCH" "refs/tags/$version"
	echo "Released $version."
else
	echo "Tagged $version locally. To publish it:"
	echo "  git push --atomic $REMOTE $BRANCH refs/tags/$version"
	echo "To undo:"
	echo "  git tag --delete $version"
fi
