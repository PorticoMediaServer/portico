#!/usr/bin/env python3
"""Verify the generated synthetic library and the seeded state over the real API.

Read-only.  It uses the current registered surfaces only:

* ``POST /v1/libraries/{id}/browse`` for movies, shows, episodes, artists,
  albums and songs (paged by ``pageInfo.nextCursor``);
* ``GET /v1/reading/items?libraryId=`` for book/comic publications;
* ``GET /v1/libraries/{id}/shows``, ``/artists`` and ``/books`` for structure;
* ``GET /v1/books/{id}/files`` for audiobook parts.

It never reads generated media locally.  ``--reading`` opens short real reading
sessions to read back stored progress and annotations, then closes them.
Playback and concurrency belong to ``playback_smoke.py``; this script does not
duplicate that verifier.

Manifest counts: library entries may declare ``expected`` (or numeric
``movies/shows/episodes/artists/albums/songs/books``).  When they do not:

* video expected counts are derived from file entries exactly as
  ``video.expected_counts`` does (unique movie/disc/show titles, episode count);
* reading expected counts are entries whose path is *under that exact library
  prefix* (nested ``Reading/Books`` and ``Reading/Magazines`` never share a
  count);
* music and audiobook libraries report actual route counts but compare exactly
  only when the generator declares expected totals.

Usage::

    python3 verify.py --manifest ~/.private/walks/test-library-v3-manifest.json
    python3 verify.py --manifest ... --reading --report /tmp/verify.json
"""
from __future__ import annotations

import argparse
import json
import os
import stat
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

MAX_BODY = 16 * 1024 * 1024
DEFAULT_BASE = "http://127.0.0.1:49300"
DEFAULT_CREDENTIALS = Path("/home/justin/projects/Portico/.private/walks/credentials.json")
DEFAULT_ROOT = "/home/runner/portico-test-library-v3"
LAYOUT = {"Movies": "movie", "TV": "tv", "Anime": "anime", "Music": "music",
          "Audiobooks": "audiobook", "Reading/Books": "book", "Books": "book",
          "Reading/Comics": "comic", "Comics": "comic",
          "Reading/Magazines": "book", "Magazines": "book",
          "Reading/Newspapers": "book", "Reading/Mixed": "book",
          "Reading/Large Books": "book"}


def redact(text):
    """Never let a server error body carry a credential into a report."""
    if not text:
        return text
    import re as _re
    for key in ("accessToken", "refreshToken", "token", "password", "secret", "authorization"):
        text = _re.sub(rf'("{key}"\s*:\s*")[^"]*(")', r'\1<redacted>\2', text, flags=_re.I)
    return text[:2000]


class Refusal(Exception):
    def __init__(self, code, status=0, detail=""):
        super().__init__(code)
        self.code = code
        self.status = status
        self.detail = detail


def items(document):
    if isinstance(document, list):
        return document
    if not isinstance(document, dict):
        return []
    if isinstance(document.get("data"), dict):
        document = document["data"]
    for key in ("items", "entries", "resources", "playlists", "channels", "sources",
                "backups", "profiles", "artists", "shows", "books", "files", "sections"):
        if key in document:
            value = document[key]
            if key == "sections":
                return [entry for section in value for entry in section.get("entries", [])]
            if isinstance(value, list):
                return value
    return []


def private_json(path):
    st = path.lstat()
    if not stat.S_ISREG(st.st_mode) or st.st_uid != os.getuid() or stat.S_IMODE(st.st_mode) != 0o600:
        raise SystemExit("credentials file must be a private 0600 file owned by this user")
    return json.loads(path.read_text(encoding="utf-8"))


def under(path, prefix):
    path = str(path or "").replace("\\", "/").strip("/")
    prefix = str(prefix or "").replace("\\", "/").strip("/")
    return bool(prefix) and (path == prefix or path.startswith(prefix + "/"))


class Client:
    def __init__(self, base, installation=None, token=""):
        self.base = base.rstrip("/")
        self.installation = installation or str(uuid.uuid4())
        self.token = token
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(self, method, path, body=None, ok=(200, 201, 202, 204), timeout=60, extra_headers=None):
        headers = {"Accept": "application/json", "Origin": self.base,
                   "X-Portico-Installation-Id": self.installation}
        data = None
        if body is not None:
            data = json.dumps(body, separators=(",", ":")).encode()
            headers["Content-Type"] = "application/json"
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        headers.update(extra_headers or {})
        request = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            response = self.opener.open(request, timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        except Exception as error:
            raise Refusal("transport_" + type(error).__name__)
        with response:
            payload = response.read(MAX_BODY + 1)
            if len(payload) > MAX_BODY:
                raise Refusal("oversized_response", response.status)
            if response.status not in ok:
                try:
                    document = json.loads(payload) if payload else None
                except ValueError:
                    document = None
                code = ""
                if isinstance(document, dict) and isinstance(document.get("error"), dict):
                    code = str(document["error"].get("code", ""))
                detail = redact(payload.decode("utf-8", "replace")[:2000]) if payload else ""
                raise Refusal(code or "http_" + str(response.status), response.status, detail)
            content_type = response.headers.get("Content-Type", "").split(";")[0]
            if content_type in ("image/png", "image/jpeg", "application/octet-stream"):
                return payload
            try:
                document = json.loads(payload) if payload else None
            except ValueError:
                return payload
            if isinstance(document, dict) and {"protocolVersion", "serverId", "result"} <= document.keys():
                return document["result"]
            return document

    def read(self, path, attempts=12, extra_headers=None):
        last = None
        for attempt in range(attempts):
            try:
                return self.call("GET", path, extra_headers=extra_headers)
            except Refusal as refusal:
                last = refusal
                if refusal.status not in (409, 412, 423, 503, 504) or attempt == attempts - 1:
                    raise
                time.sleep(0.6 * (attempt + 1))
        raise last


def sign_in(client, username, password):
    last = None
    for attempt in range(8):
        try:
            signed = client.call("POST", "/v1/direct/sign-in",
                                 {"username": username, "password": password})
            session = signed.get("session", signed)
            client.token = session["accessToken"]
            return signed
        except Refusal as refusal:
            last = refusal
            if refusal.status not in (429, 503, 504) or attempt == 7:
                raise
            time.sleep(min(20, 3 * (attempt + 1)))
    raise last


def select(client, profile_id, pin=None):
    body = {"installationId": client.installation, "trust": False}
    if pin:
        body["pin"] = pin
    last = None
    for attempt in range(8):
        try:
            selected = client.call("POST", "/v1/direct/profiles/" +
                                   urllib.parse.quote(profile_id, safe="") + "/select", body)
            client.token = selected["session"]["accessToken"]
            return
        except Refusal as refusal:
            last = refusal
            if refusal.status not in (409, 429, 503, 504) or attempt == 7:
                raise
            time.sleep(min(15, 1.5 * (attempt + 1)))
    raise last


def open_profile(base, username, password, profile_id, pin=None):
    last = None
    for attempt in range(4):
        client = Client(base, str(uuid.uuid4()))
        try:
            _, token = sign_in(client, username, password)
            client.token = token
            select(client, profile_id, pin)
            return client
        except Refusal as refusal:
            last = refusal
            if refusal.status not in (409, 429, 503, 504) or attempt == 4:
                raise
            time.sleep(min(20, 3 * (attempt + 1)))
    raise last


def browse(client, library_id, pivot, maximum=26000):
    """Page the real browse surface; never the stale /v1/items route."""
    out, cursor = [], ""
    while len(out) < maximum:
        body = {"pivot": pivot, "limit": 100}
        if cursor:
            body["cursor"] = cursor
        page = client.call("POST", "/v1/libraries/" + urllib.parse.quote(library_id, safe="") +
                           "/browse", body, ok=(200,))
        out.extend(page.get("entries") or [])
        cursor = (page.get("pageInfo") or {}).get("nextCursor") or ""
        if not cursor:
            break
    return out


def reading_items(client, library_id, maximum=12000):
    out, cursor = [], ""
    while len(out) < maximum:
        query = {"libraryId": library_id, "limit": "100"}
        if cursor:
            query["cursor"] = cursor
        page = client.read("/v1/reading/items?" + urllib.parse.urlencode(query))
        out.extend(page.get("items") or [])
        cursor = page.get("nextCursor") or ""
        if not cursor:
            break
    return out


def _expected_from_entries(library, entries):
    prefix = library["path"]
    kind = library["kind"]
    scoped = [entry for entry in entries if under(entry.get("path"), prefix)]
    expected = {}
    if kind == "movie":
        expected["movies"] = len({str(entry.get("title")) for entry in scoped
                                  if entry.get("kind") in ("movie", "disc")})
    elif kind in ("tv", "anime"):
        expected["shows"] = len({str(entry.get("title")) for entry in scoped
                                 if entry.get("kind") == "show"})
        expected["episodes"] = sum(1 for entry in scoped if entry.get("kind") == "episode")
        expected["seasons"] = len({str(entry.get("path")) for entry in scoped
                                   if entry.get("kind") == "season"})
    elif kind in ("book", "comic"):
        expected["items"] = len(scoped)
    elif kind == "audiobook":
        expected["books"] = len({str(entry.get("title")) for entry in scoped
                                 if entry.get("kind") == "audiobook"})
    return expected


def manifest_expectations(manifest_path, root):
    document = None
    if manifest_path and Path(manifest_path).is_file():
        document = json.loads(Path(manifest_path).read_text(encoding="utf-8"))
    libraries, entries = [], []
    if isinstance(document, dict):
        entries = [entry for entry in document.get("entries") or [] if isinstance(entry, dict)]
        if isinstance(document.get("libraries"), list):
            for entry in document["libraries"]:
                if not isinstance(entry, dict) or not entry.get("name") or not entry.get("kind"):
                    continue
                name = str(entry["name"])
                if not name.startswith("Synthetic "):
                    name = "Synthetic " + name
                path = str(entry.get("path") or entry.get("root") or
                           entry.get("directory") or "").replace("\\", "/").strip("/")
                root_prefix = str(root).replace("\\", "/").strip("/")
                if root_prefix and (path == root_prefix or path.startswith(root_prefix + "/")):
                    path = path[len(root_prefix):].strip("/")
                libraries.append({"name": name, "kind": entry["kind"], "path": path,
                                  "declared": entry})
    elif isinstance(document, list):
        entries = [entry for entry in document if isinstance(entry, dict)]
    if not libraries:
        prefixes = {}
        for entry in entries:
            path = str(entry.get("path") or "")
            parts = path.replace("\\", "/").strip("/").split("/")
            for length in range(1, len(parts)):
                prefix = "/".join(parts[:length])
                if prefix in LAYOUT:
                    prefixes[prefix] = LAYOUT[prefix]
        libraries = [{"name": "Synthetic " + prefix.split("/")[-1], "kind": kind,
                      "path": prefix, "declared": {}} for prefix, kind in prefixes.items()]
    for library in libraries:
        declared = library.get("declared") or {}
        expected = {}
        for key in ("expected", "counts"):
            if isinstance(declared.get(key), dict):
                expected.update({k: v for k, v in declared[key].items() if isinstance(v, int)})
        for key in ("items", "movies", "shows", "episodes", "seasons",
                    "artists", "albums", "songs", "books"):
            if isinstance(declared.get(key), int):
                expected[key] = declared[key]
        if not expected:
            expected = _expected_from_entries(library, entries)
        library["expected"] = expected
    return {"root": root, "libraries": libraries, "entries": entries}


def canonical_counts(client, library):
    """Actual item counts for one library from real hierarchical routes."""
    kind, library_id = library["kind"], library["id"]
    if kind == "movie":
        return {"movies": len(browse(client, library_id, "movies"))}
    if kind in ("tv", "anime"):
        return {"shows": len(browse(client, library_id, "shows")),
                "episodes": len(browse(client, library_id, "episodes"))}
    if kind == "music":
        return {"artists": len(browse(client, library_id, "artists")),
                "albums": len(browse(client, library_id, "albums")),
                "songs": len(browse(client, library_id, "songs"))}
    if kind == "audiobook":
        books = browse(client, library_id, "books")
        parts = 0
        for book in books:
            try:
                files = client.read("/v1/books/" + urllib.parse.quote(book["id"], safe="") +
                                    "/files?limit=100")
            except Refusal:
                continue
            parts += len(files.get("files") or [])
        return {"books": len(books), "parts": parts}
    if kind in ("book", "comic"):
        found = reading_items(client, library_id)
        formats = {}
        for entry in found:
            formats[entry.get("format")] = formats.get(entry.get("format"), 0) + 1
        return {"items": len(found), "formats": formats}
    return {}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default=os.environ.get("PORTICO_TEST_BASE", DEFAULT_BASE))
    parser.add_argument("--credentials", type=Path, default=DEFAULT_CREDENTIALS)
    parser.add_argument("--manifest", type=Path, default=None)
    parser.add_argument("--root", "--remote-root", default=DEFAULT_ROOT, help="server root, for display/matching")
    parser.add_argument("--report", type=Path, default=None)
    parser.add_argument("--strict-counts", action="store_true", default=True)
    parser.add_argument("--no-strict-counts", dest="strict_counts", action="store_false")
    parser.add_argument("--recordings-root",
                        default="/home/runner/work/test-server-v3/state/recordings")
    parser.add_argument("--reading", action="store_true",
                        help="open short reading sessions to read back progress/annotations")
    args = parser.parse_args(argv)

    report = {"schema": "portico-synthetic-verify/3", "base": args.base,
              "startedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "checks": [], "failures": [], "warnings": [], "libraries": {}, "coverage": {}}
    expected = manifest_expectations(args.manifest, args.root)
    report["manifest"] = {"path": str(args.manifest) if args.manifest else None,
                          "libraries": len(expected["libraries"]), "entries": len(expected["entries"])}
    credentials = private_json(args.credentials)
    owner = Client(args.base)
    saved = credentials.get("fixtureStateV3") or {}
    owner_credentials = credentials.get("owner", credentials)

    def check(name, ok, detail=None, warn=False):
        record = {"check": name, "ok": bool(ok), "detail": detail}
        report["checks"].append(record)
        if not ok:
            (report["warnings"] if warn else report["failures"]).append(record)
        return bool(ok)

    try:
        signed = sign_in(owner, owner_credentials["username"], owner_credentials["password"])
        primary = next((p for p in signed.get("profiles") or [] if p.get("primary")), None)
        if primary:
            select(owner, primary["id"])
        system = owner.read("/v1/system")
        report["server"] = {key: system.get(key) for key in ("id", "version", "commit")}
        setup = owner.read("/v1/setup/state")
        check("setup-ready", setup.get("ready") is True, setup.get("phase"))

        # Libraries, scans and canonical counts ---------------------------
        libraries = items(owner.read("/v1/libraries"))
        admin = {entry["name"]: entry for entry in items(owner.read("/v1/admin/libraries"))}
        by_name = {entry["name"]: entry for entry in libraries}
        for declared in expected["libraries"]:
            name = declared["name"]
            library = by_name.get(name)
            if library is None:
                for suffix in (" (v3)", " 2", " 3"):
                    library = by_name.get(name + suffix)
                    if library:
                        name = name + suffix
                        break
            if not check("library:" + name, library is not None):
                continue
            if not check("library-kind:" + name, library.get("kind") == declared["kind"],
                         library.get("kind")):
                continue
            inventory = owner.read("/v1/libraries/" + urllib.parse.quote(library["id"], safe="") +
                                   "/inventory-status")
            sources = (inventory.get("inventory") or {}).get("sources") or []
            check("scan-complete:" + name,
                  bool(sources) and all(s.get("status") in ("complete", "complete_with_warnings")
                                       for s in sources),
                  [s.get("status") for s in sources])
            entry = (admin.get(name) or {})
            scan = entry.get("lastScan") or {}
            check("scan-error:" + name, not scan.get("error") and not scan.get("errorCode"),
                  scan.get("error") or scan.get("errorCode"))
            try:
                settings_doc = owner.read("/v1/admin/libraries/" +
                                          urllib.parse.quote(library["id"], safe="") + "/settings")
                library_settings = settings_doc.get("settings") or {}
                expensive = {"loudness", "checksum", "audio_fingerprint", "trickplay",
                             "chapter_images", "video_preview", "subtitle_extraction",
                             "subtitle_download", "deep_stream_analysis", "lyrics", "waveform"}
                check("library-local-metadata:" + name,
                      not (set(library_settings.get("analysis") or []) & expensive) and
                      all((provider.get("provider") == "local")
                          for provider in library_settings.get("providers") or []) and
                      library_settings.get("acoustidEnabled") is False and
                      library_settings.get("segmentDetection") == "off",
                      {"analysis": library_settings.get("analysis"),
                       "providers": [p.get("provider") for p in library_settings.get("providers") or []],
                       "segmentDetection": library_settings.get("segmentDetection")})
            except Refusal as refusal:
                check("library-local-metadata:" + name, False, refusal.code)
            actual = canonical_counts(owner, library)
            expected_counts = declared.get("expected") or {}
            report["libraries"][name] = {"id": library["id"], "kind": library["kind"],
                                         "actual": actual, "expected": expected_counts,
                                         "path": declared["path"]}
            for key, wanted in expected_counts.items():
                if key == "formats" or key not in actual:
                    continue
                if args.strict_counts:
                    check(f"count:{name}:{key}", actual.get(key) == wanted,
                          {"actual": actual.get(key), "manifest": wanted})
            if library["kind"] in ("book", "comic"):
                check("reading-format:" + name, bool(actual.get("formats")), actual.get("formats"))

        # Metadata, artwork and technical coverage -------------------------
        kinds, containers, video_codecs, audio_codecs = {}, set(), set(), set()
        art_checked = without_art = 0
        for name, entry in report["libraries"].items():
            library = by_name.get(name) or {"id": entry["id"], "kind": entry["kind"]}
            if entry["kind"] in ("book", "comic"):
                found = reading_items(owner, library["id"], 12)
                for item in found:
                    item.setdefault("kind", entry["kind"])
            else:
                found, cursor = [], ""
                while len(found) < 12:
                    body = {"pivot": {"movie": "movies", "tv": "episodes", "anime": "episodes",
                                      "music": "songs", "audiobook": "books"}[entry["kind"]],
                            "limit": 12}
                    if cursor:
                        body["cursor"] = cursor
                    page = owner.call("POST", "/v1/libraries/" + urllib.parse.quote(library["id"], safe="") +
                                      "/browse", body, ok=(200,))
                    found.extend(page.get("entries") or [])
                    cursor = (page.get("pageInfo") or {}).get("nextCursor") or ""
                    if not cursor:
                        break
            for item in found:
                kinds[item.get("kind")] = kinds.get(item.get("kind"), 0) + 1
                try:
                    owner.call("GET", "/v1/items/" + urllib.parse.quote(item["id"], safe="") +
                               "/art/poster", ok=(200,))
                    art_checked += 1
                except Refusal as refusal:
                    if refusal.status == 404:
                        without_art += 1
                    else:
                        report["warnings"].append({"check": "artwork", "itemId": item["id"],
                                                   "code": refusal.code})
                try:
                    info = owner.read("/v1/items/" + urllib.parse.quote(item["id"], safe="") +
                                      "/file-info")
                    for file in info.get("files") or []:
                        if file.get("container"):
                            containers.add(str(file["container"]).lower())
                        if (file.get("video") or {}).get("codec"):
                            video_codecs.add(str(file["video"]["codec"]).lower())
                        for track in file.get("audio") or []:
                            if track.get("codec"):
                                audio_codecs.add(str(track["codec"]).lower())
                except Refusal:
                    pass
            if entry["kind"] in ("tv", "anime"):
                shows = owner.read("/v1/libraries/" + urllib.parse.quote(library["id"], safe="") +
                                   "/shows?limit=100")
                entry["shows"] = len(shows.get("shows") or [])
                if shows.get("shows"):
                    show_id = shows["shows"][0]["id"]
                    seasons = owner.read("/v1/shows/" + urllib.parse.quote(show_id, safe="") +
                                         "/seasons")
                    entry["sampleSeasons"] = len(seasons.get("seasons") or [])
                    check("seasons:" + name, entry["sampleSeasons"] > 0, entry["sampleSeasons"])
            if entry["kind"] == "audiobook":
                books = items(owner.read("/v1/libraries/" + urllib.parse.quote(library["id"], safe="") +
                                         "/books?limit=100"))
                if books:
                    files = owner.read("/v1/books/" + urllib.parse.quote(books[0]["id"], safe="") +
                                       "/files?limit=100")
                    entry["sampleBookParts"] = len(files.get("files") or [])
                    check("audiobook-parts:" + name, entry["sampleBookParts"] > 0,
                          entry["sampleBookParts"])
                    timeline = owner.read("/v1/books/" + urllib.parse.quote(books[0]["id"], safe="") +
                                          "/timeline")
                    progress = (timeline.get("data") or {}).get("progress")
                    check("audiobook-progress:" + name, progress is not None, bool(progress))
        report["coverage"] = {"kinds": kinds, "containers": sorted(containers),
                              "videoCodecs": sorted(video_codecs), "audioCodecs": sorted(audio_codecs),
                              "artworkChecked": art_checked, "artworkMissing": without_art}
        check("catalogue-nonempty", bool(report["libraries"]), sorted(report["libraries"]))
        if video_codecs:
            check("coverage-video-codecs", len(video_codecs) >= 3, sorted(video_codecs), warn=True)
        if audio_codecs:
            check("coverage-audio-codecs", len(audio_codecs) >= 3, sorted(audio_codecs), warn=True)
        if containers:
            check("coverage-containers", len(containers) >= 3, sorted(containers), warn=True)

        # Accounts, profiles and restrictions ----------------------------
        snapshot = owner.read("/v1/direct")
        profiles = snapshot.get("profiles") or []
        profile_by_name = {p.get("name"): p for p in profiles}
        kids = profile_by_name.get("Synthetic Kids")
        check("profile-kids", kids is not None)
        if kids:
            check("profile-kids-pin", kids.get("pinRequired") is True, kids.get("pinRequired"))
            restrictions = owner.read("/v1/direct/profiles/" + urllib.parse.quote(kids["id"], safe="") +
                                      "/restrictions")
            check("profile-kids-rating", restrictions.get("maximumAgeRating") == "PG",
                  restrictions.get("maximumAgeRating"))
        check("profile-guest", "Synthetic Guest" in profile_by_name)
        members = items(owner.read("/v1/direct/members"))
        member_names = {m.get("username") for m in members}
        for member in credentials.get("members") or []:
            check("member:" + member["username"], member["username"] in member_names)
        allowed = {entry["id"] for entry in libraries}
        for member in members:
            if member.get("username") in {m.get("username") for m in credentials.get("members") or []}:
                check("member-libraries:" + member["username"],
                      set(member.get("allowedLibraries") or []) >= allowed,
                      len(member.get("allowedLibraries") or []))

        # Saved state per profile ----------------------------------------
        targets = []
        owner_primary = next((p for p in profiles if p.get("primary")), None)
        owner_login = (owner_credentials["username"], owner_credentials["password"])
        if owner_primary:
            targets.append(("Owner", owner_primary["id"], None) + owner_login)
        kids_pin = ((saved.get("profiles") or {}).get(kids["id"], {}).get("pin") if kids else None)
        if kids:
            targets.append(("Kids", kids["id"], kids_pin) + owner_login)
        guest = profile_by_name.get("Synthetic Guest")
        if guest:
            targets.append(("Guest", guest["id"], None) + owner_login)
        for member in credentials.get("members") or []:
            probe = Client(args.base)
            try:
                member_signed = sign_in(probe, member["username"], member["password"])
                member_primary = next((p for p in member_signed.get("profiles") or []
                                       if p.get("primary")), None)
                if member_primary:
                    targets.append((member.get("name") or member["username"],
                                    member_primary["id"], None,
                                    member["username"], member["password"]))
            except Refusal as refusal:
                report["warnings"].append({"check": "member-sign-in", "username": member["username"],
                                           "code": refusal.code, "status": refusal.status})
        for label, profile_id, pin, username, password in targets:
            try:
                client = open_profile(args.base, username, password, profile_id, pin)
            except Refusal as refusal:
                check("profile-select:" + label, False, refusal.code)
                continue
            summary = client.read("/v1/me/saved/summary")
            my_list = int((summary.get("myList") or {}).get("posters") or 0) + \
                int((summary.get("myList") or {}).get("squares") or 0)
            state = {"profile": label, "myList": my_list, "collections": summary.get("collections"),
                     "playlists": summary.get("playlists"), "views": summary.get("savedViews")}
            report.setdefault("states", []).append(state)
            check("state-collections:" + label, int(summary.get("collections") or 0) >= 2,
                  summary.get("collections"))
            check("state-playlists:" + label, int(summary.get("playlists") or 0) >= 2,
                  summary.get("playlists"))
            check("state-views:" + label, int(summary.get("savedViews") or 0) >= 3,
                  summary.get("savedViews"))
            check("state-my-list:" + label, my_list > 0, my_list)
            history = client.read("/v1/personal-history?limit=20")
            check("state-history:" + label, len(history.get("entries") or []) > 0,
                  len(history.get("entries") or []))
            collections = items(client.read("/v1/saved-resources?kind=collection&limit=100"))
            for collection in collections:
                if collection.get("name", "").endswith("Picks"):
                    content = client.read("/v1/saved-resources/" +
                                          urllib.parse.quote(collection["id"], safe="") + "/content?limit=50")
                    visible = [entry for entry in content.get("entries") or []
                               if not entry.get("hidden") and entry.get("media")]
                    check("collection-members:" + label, len(visible) >= 3, len(visible))
            playlists = items(client.read("/v1/playlists"))
            for playlist in playlists:
                if playlist.get("name", "").endswith("Evening Queue"):
                    content = client.read("/v1/playlists/" + urllib.parse.quote(playlist["id"], safe="") +
                                          "/content?limit=50")
                    entries = sum(len(section.get("entries") or [])
                                  for section in content.get("sections") or [])
                    check("playlist-members:" + label, entries >= 3, entries)
                if playlist.get("name", "").endswith("Empty Playlist"):
                    content = client.read("/v1/playlists/" + urllib.parse.quote(playlist["id"], safe="") +
                                          "/content?limit=50")
                    entries = sum(len(section.get("entries") or [])
                                  for section in content.get("sections") or [])
                    check("playlist-empty:" + label, entries == 0, entries)
            for collection in collections:
                if collection.get("name", "").endswith("Empty Collection"):
                    content = client.read("/v1/saved-resources/" +
                                          urllib.parse.quote(collection["id"], safe="") + "/content?limit=50")
                    check("collection-empty:" + label, len(content.get("entries") or []) == 0,
                          len(content.get("entries") or []))
            views = items(client.read("/v1/saved-resources?kind=view&limit=100"))
            for view in views:
                if view.get("name", "").endswith("Empty View"):
                    content = client.read("/v1/saved-resources/" +
                                          urllib.parse.quote(view["id"], safe="") + "/content?limit=50")
                    matched = len((content.get("browse") or {}).get("entries") or [])
                    check("view-empty:" + label, matched == 0, matched)
            if args.reading:
                verify_reading(client, label, report, check)

        # Server settings -------------------------------------------------
        try:
            keys = items(owner.read("/v1/admin/api-keys"))
            check("api-keys", len(keys) >= 3, len(keys))
            webhooks = items(owner.read("/v1/admin/webhooks"))
            check("webhooks", len(webhooks) >= 2, len(webhooks))
            for webhook in webhooks:
                if not webhook.get("enabled"):
                    continue
                deliveries = items(owner.read("/v1/admin/webhooks/" +
                                              urllib.parse.quote(webhook["id"], safe="") +
                                              "/deliveries?limit=20"))
                check("webhook-delivery:" + str(webhook.get("name")),
                      any(200 <= int(entry.get("status") or 0) < 300 for entry in deliveries),
                      [{"state": entry.get("state"), "status": entry.get("status")}
                       for entry in deliveries[:3]])
            channels = owner.read("/v1/admin/library-channels").get("channels") or []
            check("library-channels", len(channels) >= 3, len(channels))
            check("library-channels-states",
                  any(not (entry.get("config") or {}).get("enabled", True) for entry in channels)
                  and any((entry.get("config") or {}).get("enabled", True) for entry in channels))
            backups = owner.read("/v1/admin/backups")
            check("backup-history", bool(backups.get("backups") or backups.get("lastBackup")))
            jobs = items(owner.read("/v1/admin/jobs"))
            check("jobs", len(jobs) > 0, len(jobs))
            inbox = owner.read("/v1/notifications/inbox?audience=profile&limit=50")
            check("notifications", len(items(inbox)) > 0, len(items(inbox)))
            root = owner.read("/v1/admin/dvr/recording-root")
            requested = root.get("requested") if isinstance(root.get("requested"), str) else ""
            report["recordingRoot"] = {"requested": requested,
                                        "effective": root.get("effective"),
                                        "pendingRestart": root.get("pendingRestart")}
            check("recording-root", requested == args.recordings_root,
                  report["recordingRoot"])
            dvr = owner.read("/v1/dvr?state=all&limit=50")
            recordings = (dvr.get("recordings") or [])
            report["recordings"] = [{"id": entry.get("id"), "state": entry.get("state"),
                                     "bytes": entry.get("bytes"), "itemId": entry.get("itemId"),
                                     "coverageStart": entry.get("coverageStart"),
                                     "coverageEnd": entry.get("coverageEnd")}
                                    for entry in recordings]
            live_expected = any(entry.get("kind") == "live-channel" for entry in expected["entries"])
            check("recording-present", bool(recordings), len(recordings), warn=not live_expected)
            if live_expected:
                check("recording-completed", any(entry.get("state") == "completed" and
                      int(entry.get("bytes") or 0) > 0 and entry.get("itemId") and
                      entry.get("coverageStart") < entry.get("coverageEnd")
                      for entry in recordings), report["recordings"])
        except Refusal as refusal:
            check("server-settings", False, refusal.code)

        # Live ------------------------------------------------------------
        live = owner.read("/v1/admin/live-sources")
        live_sources = live.get("sources") or []
        report["live"] = {"sources": [{"id": s.get("id"), "state": s.get("state"),
                                       "channels": s.get("channels"),
                                       "programmes": s.get("programmes")}
                                      for s in live_sources]}
        if live_sources:
            check("live-source", any(s.get("channels", 0) > 0 for s in live_sources),
                  report["live"])
            check("live-guide", any(s.get("programmes", 0) > 0 for s in live_sources),
                  report["live"])
            guide = owner.read("/v1/guide/sources?timezone=UTC")
            check("live-guide-sources", bool(guide.get("sources")),
                  len(guide.get("sources") or []))
        else:
            live_expected = any(isinstance(entry, dict) and entry.get("kind") == "live-channel"
                                for entry in expected["entries"])
            check("live-source", False, "no live source registered", warn=not live_expected)

        report["finishedAt"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        report["ok"] = not report["failures"]
    except Refusal as refusal:
        report["ok"] = False
        report["error"] = {"code": refusal.code, "status": refusal.status, "detail": refusal.detail}
    except Exception as error:
        report["ok"] = False
        report["error"] = {"code": "verify_shape", "type": type(error).__name__}
    finally:
        if args.report is not None:
            args.report.parent.mkdir(parents=True, exist_ok=True)
            args.report.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n",
                                   encoding="utf-8")
            os.chmod(args.report, 0o600)
        summary = {"ok": report.get("ok"), "checks": len(report["checks"]),
                   "failures": len(report["failures"]), "warnings": len(report["warnings"]),
                   "libraries": {name: entry.get("actual") for name, entry in
                                 report["libraries"].items()},
                   "playback": "run playback_smoke.py for direct/transcode qualification"}
        if report.get("error"):
            summary["error"] = report["error"]
        print(json.dumps(summary, indent=2, sort_keys=True))
    return 0 if report.get("ok") else 1


def verify_reading(client, label, report, check):
    """Open real reading sessions; prove at least one saved progress and note."""
    progress_seen, annotation_total = 0, 0
    for library in report["libraries"].values():
        if library.get("kind") not in ("book", "comic"):
            continue
        page = client.read("/v1/reading/items?" + urllib.parse.urlencode(
            {"libraryId": library["id"], "limit": 50}))
        for entry in (page.get("items") or [])[:1]:
            try:
                opened = client.call("POST", "/v1/reading/sessions",
                                     {"itemId": entry["id"], "sourceId": entry.get("sourceId", "")})
            except Refusal as refusal:
                report["warnings"].append({"check": "reading-session", "itemId": entry["id"],
                                           "code": refusal.code})
                continue
            session_id = opened.get("sessionId") or opened.get("id")
            if not session_id:
                continue
            quote = urllib.parse.quote(session_id, safe="")
            headers = {"If-Match": '"' + opened["revision"] + '"'}
            try:
                progress = client.read(f"/v1/reading/sessions/{quote}/progress", extra_headers=headers)
                if progress.get("progress") is not None:
                    progress_seen += 1
                annotations = client.read(f"/v1/reading/sessions/{quote}/annotations", extra_headers=headers)
                annotation_total += len(annotations.get("entries") or [])
            except Refusal as refusal:
                report["warnings"].append({"check": "reading-read", "itemId": entry["id"],
                                           "code": refusal.code})
            finally:
                try:
                    client.call("DELETE", f"/v1/reading/sessions/{quote}", ok=(204,))
                except Refusal:
                    pass
    check("reading-progress:" + label, progress_seen > 0, progress_seen)
    check("reading-annotations:" + label, annotation_total > 0, annotation_total)


def generate(root: Path, ffmpeg: str, full: bool = True) -> list[dict]:
    """Verification is not a media producer; import-safe for the driver."""
    del root, ffmpeg, full
    return []


if __name__ == "__main__":
    raise SystemExit(main())
