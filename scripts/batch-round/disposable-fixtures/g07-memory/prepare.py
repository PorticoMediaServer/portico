#!/usr/bin/env python3
"""Prepare one G07 phase: owner, settings, library, scan and item inventory.

Runs against a disposable fresh server only. Writes a private 0600 fixture file
containing item ids and subtitle ids; it never writes the bearer token.
"""

import argparse
import json
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))
import api  # noqa: E402


SETTING_NAMES = {
    "transcodeLocation", "memoryCap", "temporaryDirectory", "conversionMode",
    "sessionStorageMiB", "maxConcurrentSessions", "maxHardwareSessions",
    "maxSoftwareSessions",
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--media-root", required=True)
    parser.add_argument("--library-name", default="G07 fixture movies")
    parser.add_argument("--settings", default="{}")
    parser.add_argument("--out", required=True)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    settings = json.loads(args.settings)
    unknown = set(settings) - SETTING_NAMES
    if unknown:
        raise SystemExit("unsupported settings: %s" % ", ".join(sorted(unknown)))
    if not args.execute:
        print("PLAN: would prepare owner/settings/library/scan at %s" % args.base)
        return 0
    if os.path.exists(args.out):
        raise SystemExit("refusing to overwrite a previous fixture file")

    client = api.Api.load(args.base, args.token_file)
    doc = api.patch_settings(client, settings, idempotency_key="g07-prepare-%d" % int(time.time()))
    effective = doc.get("effective", {})
    for key, want in settings.items():
        if effective.get(key) != want:
            raise SystemExit("setting %s is %r, wanted %r" % (key, effective.get(key), want))
    if effective.get("conversionMode") != "all":
        raise SystemExit("conversionMode must stay 'all' for the conversion fixture")

    libraries = client.request("GET", "/v1/libraries")
    libraries = libraries if isinstance(libraries, list) else libraries.get("items", [])
    library = next((l for l in libraries if l.get("name") == args.library_name), None)
    if library is None:
        library = api.create_library(client, args.library_name, "movie", args.media_root)
    api.wait_inventory(client, library["id"])
    entries = api.browse_items(client, library["id"])
    movies = [e for e in entries if e.get("kind") == "movie"]
    if not movies:
        raise SystemExit("scan produced no movie entries; check the media mount")
    items = []
    for entry in movies:
        tracks = []
        try:
            offers = api.playback_offers(client, entry["id"])
            plan = offers.get("subtitlePlan") or {}
            tracks = [r.get("id") for r in plan.get("resources", [])
                      if r.get("id") and r.get("format") in ("srt", "text", "ass")]
        except api.ApiError:
            tracks = []
        items.append({"id": entry["id"], "title": entry.get("title", ""),
                      "subtitleTracks": tracks})
    out = {"serverId": client.state.get("serverId"), "libraryId": library["id"],
           "mediaRoot": args.media_root, "settings": effective,
           "items": items, "preparedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    directory = os.path.dirname(os.path.abspath(args.out)) or "."
    os.makedirs(directory, mode=0o700, exist_ok=True)
    fd = os.open(args.out, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(out, handle, indent=2)
        handle.write("\n")
    print("prepared library %s with %d movie items" % (library["id"], len(items)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
