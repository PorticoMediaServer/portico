#!/usr/bin/env bash
# Run Go test suites on the shared Linux test runner (ssh alias: portico-runner).
#
# Usage, from a portico-server or portico-internal checkout (the same script is in
# both; it finds the Go modules from the checkout's layout):
#   scripts/runner-test.sh                          # full preset: every Go module in this checkout
#   scripts/runner-test.sh ./internal/httpapi       # packages of the default module (server, or hosted)
#   scripts/runner-test.sh -run 'TestFoo' ./internal/identity
#   scripts/runner-test.sh --module apikit ./...    # another module (server, apikit; or hosted, dns, apikit)
#   scripts/runner-test.sh --lane be-data -json ./...
#   scripts/runner-test.sh --full -json             # full preset with extra flags for every module
#   scripts/runner-test.sh --tier media ./...       # a test tier: default, media or all (see below)
#   scripts/runner-test.sh --stop                   # stop this worktree's running job on the runner
#   scripts/runner-test.sh --stop <job>             # stop another job (see --jobs)
#   scripts/runner-test.sh --jobs                   # list running jobs
#
# Each go test runs in its own process group, recorded in ~/slots/jobs/<job>.pgid
# (<job> = <lane>-<branch>). Stop a run with --stop, never with pkill: the
# runner is shared and a pattern would match other lanes' tests.
#
# What it does:
#   1. Syncs this worktree's files (tracked plus untracked-but-not-ignored, so
#      uncommitted work is tested; .git, node_modules and ignored paths never go)
#      to ~/work/<lane>-<branch> on the runner, incrementally, deleting files
#      that no longer exist here.
#   2. Takes one of two slots (flock on ~/slots/1 or ~/slots/2), waiting while
#      both are busy, so parallel lanes don't overload the box.
#   3. Builds the playback test helper and runs `go test` with
#      PORTICO_PLAYBACK_TEST_HELPER and PORTICO_TEST_DATABASE_URL_FILE set (each
#      slot has its own PostgreSQL test database), -count=1, -p 3 and -timeout 30m
#      unless the arguments already say otherwise; PORTICO_FFMPEG/PORTICO_FFPROBE
#      point at the production FFmpeg bundle in /opt/portico-ffmpeg when present.
#      Output streams back; the exit code is go test's (the full preset fails if
#      any module fails).
#
# Tiers (PORTICO_TEST_TIER, see internal/testtier): `default` is the fast suite
# every lane runs (target under 5 minutes on the G12); it skips, with a message,
# the real-FFmpeg, Live TV, Library Channel and real-time tests. `media` and
# `all` run those as well (the same thing today: Go cannot run "only" a tier, so
# both mean the default tests plus the media tests; the integrator runs `all`
# once per merge batch). Without --tier a
# run is `default`, except --full, which is `all` unless --tier says otherwise.
# The performance tier stays PORTICO_PERFORMANCE_TIER, set by the caller.
#
# Hosts: by default the job goes to the first runner with a free slot, in the
# order of PORTICO_RUNNER_HOSTS (default "portico-runner-g12 portico-runner":
# the faster Netcup G12 first, then Contabo), and waits on the first one when
# every slot is busy. PORTICO_RUNNER_HOST pins one host. --jobs and --stop act on
# every host. The host, user and key come from ~/.ssh/config. A host may set its
# default go test parallelism in ~/.runner-p (2 on the 4-core G12) and its slot
# count in ~/.runner-slots (default 2; Contabo runs 1, it also hosts the e2e server).
set -euo pipefail

hosts="${PORTICO_RUNNER_HOST:-${PORTICO_RUNNER_HOSTS:-portico-runner-g12 portico-runner}}"
lane=""
module=""
tier=""
stop=""
list_jobs=0
args=()
while [ $# -gt 0 ]; do
	case "$1" in
	--lane) lane="${2:?--lane needs a name}"; shift 2 ;;
	--module) module="${2:?--module needs a module name}"; shift 2 ;;
	--full) module="full"; shift ;;
	--tier) tier="${2:?--tier needs default, media or all}"; shift 2 ;;
	--stop) if [ $# -gt 1 ] && [ "${2#-}" = "$2" ]; then stop="$2"; shift 2; else stop="."; shift; fi ;;
	--jobs) list_jobs=1; shift ;;
	--help|-h) sed -n '2,35p' "$0"; exit 0 ;;
	*) args+=("$1"); shift ;;
	esac
done
if [ -z "$tier" ]; then
	if [ "$module" = full ]; then tier=all; else tier=default; fi
fi
case "$tier" in
default | media | all) ;;
*) echo "runner-test: unknown tier $tier (default, media, all)" >&2; exit 2 ;;
esac

# Keepalives: a job waiting silently for a slot must not be dropped on the path
# and leave the local side hanging forever (seen on both hosts, 24 Sep).
ssh_keepalive="-o ServerAliveInterval=15 -o ServerAliveCountMax=4"

root="$(git rev-parse --show-toplevel)"
# The Go modules this checkout holds, and where they live.
if [ -f "$root/server/go.mod" ]; then
	modules="server apikit"; prefix=""
elif [ -f "$root/hosted-services/hosted/go.mod" ]; then
	modules="hosted dns apikit"; prefix="hosted-services/"
else
	echo "runner-test: no Go modules here (expected server/ or hosted-services/)" >&2; exit 2
fi
case " $modules full " in
*" ${module:-full} "*) ;;
*) echo "runner-test: unknown module $module ($modules)" >&2; exit 2 ;;
esac
branch="$(git -C "$root" rev-parse --abbrev-ref HEAD)"
[ "$branch" = "HEAD" ] && branch="detached-$(git -C "$root" rev-parse --short HEAD)"
[ -n "$lane" ] || lane="$(basename "$root")"
slug() { printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '-' | sed 's/--*/-/g; s/^-//; s/-$//'; }
dir="$(slug "$lane")-$(slug "$branch")"
remote="work/$dir"

if [ "$list_jobs" = 1 ]; then
	for host in $hosts; do
		ssh $ssh_keepalive -o BatchMode=yes -o ConnectTimeout=10 "$host" 'cd ~/slots/jobs 2>/dev/null || exit 0; for f in *.pgid; do [ -e "$f" ] || continue; pg=$(cat "$f"); if kill -0 -- "-$pg" 2>/dev/null; then echo "$(hostname): ${f%.pgid} (process group $pg)"; else rm -f "./$f"; fi; done' || echo "runner-test: $host unreachable" >&2
	done
	exit 0
fi
if [ -n "$stop" ]; then
	[ "$stop" = "." ] && stop="$dir"
	stop="$(slug "$stop")"
	found=1
	for host in $hosts; do
		ssh $ssh_keepalive -o BatchMode=yes -o ConnectTimeout=10 "$host" "f=~/slots/jobs/$stop.pgid; [ -s \"\$f\" ] || exit 1; pg=\$(cat \"\$f\"); kill -TERM -- -\$pg 2>/dev/null && echo \"runner-test: stopped $stop on \$(hostname) (process group \$pg)\" || echo 'runner-test: job $stop had already ended'; rm -f \"\$f\"" && found=0
	done
	[ "$found" = 0 ] || echo "runner-test: no running job $stop" >&2
	exit $found
fi

# 0. Pick a host: the first with a free slot, else the first reachable one (the
# job then waits there for a slot). A probe is advisory; the flock below decides.
host=""
for h in $hosts; do
	free="$(ssh $ssh_keepalive -o BatchMode=yes -o ConnectTimeout=10 "$h" 'mkdir -p ~/slots; for n in $(seq 1 "$(cat ~/.runner-slots 2>/dev/null || echo 2)"); do exec 9>"$HOME/slots/$n"; if flock -n 9; then echo yes; exit 0; fi; exec 9>&-; done; echo no' 2>/dev/null || echo down)"
	case "$free" in
	yes) host="$h"; break ;;
	no) [ -n "$host" ] || host="$h" ;;
	esac
done
[ -n "$host" ] || { echo "runner-test: no runner reachable ($hosts)" >&2; exit 2; }
echo "runner-test: using $host" >&2

# 1. Sync. An include list of every file and its parent directories, then
# exclude everything else, lets --delete remove what disappeared locally.
list="$(mktemp -t portico-runner.XXXXXX)"
trap 'rm -f "${list:?}"' EXIT
# One awk pass (no per-file processes): each file, and each parent directory.
# A tracked file deleted locally is simply absent on the runner after --delete.
(cd "$root" && git -c core.quotePath=false ls-files --cached --others --exclude-standard) |
	awk 'NF == 0 { next }
		{
			n = split($0, part, "/"); d = ""
			for (i = 1; i < n; i++) { d = d "/" part[i]; print d "/" }
			print "/" $0
		}' | sort -u >"$list"
echo "runner-test: syncing $(grep -vc '/$' "$list") files to $host:~/$remote" >&2
ssh $ssh_keepalive -o BatchMode=yes "$host" "mkdir -p ~/$remote"
rsync -a --delete --delete-excluded --include-from="$list" --exclude='*' \
	-e "ssh $ssh_keepalive -o BatchMode=yes" "$root/" "$host:$remote/"

# 2–3. Run on the runner.
quoted=""
for a in ${args[@]+"${args[@]}"}; do quoted+=" $(printf '%q' "$a")"; done
echo "runner-test: tier $tier" >&2
ssh $ssh_keepalive -o BatchMode=yes "$host" "bash -l -s -- $(printf '%q' "$remote") $(printf '%q' "${module}") $(printf '%q' "$tier") $(printf '%q' "$modules") $(printf '%q' "$prefix")$quoted" <<'REMOTE'
set -uo pipefail
remote="$1"; module="$2"; tier="$3"; modules="$4"; prefix="$5"; shift 5
export PORTICO_TEST_TIER="$tier"
work="$HOME/$remote"
# The one checked capability list, from the synced worktree: the helper build and
# every go test below carry the same tags as an official build.
PORTICO_GO_TAGS="$(python3 "$work/scripts/release-build-tags.py" --capability)" || {
	echo "runner-test: could not read the official capability tags" >&2; exit 2; }
[ -n "$PORTICO_GO_TAGS" ] || { echo "runner-test: empty official capability tag list" >&2; exit 2; }
export PORTICO_GO_TAGS
mkdir -p "$HOME/slots"
slot=""
waited=0
while [ -z "$slot" ]; do
	for n in $(seq 1 "$(cat "$HOME/.runner-slots" 2>/dev/null || echo 2)"); do
		exec {fd}>"$HOME/slots/$n"
		if flock -n "$fd"; then slot=$n; break; fi
		exec {fd}>&-
	done
	if [ -z "$slot" ]; then
		[ "$waited" = 0 ] && echo "runner-test: every slot on $(hostname) busy; waiting" >&2
		waited=$((waited + 5))
		# A heartbeat every 30 s, so a waiting job is visibly alive.
		[ $((waited % 30)) = 0 ] && echo "runner-test: still waiting for a slot on $(hostname) (${waited}s)" >&2
		sleep 5
	fi
done
echo "runner-test: slot $slot on $(hostname), load $(cut -d' ' -f1-3 /proc/loadavg)" >&2
jobs_dir="$HOME/slots/jobs"
mkdir -p "$jobs_dir"
job="$(basename "$remote")"
pgid_file="$jobs_dir/$job.pgid"
current=""
stop_current() { [ -n "$current" ] && kill -TERM -- "-$current" 2>/dev/null; rm -f "$pgid_file"; }
trap 'stop_current; exit 143' TERM HUP INT

# One PostgreSQL test database per slot, so two lanes never share one.
db="portico_hosted_test_s$slot"
psql -Atq postgres -h /var/run/postgresql -c "SELECT 1 FROM pg_database WHERE datname='$db'" | grep -q 1 ||
	createdb -h /var/run/postgresql "$db"
url="$HOME/slots/hosted-test-$slot.url"
printf 'postgres:///%s?host=/var/run/postgresql\n' "$db" >"$url"

run="$work/.runner"
rm -rf "${run:?}/tmp"
mkdir -p "$run/tmp"
export TMPDIR="$run/tmp"
export PORTICO_TEST_DATABASE_URL_FILE="$url"
export PORTICO_PLAYBACK_TEST_HELPER="$run/portico-server"
# The production FFmpeg bundle (what the demo ships) when the runner has it, so
# tests exercise the same toolchain as production rather than Ubuntu's ffmpeg.
if [ -x /opt/portico-ffmpeg/bin/ffmpeg ]; then
	export PORTICO_FFMPEG=/opt/portico-ffmpeg/bin/ffmpeg PORTICO_FFPROBE=/opt/portico-ffmpeg/bin/ffprobe
	export PATH="/opt/portico-ffmpeg/bin:$PATH"
fi
if [ -d "$work/server" ]; then
	(cd "$work/server" && go build -tags "$PORTICO_GO_TAGS" -o "$PORTICO_PLAYBACK_TEST_HELPER" ./cmd/server) || { echo "runner-test: helper build failed" >&2; exit 1; }
fi

defaults() {
	local have_p=0 have_count=0 have_timeout=0 a
	for a in "$@"; do
		case "$a" in -p|-p=*) have_p=1 ;; -count|-count=*) have_count=1 ;; -timeout|-timeout=*) have_timeout=1 ;; esac
	done
	[ "$have_p" = 1 ] || printf '%s\n' "-p" "$(cat "$HOME/.runner-p" 2>/dev/null || echo 3)"
	[ "$have_count" = 1 ] || printf '%s\n' "-count=1"
	# Stopgap until persistence's migration-order test stops replaying the chain.
	[ "$have_timeout" = 1 ] || { if [ "$module" = full ]; then printf '%s\n' "-timeout" "60m"; else printf '%s\n' "-timeout" "30m"; fi; }
}
gotest() {
	local mod="$1"; shift
	local extra=()
	mapfile -t extra < <(defaults "$@")
	extra=(-tags "$PORTICO_GO_TAGS" "${extra[@]}")
	echo "runner-test: [$mod] go test ${extra[*]} $*" >&2
	# Its own process group (setsid), recorded so --stop can end exactly this job.
	setsid bash -c 'cd "$1" && shift && exec go test "$@"' _ "$work/$prefix$mod" "${extra[@]}" "$@" &
	current=$!
	echo "$current" >"$pgid_file"
	local rc=0
	wait "$current" || rc=$?
	current=""
	rm -f "$pgid_file"
	return "$rc"
}
status=0
if [ "$module" = full ] || { [ -z "$module" ] && [ $# -eq 0 ]; }; then
	started=$(date +%s)
	for mod in $modules; do
		gotest "$mod" "$@" ./... || status=1
	done
	echo "runner-test: full preset finished in $(( $(date +%s) - started ))s, status $status" >&2
else
	gotest "${module:-${modules%% *}}" "$@" || status=$?
fi
rm -rf "${run:?}/tmp"
exit "$status"
REMOTE
