#!/usr/bin/env python3
"""Drive 2-4 real conversions for one G07 phase.

Real HLS playlists and segments are fetched from the server grants, which drives
the producer; seeks, quality changes and subtitle burn-in change the rendition;
settings are lowered mid-run. Seeks and fetches are bounded, and the receipt
holds pathnames and hashes only, never grant URLs or tokens.
"""

import argparse
import hashlib
import json
import os
import random
import sys
import time
import urllib.error
import urllib.parse

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))
import api  # noqa: E402


def hls_paths(playlist):
    paths = []
    for line in playlist.decode("utf-8", "replace").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        paths.append(line)
    return paths


def safe_path(url, base):
    parts = urllib.parse.urlsplit(url)
    base_parts = urllib.parse.urlsplit(base)
    if parts.scheme and parts.netloc and (parts.scheme, parts.netloc) != (base_parts.scheme, base_parts.netloc):
        raise SystemExit("grant left the server origin")
    path = parts.path + (("?" + parts.query) if parts.query else "")
    if not path.startswith("/v1/media/"):
        raise SystemExit("grant is not a media route: %s" % parts.path)
    return path


def fetch(api_client, path):
    status, body, _ = api_client.request_raw("GET", path, expect=(200, 206))
    return status, body


class SessionDriver:
    def __init__(self, client, item, subtitle, index, args):
        self.client = client
        self.item = item
        self.subtitle = subtitle
        self.index = index
        self.args = args
        self.session = None
        self.bytes = 0
        self.fetched = set()
        self.seeks = 0
        self.changes = 0
        self.initial = None
        self.final = None
        self.states = []
        self.errors = []

    def start(self):
        quality = {"mode": "limit", "maxHeight": self.args.max_height}
        subtitles = {"trackId": self.subtitle, "delivery": "burn"} if self.subtitle else {"trackId": None}
        self.session = api.start_session(self.client, self.item, quality=quality, subtitles=subtitles)
        self.states.append(self.session.get("state"))

    @property
    def generation(self):
        return (self.session or {}).get("presentation", {}).get("generation")

    def playlist_path(self):
        return safe_path(self.session["presentation"]["url"], self.client.base)

    def consume(self, capture=False):
        status, playlist = fetch(self.client, self.playlist_path())
        playlist_sha = hashlib.sha256(playlist).hexdigest()
        if b'#EXT-X-STREAM-INF:' in playlist:
            variants = hls_paths(playlist)
            if len(variants) != 1:
                raise SystemExit('expected one HLS variant')
            leaf = urllib.parse.urljoin(self.playlist_path(), variants[0])
            _, playlist = fetch(self.client, safe_path(leaf, self.client.base))
            playlist_base = leaf
        else:
            playlist_base = self.playlist_path()
        segments = hls_paths(playlist)
        new = [s for s in segments if s not in self.fetched]
        for relative in new[:2]:
            path = safe_path(urllib.parse.urljoin(playlist_base, relative), self.client.base)
            _, body = fetch(self.client, path)
            self.bytes += len(body)
            self.fetched.add(relative)
            if capture and self.initial is None:
                self.initial = {
                    "generation": self.generation,
                    "segment": os.path.basename(urllib.parse.urlsplit(path).path),
                    "sha256": hashlib.sha256(body).hexdigest(),
                    "size": len(body),
                    "playlistSha256": playlist_sha,
                    "playlistGeneration": self.generation,
                }
        if self.initial is not None and self.final is None and capture:
            # Re-read the first captured segment by basename in the current playlist.
            for relative in segments:
                if os.path.basename(urllib.parse.urlsplit(relative).path) != self.initial["segment"]:
                    continue
                path = safe_path(urllib.parse.urljoin(playlist_base, relative), self.client.base)
                _, body = fetch(self.client, path)
                self.bytes += len(body)
                if self.generation == self.initial["generation"]:
                    self.final = {
                        "generation": self.generation,
                        "segment": self.initial["segment"],
                        "sha256": hashlib.sha256(body).hexdigest(),
                        "size": len(body),
                        "playlistSha256": playlist_sha,
                    }
                break
        return len(new)

    def seek(self, position_ms):
        seek_id = "g07-%d-%d-%s" % (self.index, self.seeks, os.urandom(4).hex())
        self.session = api.change_session(self.client, self.session, {
            "seek": {"positionMs": position_ms, "id": seek_id},
            "positionMs": position_ms,
        })
        self.seeks += 1
        self.states.append(self.session.get("state"))

    def switch(self, max_height):
        self.session = api.change_session(self.client, self.session, {
            "quality": {"mode": "limit", "maxHeight": max_height},
            "positionMs": self.session.get("presentation", {}).get("startPositionMs", 0),
        })
        self.changes += 1
        self.states.append(self.session.get("state"))

    def stop(self):
        position = self.session.get("presentation", {}).get("startPositionMs", 0)
        try:
            api.stop_session(self.client, self.session, position)
        except (api.ApiError, urllib.error.URLError, TimeoutError) as error:
            self.errors.append('stop_failed_' + str(getattr(error, 'status', 'wire')))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--fixture", required=True, help="prepare.py output")
    parser.add_argument("--sessions", type=int, default=3)
    parser.add_argument("--seconds", type=float, default=180)
    parser.add_argument("--max-height", type=int, default=480)
    parser.add_argument("--seek-every", type=float, default=12)
    parser.add_argument("--switch-at", type=float, default=0, help="seconds before one quality switch (0 disables)")
    parser.add_argument("--lower-cap-at", type=float, default=0, help="seconds before memoryCap=quarter")
    parser.add_argument("--disk-at", type=float, default=0, help="seconds before transcodeLocation=disk")
    parser.add_argument("--spill-trigger", help="write this file when the first spill is observed")
    parser.add_argument("--kill-after-spill-ms", type=int, default=0,
                        help="if set, record an interrupted run after this delay and stop driving")
    parser.add_argument("--out", required=True)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    if not args.execute:
        print("PLAN: would drive %d conversions for %.0fs" % (args.sessions, args.seconds))
        return 0
    if not 2 <= args.sessions <= 4:
        raise SystemExit("2-4 sessions are required")
    if os.path.exists(args.out):
        raise SystemExit("refusing to overwrite a previous receipt")

    fixture = json.load(open(args.fixture, "r", encoding="utf-8"))
    media = [i for i in fixture["items"] if i["subtitleTracks"]]
    plain = [i for i in fixture["items"] if not i["subtitleTracks"]]
    pool = media + plain + fixture["items"]
    if not pool:
        raise SystemExit("fixture file has no items")
    pool = [pool[i % len(pool)] for i in range(args.sessions)]
    client = api.Api.load(args.base, args.token_file)
    drivers = [SessionDriver(client, item["id"],
                             item["subtitleTracks"][0] if item["subtitleTracks"] and idx == 0 else None,
                             idx, args)
               for idx, item in enumerate(pool)]
    receipt = {"phase": "g07-drive", "base": client.base, "startedAt": time.time(),
               "sessions": [], "settingsTransitions": [], "errors": [], "interrupted": False,
               "spillObservedAt": None, "killAfterSpillMs": args.kill_after_spill_ms}
    started = time.time()
    spills_seen = 0
    try:
        for drv in drivers:
            drv.start()
            # Capture the first presented segment immediately; later captures
            # compare the same segment name within the same generation.
            try:
                drv.consume(capture=True)
            except (api.ApiError, urllib.error.URLError):
                pass
            time.sleep(0.4)
        receipt["sessionsStarted"] = [d.session["id"] for d in drivers]
        last_seek = started
        while time.time() - started < args.seconds:
            elapsed = time.time() - started
            for drv in drivers:
                try:
                    drv.consume(capture=drv.initial is None)
                except (api.ApiError, urllib.error.URLError) as err:
                    drv.errors.append(getattr(err, "status", None) or "wire")
            if args.switch_at and elapsed >= args.switch_at and elapsed < args.switch_at + 2:
                drivers[-1].switch(min(720, args.max_height * 2))
                args.switch_at = 0
            if args.seek_every and time.time() - last_seek >= args.seek_every:
                target = drivers[random.randrange(len(drivers))]
                target.seek(random.randrange(5_000, 60_000))
                last_seek = time.time()
            if args.lower_cap_at and elapsed >= args.lower_cap_at and args.lower_cap_at > 0:
                doc = api.patch_settings(client, {"memoryCap": "quarter"},
                                         idempotency_key="g07-cap-%d" % int(time.time()))
                receipt["settingsTransitions"].append({"at": time.time(), "change": {"memoryCap": "quarter"},
                                                       "effective": doc["effective"]["memoryCap"]})
                args.lower_cap_at = 0
            if args.disk_at and elapsed >= args.disk_at and args.disk_at > 0:
                doc = api.patch_settings(client, {"transcodeLocation": "disk"},
                                         idempotency_key="g07-disk-%d" % int(time.time()))
                receipt["settingsTransitions"].append({"at": time.time(), "change": {"transcodeLocation": "disk"},
                                                       "effective": doc["effective"]["transcodeLocation"]})
                args.disk_at = 0
            if args.spill_trigger or args.kill_after_spill_ms:
                try:
                    diagnostics = api.diagnostics(client)
                    spills = int(diagnostics.get("memorySpills", 0))
                    if spills > spills_seen:
                        spills_seen = spills
                        receipt["spillObservedAt"] = time.time()
                        if args.spill_trigger:
                            with open(args.spill_trigger, "w", encoding="utf-8") as handle:
                                handle.write("spill\n")
                        if args.kill_after_spill_ms:
                            time.sleep(args.kill_after_spill_ms / 1000.0)
                            receipt["interrupted"] = True
                            receipt["interruptedAt"] = time.time()
                            break
                except api.ApiError:
                    pass
            time.sleep(0.5)
    finally:
        receipt["endedAt"] = time.time()
        for drv in drivers:
            if drv.session is not None:
                try:
                    drv.consume(capture=True)
                except (api.ApiError, urllib.error.URLError):
                    pass
                if not receipt["interrupted"]:
                    drv.stop()
            receipt["sessions"].append({
                "index": drv.index,
                "id": drv.session["id"] if drv.session else None,
                "itemId": drv.item,
                "subtitleTrack": drv.subtitle,
                "bytesFetched": drv.bytes,
                "seeks": drv.seeks,
                "changes": drv.changes,
                "states": drv.states,
                "generation": drv.generation,
                "initialSegment": drv.initial,
                "finalSegment": drv.final,
                "errors": drv.errors,
            })
        if client._session_bridge is not None:
            try: client._session_bridge.close()
            except api.ApiError: receipt['errors'].append('canonical_adapter_cleanup_failed')
        fd = os.open(args.out, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(receipt, handle, indent=2)
            handle.write("\n")
    print("drove %d sessions; receipt written privately" % len(drivers))
    return 1 if receipt["errors"] or any(d.errors for d in drivers) else 0


if __name__ == "__main__":
    sys.exit(main())
