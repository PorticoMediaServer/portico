#!/usr/bin/env bash
# Remote storage acceptance: on a host you name, stand up a real WebDAV server and an rclone
# remote in front of it, add both to a Portico server as remote sources, scan a library on
# each, and play every film as it is and converted, with the sources' request limits on.
# Prints a report and keeps it as report.txt.
#
#   server-next/scripts/remote-acceptance.sh <ssh-host> --dir <folder> --dav-port <n> --port <n> [options]
#
#   --dir       the folder on the host to work in, relative to the home folder or absolute. It
#               is made if missing; a run replaces what an earlier run left there.
#   --dav-port  the port the WebDAV server listens on, on 127.0.0.1.
#   --port      the port the Portico server listens on, on 127.0.0.1.
#   --keep      leave the media, the logs and the server's data for a look.
#   --ffmpeg <folder>     the folder holding the ffmpeg and ffprobe to use, for the test media
#                         and as the server's own; the host's are used without it.
#   --keep-free-gb <n>    stop before making the media if less than n GB would be left free
#                         (the media and the server's data take about 1 GB); 5 without it.
#
# All three are required, so a run cannot land on what another server on the host uses; a
# port that is taken stops the run before anything starts. Run from a Portico checkout: it
# builds portico-server here, copies it and this script into the folder, and runs the rest
# there. The host needs ffmpeg, ffprobe, rclone, curl and jq; nothing is installed. Every
# server it starts listens on 127.0.0.1 only and is stopped by its PID at the end, whether
# the run passed, failed or was interrupted; the Portico server stops the rclone it started.
set -euo pipefail

usage() { sed -n '7,18p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
host="" dir="" dav_port="" port="" keep="" on_host="" ffmpeg_dir="" keep_free=5
while [[ $# -gt 0 ]]; do
	case "$1" in
	--on-host) on_host=1 ;;
	--dir) dir="${2:-}"; shift ;;
	--dav-port) dav_port="${2:-}"; shift ;;
	--port) port="${2:-}"; shift ;;
	--keep) keep=--keep ;;
	--ffmpeg) ffmpeg_dir="${2:-}"; shift ;;
	--keep-free-gb) keep_free="${2:-}"; shift ;;
	-*) usage ;;
	*) host="$1" ;;
	esac
	shift
done
[[ -n "$dir" && "$dir" != "/" && "$dir" != "~" && "$dir" != "." && "$dir" != *..* && "$dir" =~ ^[A-Za-z0-9._/-]+$ ]] || usage
for p in "$dav_port" "$port"; do [[ "$p" =~ ^[0-9]+$ && "$p" -ge 1024 && "$p" -le 65535 ]] || usage; done
[[ "$dav_port" != "$port" ]] || usage
[[ "$keep_free" =~ ^[0-9]+$ ]] || usage
[[ -z "$ffmpeg_dir" || "$ffmpeg_dir" =~ ^/[A-Za-z0-9._/-]+$ ]] || usage

if [[ -z "$on_host" ]]; then
	[[ -n "$host" ]] || usage
	here="$(cd "$(dirname "$0")/.." && pwd)"
	arch="$(ssh "$host" uname -m)"
	case "$arch" in x86_64) goarch=amd64 ;; aarch64 | arm64) goarch=arm64 ;; *) echo "unknown host arch $arch" >&2; exit 1 ;; esac
	build="$(mktemp -d "$here/../.acceptance-build.XXXXXX")"
	trap 'rm -rf "$build"' EXIT
	(cd "$here" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 GO_OFFLOAD_OFF=1 go build -o "$build/portico-server" ./cmd/portico-server)
	cp "$0" "$build/acceptance.sh"
	cp "$here/internal/streaming/testdata/client-profiles.json" "$build/"
	ssh "$host" "mkdir -p '$dir'"
	scp -q "$build/portico-server" "$build/acceptance.sh" "$build/client-profiles.json" "$host:$dir/"
	status=0
	ssh "$host" "bash '$dir/acceptance.sh' --on-host --dir '$dir' --dav-port $dav_port --port $port --keep-free-gb $keep_free ${ffmpeg_dir:+--ffmpeg '$ffmpeg_dir'} $keep" || status=$?
	scp -q "$host:$dir/report.txt" "./remote-acceptance-$host-$(date -u +%Y%m%dT%H%M%SZ).txt" && echo "report copied here"
	exit "$status"
fi

# ---- on the host ----
cd ~ && cd "$dir"
taken() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
for p in "$dav_port" "$port"; do
	if taken "$p"; then echo "port $p on 127.0.0.1 is in use on this host; choose another" >&2; exit 1; fi
done
if [[ -n "$ffmpeg_dir" ]]; then
	[[ -x "$ffmpeg_dir/ffmpeg" && -x "$ffmpeg_dir/ffprobe" ]] || { echo "no ffmpeg and ffprobe in $ffmpeg_dir" >&2; exit 1; }
	export PATH="$ffmpeg_dir:$PATH" PORTICO_FFMPEG="$ffmpeg_dir/ffmpeg" PORTICO_FFPROBE="$ffmpeg_dir/ffprobe"
fi
free_gb=$(df -Pk . | awk 'NR == 2 { print int($4 / 1048576) }')
if ((free_gb - 1 < keep_free)); then
	echo "only $free_gb GB free here; the run would leave less than $keep_free GB" >&2
	exit 1
fi
for tool in ffmpeg ffprobe rclone curl jq; do
	command -v "$tool" >/dev/null || { echo "missing on this host: $tool" >&2; exit 1; }
done
rm -rf media data logs report.txt && mkdir -p media/Films media/Many logs
# Report lines go to report.txt and to stderr, so a function's output can be captured.
report() { echo "$*" | tee -a report.txt >&2; }
check() { # check <name> <ok:0/1> <detail>
	if [[ "$2" == 0 ]]; then report "PASS $1: $3"; else report "FAIL $1: $3"; fi
}
since() { awk -v a="$1" -v b="$(date +%s.%N)" 'BEGIN { printf "%.1f", b - a }'; }
pids=()
# cleanup stops what this run started, by PID only: the Portico server first (it stops its
# own rclone as it shuts down), then the WebDAV server, waiting for each to exit.
cleanup() {
	local pid
	for ((i = ${#pids[@]} - 1; i >= 0; i--)); do
		pid=${pids[i]}
		kill "$pid" 2>/dev/null || continue
		for _ in $(seq 100); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
		kill -9 "$pid" 2>/dev/null || true
	done
	[[ "$keep" == "--keep" ]] || rm -rf media data portico-server
}
trap cleanup EXIT
trap 'exit 130' INT TERM

report "Portico remote storage acceptance on $(hostname), $(date -u +%FT%TZ)"
report "rclone $(rclone version | head -1); $(command -v ffmpeg): $(ffmpeg -version | head -1); $free_gb GB free at the start"

# The media: a 1080p film, a 4K film, an MP4 whose picture and sound are stored in long
# separate runs (badly interleaved), and one folder of 3,000 small files to time a big
# listing (scanned with File list only, so nothing in it is read).
enc=(-nostdin -hide_banner -loglevel error -y)
mkdir -p "media/Films/Plain (2020)" "media/Films/Big (2021)" "media/Films/Interleaved (2022)"
ffmpeg "${enc[@]}" -f lavfi -i testsrc2=size=1920x1080:rate=24 -f lavfi -i sine=frequency=440:sample_rate=48000 -t 30 \
	-c:v libx264 -preset veryfast -pix_fmt yuv420p -c:a aac -movflags +faststart "media/Films/Plain (2020)/Plain (2020).mp4"
ffmpeg "${enc[@]}" -f lavfi -i testsrc2=size=3840x2160:rate=24 -f lavfi -i sine=frequency=330:sample_rate=48000 -t 30 \
	-c:v libx264 -preset ultrafast -b:v 40M -pix_fmt yuv420p -c:a aac "media/Films/Big (2021)/Big (2021).mkv"
ffmpeg "${enc[@]}" -f lavfi -i testsrc2=size=1280x720:rate=24 -f lavfi -i sine=frequency=550:sample_rate=48000 -t 60 \
	-c:v libx264 -preset veryfast -pix_fmt yuv420p -c:a aac -max_interleave_delta 0 -chunk_duration 30000000 \
	"media/Films/Interleaved (2022)/Interleaved (2022).mp4"
ffmpeg "${enc[@]}" -f lavfi -i testsrc2=size=320x240:rate=24 -t 1 -c:v libx264 -preset ultrafast media/clip.mkv
for i in $(seq -w 1 3000); do ln media/clip.mkv "media/Many/Clip $i (2000).mkv"; done
rm media/clip.mkv
report "media made: $(du -sh media | cut -f1)"

# The WebDAV server: rclone serving the media folder, logging every request.
dav_pass=$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')
rclone serve webdav media --addr "127.0.0.1:$dav_port" --user viewer --pass "$dav_pass" --read-only -vv --log-file logs/webdav.log &
pids+=($!)
for _ in $(seq 50); do curl -fsu "viewer:$dav_pass" -X PROPFIND -H 'Depth: 0' "http://127.0.0.1:$dav_port/" >/dev/null 2>&1 && break; sleep 0.2; done

# The Portico server, with the host's rclone as the one that comes with it.
PORTICO_RCLONE="$(command -v rclone)" ./portico-server -listen "127.0.0.1:$port" -data "$PWD/data" >logs/server.log 2>&1 &
pids+=($!)
api="http://127.0.0.1:$port"
for _ in $(seq 100); do curl -fs "$api/v1/health/live" >/dev/null 2>&1 && break; sleep 0.2; done
token=""
# call <method> <path> [json] -> body; fails on a status of 400 or more. Every call is in
# logs/api.log with its status and the start of its answer.
call() {
	local args=(-sS -X "$1" "$api$2" -H "Authorization: Bearer $token" -o logs/answer -w '%{http_code}') status
	[[ $# -ge 3 ]] && args+=(-H 'Content-Type: application/json' --data "$3")
	status=$(curl "${args[@]}" 2>>logs/api.log) || status=000
	echo "$1 $2 -> $status $(head -c 400 logs/answer 2>/dev/null | tr '\n' ' ')" >>logs/api.log
	cat logs/answer 2>/dev/null || true
	[[ "$status" == 2* ]]
}
# media <path> <file> [range] fetches a stream's file, waiting while it is being prepared
# (503); it fails when the answer is not a success after a minute or two.
media() {
	local status args=(-sS -H "Authorization: Bearer $token" -o "$2" -w '%{http_code}' "$api$1")
	[[ $# -ge 3 ]] && args+=(-H "Range: bytes=$3")
	for _ in $(seq 240); do
		status=$(curl "${args[@]}" 2>>logs/api.log) || status=000
		[[ "$status" == 503 || "$status" == 000 ]] || break
		sleep 0.5
	done
	echo "GET $1 -> $status" >>logs/api.log
	[[ "$status" == 200 || "$status" == 206 ]]
}
code=$(tr -d '[:space:]' <data/setup-code.txt)
device='{"installationId":"'"$(printf 'a%.0s' {1..32})"'","name":"Acceptance"}'
owner=$(call POST /v1/setup/owner '{"setupCode":"'"$code"'","username":"owner","password":"acceptance run password","device":'"$device"'}')
token=$(jq -r .session.accessToken <<<"$owner")
session=$(call POST "/v1/profiles/$(jq -r '.profiles[0].id' <<<"$owner")/select" '{}')
token=$(jq -r .accessToken <<<"$session")
call PUT "/v1/devices/$(jq -r .deviceId <<<"$session")/capabilities" "$(jq .chromium client-profiles.json)" >/dev/null

# The two sources: the WebDAV server itself, and an rclone remote in front of it. Both on
# the default preset (4 connections, 4 requests a second).
webdav=$(call POST /v1/admin/remote-sources '{"kind":"webdav","name":"WebDAV","webdav":{"address":"http://127.0.0.1:'"$dav_port"'/","user":"viewer","password":"'"$dav_pass"'"}}') ||
	{ check "add WebDAV source" 1 "refused"; exit 1; }
check "add WebDAV source" 0 "$(jq -r '.state' <<<"$webdav")"
remote_conf=$(printf '[acceptance]\ntype = webdav\nurl = http://127.0.0.1:%s/\nvendor = other\nuser = viewer\npass = %s\n' "$dav_port" "$(rclone obscure "$dav_pass")")
rclone_src=$(call POST /v1/admin/remote-sources "$(jq -n --arg c "$remote_conf" '{kind:"rclone",name:"rclone",rclone:{config:$c}}')") ||
	{ check "add rclone source" 1 "refused"; exit 1; }
check "add rclone source" 0 "$(jq -r '.state' <<<"$rclone_src")"

scan_library() { # scan_library <name> <source id> <folder> [depth] -> library id; reports the scan
	local lib id start state
	start=$(date +%s.%N)
	lib=$(call POST /v1/admin/libraries "$(jq -n --arg n "$1" --arg s "$2" --arg p "$3" --arg d "${4:-basic}" \
		'{name:$n,kind:"movies",remoteFolders:[{source:$s,path:$p}],options:{depth:$d,trash:{mode:"manual"}}}')") ||
		{ check "make library $1" 1 "refused"; return 0; }
	id=$(jq -r .id <<<"$lib")
	for _ in $(seq 1800); do
		state=$(call GET "/v1/admin/libraries/$id/scan" | jq -r .state)
		[[ "$state" == idle ]] && break
		sleep 1
	done
	local counts
	counts=$(call GET "/v1/admin/libraries/$id/scan" | jq -c '{counts, folders: [.folders[] | {available, problem}]}')
	check "scan $1" "$([[ "$state" == idle ]] && echo 0 || echo 1)" "$(since "$start") s, $counts"
	echo "$id"
}

play_library() { # play_library <library id> <label>
	local items item title options local_file sess url started body same
	items=$(call GET "/v1/items?library=$1&kind=movie&limit=100")
	for item in $(jq -r '.items[].id' <<<"$items"); do
		title=$(jq -r --arg i "$item" '.items[] | select(.id==$i) | .title' <<<"$items")
		local_file=$(find media/Films -name "$title (*).*" | head -1)
		options=$(call GET "/v1/items/$item/playback-options")
		check "$2 facts: $title" "$([[ $(jq '.versions[0].durationMs // 0' <<<"$options") -gt 0 ]] && echo 0 || echo 1)" \
			"$(jq -c '.versions[0] | {durationMs, video: [.video[]? | {codec, height}], audio: [.audio[]? | .codec]}' <<<"$options")"
		# As it is, by byte range: a megabyte from the middle must equal the file.
		sess=$(call POST /v1/playback/sessions '{"itemId":"'"$item"'"}' || true)
		url=$(jq -r '.presentation.url // empty' <<<"$sess")
		if [[ -n "$url" && "$(jq -r .presentation.mode <<<"$sess")" == direct ]]; then
			local size=$(stat -c %s "$local_file") from to
			from=$((size / 2)) to=$((size / 2 + 1048575))
			started=$(date +%s.%N)
			media "$url" logs/range.bin "$from-$to" || true
			same=1
			cmp -s logs/range.bin <(tail -c +$((from + 1)) "$local_file" | head -c 1048576) && same=0
			check "$2 direct range: $title" "$same" "1 MB in $(since "$started") s"
		else
			report "NOTE $2 direct: $title is not offered as it is to this browser profile ($(jq -r '.presentation.route // "no session"' <<<"$sess"))"
		fi
		# Converted at a low quality: the first three segments, and how long the first took.
		local id
		id=$(jq -r '.id // empty' <<<"$sess")
		if [[ -z "$id" ]]; then
			check "$2 play: $title" 1 "no session: $(tail -1 logs/api.log | cut -c1-300)"
			continue
		fi
		sess=$(call PATCH "/v1/playback/sessions/$id" '{"positionMs":0,"quality":{"mode":"fixed","id":"240p"}}' || echo '{}')
		url=$(jq -r '.presentation.url // empty' <<<"$sess")
		started=$(date +%s.%N)
		local base=${url%/*}/ variant="" segments="" got=0
		if [[ -z "$url" ]]; then
			check "$2 converted: $title" 1 "no stream at 240p: $(grep PATCH logs/api.log | tail -1 | cut -c1-300)"
		else
			# The address is a master playlist naming the picture's playlist, or, when nothing
			# else is offered beside it, the picture's playlist itself.
			if media "$url" logs/master.m3u8; then
				if grep -q '^#EXTINF' logs/master.m3u8; then
					cp logs/master.m3u8 logs/variant.m3u8 && variant=.
				else
					variant=$(grep -v '^#' logs/master.m3u8 | grep . | head -1 || true)
					[[ -n "$variant" ]] && media "$base$variant" logs/variant.m3u8 || variant=""
				fi
			fi
			if [[ -n "$variant" ]]; then
				[[ "$variant" == */* ]] && base="$base${variant%/*}/"
				segments=$(grep -v '^#' logs/variant.m3u8 | grep . | head -3 || true)
			fi
			for segment in $segments; do
				media "$base$segment" logs/segment.bin && [[ -s logs/segment.bin ]] && got=$((got + 1))
			done
			check "$2 converted: $title" "$([[ $got -ge 1 ]] && echo 0 || echo 1)" \
				"$got of 3 segments in $(since "$started") s$([[ $got -ge 1 ]] || echo "; last: $(tail -1 logs/api.log)")"
		fi
		call DELETE "/v1/playback/sessions/$id" >/dev/null || true
	done
}

dav_lib=$(scan_library "Films over WebDAV" "$(jq -r .id <<<"$webdav")" Films | tail -1)
play_library "$dav_lib" WebDAV
rclone_lib=$(scan_library "Films over rclone" "$(jq -r .id <<<"$rclone_src")" Films | tail -1)
play_library "$rclone_lib" rclone
scan_library "Many over WebDAV" "$(jq -r .id <<<"$webdav")" Many files >/dev/null

# The request limits: the busiest second the WebDAV server saw, and the totals. rclone in
# front of it has its own pacing, so its seconds count too.
requests() { grep -E ": (PROPFIND|GET|HEAD) from " logs/webdav.log || true; }
busiest=$(requests | grep -oE '^[0-9/]+ [0-9:]+' | sort | uniq -c | sort -rn | head -1 | awk '{ print $1 " requests at " $2 " " $3 }')
report "WebDAV requests: $(requests | grep -c PROPFIND || true) listings, $(requests | grep -cE ': (GET|HEAD) ' || true) reads; busiest second: ${busiest:-none}"
report "server log warnings: $(grep -cE 'level=(WARN|ERROR)' logs/server.log || true) (see logs/server.log with --keep)"
failures=$(grep -c '^FAIL' report.txt || true)
report "$([[ $failures == 0 ]] && echo 'ALL PASSED' || echo "$failures FAILED")"
[[ $failures == 0 ]]
