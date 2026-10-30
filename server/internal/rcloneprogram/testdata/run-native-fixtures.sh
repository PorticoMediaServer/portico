#!/usr/bin/env bash
set -euo pipefail

# Run from the server module inside an admitted, disposable Linux runner job.
# Dependencies stay in owned scratch or the job-labelled Docker fixture.
test "$(uname -s)" = Linux
test "$(uname -m)" = x86_64
test -f ../third_party/rclone/bundle.lock.json
test -n "${TMPDIR:-}"
test "$(realpath "$TMPDIR")" = "$TMPDIR"
case "$TMPDIR" in /tmp|/var/tmp|/|"$HOME") exit 2 ;; esac
for required in python3; do
    if ! command -v "$required" >/dev/null; then
        echo "Native fixture prerequisite is missing: $required" >&2
        exit 2
    fi
done

fixture_scratch=$(mktemp -d "$TMPDIR/remote-native-XXXXXXXX")
export PORTICO_SAMBA_TEST_JOB="remote-sources-$(basename "$fixture_scratch" | tr '[:upper:]' '[:lower:]')"
docker_fixture=0
cleanup() {
    result=$?
    trap - EXIT HUP INT TERM
    cleanup_failed=0
    if test "$docker_fixture" = 1; then
    containers=$(docker ps -aq --filter label=portico=remote-sources-r40 --filter "label=portico.job=$PORTICO_SAMBA_TEST_JOB") || cleanup_failed=1
    while read -r container; do
        if test -n "$container"; then
            docker rm --force "$container" >/dev/null || cleanup_failed=1
        fi
    done <<< "${containers:-}"
    networks=$(docker network ls -q --filter label=portico=remote-sources-r40 --filter "label=portico.job=$PORTICO_SAMBA_TEST_JOB") || cleanup_failed=1
    while read -r network; do
        if test -n "$network"; then
            docker network rm "$network" >/dev/null || cleanup_failed=1
        fi
    done <<< "${networks:-}"
    if test -f "$fixture_scratch/samba-image"; then
        docker image rm "$(cat "$fixture_scratch/samba-image")" >/dev/null || cleanup_failed=1
    fi
    fi
    rm -rf -- "$fixture_scratch"
    if test "$cleanup_failed" != 0; then
        echo 'Owned native fixture cleanup failed.' >&2
        if test "$result" = 0; then result=1; fi
    fi
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

export PORTICO_FIXTURE_SCRATCH="$fixture_scratch"
python3 -u internal/rcloneprogram/testdata/fetch_locked_rclone.py \
    ../third_party/rclone/bundle.lock.json "$fixture_scratch"
export PORTICO_RCLONE_TEST_PROGRAM="$fixture_scratch/rclone"

# A direct-origin setup diagnostic must use an actually admitted private
# endpoint. Give this job its own stable bridge instead of borrowing another
# fixture's short-lived interface or relaxing the production never-list.
if test "${PORTICO_PRIVATE_ORIGIN_TEST:-}" = 1; then
    command -v docker >/dev/null || { echo 'Private origin fixture requires Docker.' >&2; exit 2; }
    if docker info --format '{{json .SecurityOptions}}' | python3 -c 'import json,sys; sys.exit(0 if "name=rootless" in json.load(sys.stdin) else 1)'; then
        echo 'Private origin fixture requires rootful Docker routing.' >&2
        exit 2
    fi
    docker_fixture=1
    export PORTICO_PRIVATE_ORIGIN_TEST_HOST=""
    for attempt in 1 2 3 4 5 6 7 8; do
        third=$((1 + (RANDOM % 250)))
        gateway="192.168.$third.1"
        if docker network create --internal --label portico=remote-sources-r40 --label "portico.job=$PORTICO_SAMBA_TEST_JOB" \
            --subnet "192.168.$third.0/24" --gateway "$gateway" "$PORTICO_SAMBA_TEST_JOB-origin" >/dev/null 2>&1; then
            export PORTICO_PRIVATE_ORIGIN_TEST_HOST="$gateway"
            break
        fi
    done
    test -n "$PORTICO_PRIVATE_ORIGIN_TEST_HOST" || { echo 'Owned private origin network could not be created.' >&2; exit 2; }
fi

# Other owned package fixtures use the same locked executable inside this exact
# cleanup scope, without copying a binary, dependency recipe or owner state.
if test "${1:-}" = --exec; then
    shift
    if test "$#" = 0; then exit 2; fi
    "$@"
    exit 0
fi
if test "${PORTICO_CONTROLLER_ONLY_TEST:-}" = 1; then
    go test -json -tags=rcloneintegration -count=1 -timeout=180s "$@" \
        ./internal/remotesources -run '^TestSourceControllerRealSharedProcessAndScanPublication$'
    exit 0
fi
if ! command -v docker >/dev/null; then
    echo 'Native fixture prerequisite is missing: docker' >&2
    exit 2
fi
if docker info --format '{{json .SecurityOptions}}' | python3 -c 'import json,sys; sys.exit(0 if "name=rootless" in json.load(sys.stdin) else 1)'; then
    echo 'Native NAS fixtures require rootful Docker routing for their isolated 192.168 network.' >&2
    exit 2
fi
docker_fixture=1
docker build --network=host --label portico=remote-sources-r40 --label "portico.job=$PORTICO_SAMBA_TEST_JOB" \
    --iidfile "$fixture_scratch/samba-image" internal/rcloneprogram/testdata/smb
export PORTICO_SAMBA_TEST_IMAGE="$(cat "$fixture_scratch/samba-image")"
if test "${PORTICO_NATIVE_SAVE_ONLY_TEST:-}" = 1; then
    go test -json -tags=rcloneintegration -count=1 -timeout=180s "$@" \
        ./internal/remotesources -run '^Test(NativeSavedNASConnectionAndForbiddenEdits|ImportedSavedPrivateEndpointsAndCryptOrigin)$'
    exit 0
fi
go test -json -tags=rcloneintegration -count=1 -timeout=180s "$@" \
    ./internal/rcloneprogram -run '^TestLocked(DAV|SFTP|SMB)MixedOperationBudget$'
if test "${PORTICO_NATIVE_SAVE_TEST:-}" = 1; then
    go test -json -tags=rcloneintegration -count=1 -timeout=180s "$@" \
        ./internal/remotesources -run '^Test(NativeSavedNASConnectionAndForbiddenEdits|SourceControllerRealSharedProcessAndScanPublication)$'
fi
