# Helpers for .github/workflows/server-next-load.yml. Sourced, not run.

# limited runs a command under the 2 GB memory limit the plan's bars assume, with no swap,
# as the invoking user. A process that goes over is killed and the step fails.
limited() {
  sudo systemd-run --quiet --wait --collect --pipe \
    -p MemoryMax=2G -p MemorySwapMax=0 \
    --uid="$(id -u)" --gid="$(id -g)" --working-directory="$PWD" "$@"
}

# summarise adds a titled block of text to the run summary and hands the JSON file to later
# jobs as this step's "report" output.
summarise() {
  local title=$1 text=$2 json=$3
  {
    echo "### $title"
    echo
    echo "Runner: ubuntu-24.04, $(nproc) vCPU, $(awk '/MemTotal/ {printf "%.0f", $2 / 1048576}' /proc/meminfo) GB, measured process limited to 2 GB"
    echo
    echo '```'
    cat "$text"
    echo '```'
  } >> "$GITHUB_STEP_SUMMARY"
  echo "report=$(jq -c . "$json")" >> "$GITHUB_OUTPUT"
}
