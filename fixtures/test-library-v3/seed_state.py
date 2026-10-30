#!/usr/bin/env python3
"""Populate a fresh Portico test server from the generated synthetic library.

This module owns *state*, not media.  The media generators in this directory
write into ``root`` and describe themselves in ``root/manifest.json``.  This
script reads that manifest (or a fixed directory layout when no manifest exists),
creates the matching libraries through the ordinary v1 API, scans them, then
creates users, profiles and saved state with real API calls only.  It never
writes the database and never fetches online metadata.

Expected generated layout (every directory optional)::

    Movies/        movie       TV/        tv          Anime/      anime
    Music/          music      Audiobooks/ audiobook   Books/      book
    Comics/         comic      Magazines/  book        Live/       (M3U/XMLTV/HLS)

``--manifest`` names a locally downloaded ``manifest.json``.  The seeder runs on
mini-1 against a tunnel, so the library root is a *server* path: it is sent to
the API verbatim and never probed with the local filesystem.  A manifest may
declare libraries explicitly::

    {"libraries": [{"name": "Synthetic Movies", "kind": "movie",
                    "path": "Movies"}], "entries": [...]}

or only entries; the top-level directory of each entry then selects the known
library.  Without a manifest, a local ``--root`` directory must exist and the
fixed layout is used (useful for a same-host rehearsal).  Live TV is registered
from a URL (``--live-base`` or a manifest ``live`` object), never from a local
M3U read.

Names are forced to the ``Synthetic `` prefix so they cannot collide with a
pre-existing catalogue.  Everything is additive: existing libraries, members
and resources are left alone.  Generated media is never written to this repo.

Usage::

    python3 seed_state.py --root /home/runner/portico-test-library-v3 \
        --manifest /home/justin/.private/walks/test-library-v3-manifest.json
    python3 seed_state.py --root ... --manifest ... --rescan --history 3
    python3 seed_state.py --root ... --manifest ... --no-bootstrap --report /tmp/seed.json

The owner and members in ``.private/walks/credentials.json`` are reused.  A
fresh server is bootstrapped through ``/v1/setup/browser`` -> ``/v1/setup`` ->
``/v1/setup/finish`` exactly as the app does, then the same credentials sign in.
New accounts, profile PINs, API-key secrets and webhook secrets are written
back to that 0600 file; nothing secret is printed.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import secrets
import stat
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

HERE = Path(__file__).resolve().parent
if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))

MAX_BODY = 16 * 1024 * 1024
PREFIX = "Synthetic "
DEFAULT_BASE = "http://127.0.0.1:49300"
DEFAULT_SERVER_ROOT = "/home/runner/portico-test-library-v3"
DEFAULT_CREDENTIALS = Path("/home/justin/projects/Portico/.private/walks/credentials.json")
LAYOUT = [
    ("Movies", "movie"), ("TV", "tv"), ("Anime", "anime"), ("Music", "music"),
    ("Audiobooks", "audiobook"), ("Reading/Books", "book"), ("Books", "book"),
    ("Reading/Comics", "comic"), ("Comics", "comic"),
    ("Reading/Magazines", "book"), ("Magazines", "book"),
    ("Reading/Newspapers", "book"), ("Reading/Mixed", "book"),
    ("Reading/Large Books", "book"),
]
TERMINAL_SCAN = {"complete", "complete_with_warnings", "completed", "failed", "cancelled"}
TERMINAL_JOB = {"complete", "completed", "failed", "cancelled", "succeeded", "success"}
PLAYABLE = {"movie", "episode", "song", "audiobook_file", "part"}
OPERATION = re.compile(r"[A-Za-z0-9._~-]{8,128}")

# Playback Protocol v1 (P2-20). A client that starts playback must declare the
# observation protocol; the server answers 400 client_update_required without
# it (playbackv1.PlaybackProtocol in errors.go, requireObservationProtocol).
PLAYBACK_PROTOCOL = {"Portico-Playback": "2"}
# The claim returns a 22-character base64url epoch (playbackv1.newEpoch); every
# sample must carry it verbatim. These are the wire shapes the checked-in
# client-core SessionsClient accepts, not guesses.
PLAYBACK_EPOCH = re.compile(r"[A-Za-z0-9_-]{22}")
PLAYBACK_STATES = frozenset({"playing", "paused", "buffering", "ended", "error"})
PLAYBACK_TIMESTAMP = re.compile(
    r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})")


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


def op_id(label):
    value = "seed-v3-" + label + "-" + uuid.uuid4().hex
    assert OPERATION.fullmatch(value), value
    return value


def opaque(label):
    return hashlib.sha256(("synthetic-v3:" + label).encode()).hexdigest()[:48]


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def private_json(path):
    st = path.lstat()
    if not stat.S_ISREG(st.st_mode) or st.st_uid != os.getuid() or stat.S_IMODE(st.st_mode) != 0o600:
        raise SystemExit("credentials file must be a private 0600 file owned by this user")
    return json.loads(path.read_text(encoding="utf-8"))


def write_private(path, document):
    """Replace the credentials file atomically, preserving its 0600 mode."""
    path = Path(path)
    handle, temporary = tempfile.mkstemp(prefix=".credentials-", dir=str(path.parent))
    try:
        os.fchmod(handle, 0o600)
        with os.fdopen(handle, "w", encoding="utf-8") as stream:
            json.dump(document, stream, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        os.chmod(path, 0o600)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


class Client:
    def __init__(self, base, installation=None, token=""):
        self.base = base.rstrip("/")
        self.installation = installation or str(uuid.uuid4())
        self.token = token
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(self, method, path, body=None, raw=None, content_type="application/json",
             ok=(200, 201, 202, 204), timeout=60, extra_headers=None):
        headers = {"Accept": "application/json", "Origin": self.base,
                   "X-Portico-Installation-Id": self.installation}
        data = None
        if raw is not None:
            data = raw
            headers["Content-Type"] = content_type
        elif body is not None:
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
        except Exception as error:  # transport failure, never retried by accident
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
                detail = payload.decode("utf-8", "replace")[:4000] if payload else ""
                raise Refusal(code or "http_" + str(response.status), response.status, detail)
            if not payload:
                return None
            if response.headers.get("Content-Type", "").split(";")[0] in (
                    "image/png", "image/jpeg", "application/octet-stream"):
                return payload
            try:
                document = json.loads(payload)
                if isinstance(document, dict) and {"protocolVersion", "serverId", "result"} <= document.keys():
                    return document["result"]
                return document
            except ValueError:
                return payload

    def read(self, path, attempts=12, timeout=60, extra_headers=None):
        """A read that tolerates a catalogue fence while a scan publishes."""
        last = None
        for attempt in range(attempts):
            try:
                return self.call("GET", path, timeout=timeout, extra_headers=extra_headers)
            except Refusal as refusal:
                last = refusal
                if refusal.status not in (409, 412, 423, 503, 504) or attempt == attempts - 1:
                    raise
                time.sleep(min(4.0, 0.5 * (attempt + 1)))
        raise last


def items(document):
    if isinstance(document, list):
        return document
    if not isinstance(document, dict):
        return []
    for key in ("items", "sections", "resources", "playlists", "channels", "sources", "backups",
                "entries", "results", "profiles"):
        if key in document:
            value = document[key]
            if key == "sections":
                return [entry for section in value for entry in section.get("entries", [])]
            if isinstance(value, list):
                return value
    return []


def sign_in(client, username, password):
    """Sign-in is a short write; the scan queue can answer 503 while busy."""
    last = None
    for attempt in range(8):
        try:
            signed = client.call("POST", "/v1/direct/sign-in",
                                 {"username": username, "password": password}, ok=(200,))
            session = signed.get("session", signed)
            client.token = session["accessToken"]
            return signed, session["accessToken"]
        except Refusal as refusal:
            last = refusal
            if refusal.status not in (429, 503, 504) or attempt == 7:
                raise
            time.sleep(min(20, 3 * (attempt + 1)))
    raise last


def select_profile(client, profile_id, pin=None):
    body = {"installationId": client.installation, "trust": False}
    if pin is not None:
        body["pin"] = pin
    last = None
    for attempt in range(8):
        try:
            selected = client.call(
                "POST", "/v1/direct/profiles/" + urllib.parse.quote(profile_id, safe="") + "/select",
                body, ok=(200,))
            return selected["session"]["accessToken"]
        except Refusal as refusal:
            last = refusal
            if refusal.status not in (409, 429, 503, 504) or attempt == 7:
                raise
            time.sleep(min(15, 1.5 * (attempt + 1)))
    raise last


def open_profile(base, username, password, profile_id, pin=None):
    """Fresh installation, ordinary sign-in, then the target profile."""
    last = None
    for attempt in range(5):
        client = Client(base, str(uuid.uuid4()))
        try:
            _, token = sign_in(client, username, password)
            client.token = token
            client.token = select_profile(client, profile_id, pin)
            return client
        except Refusal as refusal:
            last = refusal
            if refusal.status not in (409, 429, 503, 504) or attempt == 4:
                raise
            time.sleep(min(20, 3 * (attempt + 1)))
    raise last


def bootstrap_setup(client, owner):
    """The ordinary first-run flow.  Never hosted, never e-mail."""
    browser = client.call("POST", "/v1/setup/browser", {}, ok=(200,))
    token = browser["setupToken"]
    created = client.call("POST", "/v1/setup", {
        "setupToken": token,
        "requestId": str(uuid.uuid4()),
        "name": "Synthetic Test Server",
        "authMode": "local",
        "interactive": True,
        "username": owner["username"],
        "password": owner["password"],
    }, ok=(201,))
    session = created.get("session", created)
    client.token = session["accessToken"]
    state = client.read("/v1/setup/state")
    if state.get("ready"):
        return session
    client.call("POST", "/v1/setup/finish", {"revision": state["revision"]}, ok=(200,))
    return session


def _under(path, prefix):
    path = str(path or "").replace("\\", "/").strip("/")
    prefix = str(prefix or "").replace("\\", "/").strip("/")
    return bool(prefix) and (path == prefix or path.startswith(prefix + "/"))


def _server_path(root, value):
    """Join a manifest path to the server root without touching this machine."""
    import posixpath
    if value.startswith("/"):
        return posixpath.normpath(value)
    return posixpath.normpath(posixpath.join(root, value))


def manifest_libraries(root, manifest_path=None):
    """Libraries from a local manifest, or from a local layout rehearsal.

    The returned paths are server paths.  Nothing here calls ``Path.is_dir`` on
    the server root, because the seeder commonly runs on mini-1 over a tunnel.
    """
    manifest_path = Path(manifest_path) if manifest_path else Path(root) / "manifest.json"
    document = None
    if manifest_path.is_file():
        try:
            document = json.loads(manifest_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            document = None
    declared, inferred = [], {}
    if isinstance(document, dict):
        raw = document.get("libraries")
        if isinstance(raw, list):
            for entry in raw:
                if not isinstance(entry, dict):
                    continue
                name, kind = entry.get("name"), entry.get("kind")
                path = entry.get("path") or entry.get("directory")
                if isinstance(name, str) and isinstance(kind, str) and isinstance(path, str):
                    declared.append((name, kind, path))
        entries = document.get("entries")
        if isinstance(entries, list):
            for entry in entries:
                if not isinstance(entry, dict) or not isinstance(entry.get("path"), str):
                    continue
                first = entry["path"].replace("\\", "/").lstrip("/").split("/")[0]
                inferred[first] = inferred.get(first, 0) + 1
    elif isinstance(document, list):
        for entry in document:
            if isinstance(entry, dict) and isinstance(entry.get("path"), str):
                first = entry["path"].replace("\\", "/").lstrip("/").split("/")[0]
                inferred[first] = inferred.get(first, 0) + 1
    if not declared and not inferred and not manifest_path.is_file() and Path(root).is_dir():
        declared = [(PREFIX + directory, kind, directory) for directory, kind in LAYOUT
                    if (Path(root) / directory).is_dir()]
    if not declared:
        known = sorted(LAYOUT, key=lambda pair: len(pair[0]), reverse=True)
        matched = {}
        for entry in (document.get("entries") if isinstance(document, dict) else document) or []:
            if not isinstance(entry, dict) or not isinstance(entry.get("path"), str):
                continue
            for prefix, kind in known:
                if _under(entry["path"], prefix):
                    matched[prefix] = kind
                    break
        for directory in sorted(matched, key=lambda value: (value.count("/"), value)):
            declared.append((PREFIX + directory.split("/")[-1], matched[directory], directory))
    out = []
    for name, kind, path in declared:
        if not name.startswith(PREFIX):
            name = PREFIX + name
        out.append((name, kind, _server_path(root, path)))
    return out


def library_paths(settings):
    import posixpath
    sources = settings.get("sources") if isinstance(settings, dict) else None
    return {posixpath.normpath(str(source.get("path") or source.get("resolvedPath") or ""))
            for source in (sources or []) if isinstance(source, dict) and
            (source.get("path") or source.get("resolvedPath"))}


def ensure_library(owner, name, kind, desired, report):
    import posixpath
    desired = posixpath.normpath(desired)
    existing = {entry["name"]: entry for entry in items(owner.read("/v1/admin/libraries"))}
    for suffix in (None, " (v3)", " 2", " 3"):
        candidate = name if suffix is None else name + suffix
        library = existing.get(candidate)
        if library is None:
            continue
        settings = owner.read("/v1/admin/libraries/" + urllib.parse.quote(library["id"], safe="") + "/settings")
        paths = library_paths(settings)
        if desired in paths or not paths:
            report["libraries"][candidate] = {"id": library["id"], "kind": kind, "path": desired,
                                              "created": False, "name": candidate}
            return candidate, library["id"]
    library = None
    for suffix in (None, " (v3)", " 2", " 3"):
        candidate = name if suffix is None else name + suffix
        if candidate in existing:
            continue
        try:
            raw = owner.call("POST", "/v1/libraries", {
                "name": candidate, "kind": kind, "path": desired, "metadataAgent": "local",
            }, ok=(200, 201))
        except Refusal as refusal:
            if refusal.status == 409 or refusal.code in ("library_name_conflict", "invalid_request"):
                continue
            raise
        library = raw.get("library", raw)
        report["libraries"][candidate] = {"id": library["id"], "kind": kind, "path": desired,
                                          "created": True, "name": candidate}
        return candidate, library["id"]
    raise Refusal("library_name_exhausted", detail=name)


def quiet_library(owner, name, library_id, report):
    """Disable every online/enrichment/analysis option this library permits."""
    path = "/v1/admin/libraries/" + urllib.parse.quote(library_id, safe="") + "/settings"
    for attempt in range(3):
        document = owner.read(path)
        settings = dict(document.get("settings") or {})
        defaults = dict(document.get("defaults") or {})
        for key, value in defaults.items():
            settings.setdefault(key, value)
        settings["providers"] = [provider for provider in settings.get("providers") or []
                                 if provider.get("provider") == "local"]
        settings["acoustidEnabled"] = False
        settings["lyricsSource"] = "files"
        settings["localTags"] = "prefer"
        settings["segmentDetection"] = "off"
        settings["monitorChanges"] = False
        settings["allowMediaDeletion"] = False
        matrix = document.get("analysisMatrix") or {}
        operations = matrix.get("operations") if isinstance(matrix, dict) else None
        kind = document.get("libraryKind") or report.get("libraries", {}).get(name, {}).get("kind")
        media_kinds = {"tv": {"show", "episode"}, "anime": {"show", "episode"},
                       "movie": {"movie"}, "music": {"music"}, "book": {"book"},
                       "comic": {"book"}, "audiobook": {"book"}}.get(kind, {kind})
        allowed = []
        if isinstance(operations, list):
            for operation in operations:
                if not isinstance(operation, dict):
                    continue
                if operation.get("costClass") != "metadata-only" or operation.get("network"):
                    continue
                kinds = set(operation.get("mediaKinds") or [])
                if kinds and not (media_kinds & kinds):
                    continue
                identifier = operation.get("id")
                if identifier:
                    allowed.append(identifier)
        settings["analysis"] = list(dict.fromkeys(
            ["probe", "local_metadata"] + allowed))
        body = {"expectedRevision": document.get("revision", 0), "operationId": op_id("lib-settings"),
                "settings": settings}
        try:
            owner.call("PUT", path, body, ok=(200,))
            actual = owner.read(path)
            saved = actual.get("settings") or {}
            wanted = set(settings["analysis"])
            if (set(saved.get("analysis") or []) != wanted or
                    any(provider.get("provider") != "local" for provider in saved.get("providers") or []) or
                    saved.get("acoustidEnabled") or saved.get("segmentDetection") != "off"):
                report.setdefault("failures", []).append(
                    {"step": "library-settings-readback", "library": name,
                     "libraryId": library_id,
                     "actual": {"analysis": saved.get("analysis"),
                                "providers": saved.get("providers"),
                                "acoustidEnabled": saved.get("acoustidEnabled"),
                                "segmentDetection": saved.get("segmentDetection")}})
            return
        except Refusal as refusal:
            if refusal.status in (409, 412) and attempt < 2:
                time.sleep(0.75 * (attempt + 1))
                continue
            report.setdefault("failures", []).append(
                {"step": "library-settings", "library": name, "libraryId": library_id,
                 "code": refusal.code, "status": refusal.status})
            return


def scan_status(owner, library_id):
    document = owner.read("/v1/libraries/" + urllib.parse.quote(library_id, safe="") + "/inventory-status")
    sources = ((document or {}).get("inventory") or {}).get("sources") or []
    return sources


def wait_scan(owner, library_id, timeout, report, label):
    deadline = time.monotonic() + timeout
    last = []
    while time.monotonic() < deadline:
        last = scan_status(owner, library_id)
        if last and all((source.get("status") in TERMINAL_SCAN) for source in last):
            break
        if not last:
            admin = owner.read("/v1/admin/libraries/" + urllib.parse.quote(library_id, safe=""))
            library = admin.get("library", admin)
            scan = library.get("lastScan")
            if isinstance(scan, dict) and scan.get("status") in TERMINAL_SCAN:
                last = [scan]
                break
        time.sleep(2)
    terminal = bool(last) and all(source.get("status") in TERMINAL_SCAN for source in last)
    failed = any(source.get("status") in ("failed", "cancelled") for source in last)
    report["scans"].append({"library": label, "terminal": terminal, "failed": failed,
                            "sources": [{"status": s.get("status"), "discovered": s.get("discovered"),
                                         "analyzed": s.get("analyzed"), "warnings": s.get("warnings")}
                                        for s in last]})
    if not terminal:
        raise Refusal("scan_timeout", detail=label)
    if failed:
        raise Refusal("scan_failed", detail=label)
    return last


def browse(client, library_id, pivot, maximum=25000):
    """Page the real POST /v1/libraries/{id}/browse surface."""
    out, cursor = [], ""
    while len(out) < maximum:
        body = {"pivot": pivot, "limit": 100}
        if cursor:
            body["cursor"] = cursor
        try:
            page = client.call("POST", "/v1/libraries/" + urllib.parse.quote(library_id, safe="") +
                               "/browse", body, ok=(200,))
        except Refusal as refusal:
            raise Refusal("browse_" + pivot + "_" + refusal.code, refusal.status, refusal.detail)
        out.extend(page.get("entries") or [])
        cursor = (page.get("pageInfo") or {}).get("nextCursor") or ""
        if not cursor:
            break
    return out


def library_items(client, library, maximum=25000):
    """Canonical leaf entries for one library, by its real browse pivot."""
    kind = library.get("kind")
    library_id = library.get("id")
    if kind == "movie":
        return browse(client, library_id, "movies", maximum)
    if kind in ("tv", "anime"):
        return browse(client, library_id, "episodes", maximum)
    if kind == "music":
        return browse(client, library_id, "songs", maximum)
    if kind == "audiobook":
        books = browse(client, library_id, "books", 200)
        parts = []
        for book in books[:48]:
            try:
                page = client.read("/v1/books/" + urllib.parse.quote(book["id"], safe="") +
                                   "/files?limit=100")
            except Refusal:
                continue
            for part in page.get("files") or []:
                part.setdefault("kind", "audiobook_file")
                parts.append(part)
        return parts
    if kind in ("book", "comic"):
        return reading_entries(client, library_id, maximum)
    return []


def ensure_member(owner, username, password, name, library_ids, report):
    current = {entry.get("username"): entry for entry in items(owner.read("/v1/direct/members"))}
    if username in current:
        return current[username]
    raw = owner.call("POST", "/v1/direct/members", {
        "username": username, "password": password, "name": name,
        "allowedLibraries": list(dict.fromkeys(library_ids)),
    }, ok=(201,))
    report["created"]["members"].append(username)
    return raw


def ensure_profile(owner, name, art, report):
    snapshot = owner.read("/v1/direct")
    for profile in snapshot.get("profiles") or []:
        if profile.get("name") == name:
            return profile
    raw = owner.call("POST", "/v1/direct/profiles", {"name": name, "art": art}, ok=(201,))
    report["created"]["profiles"].append(name)
    snapshot = raw
    return next(p for p in snapshot.get("profiles") or [] if p.get("name") == name)


def _avatar_png(title, size=512):
    """A deterministic original PNG avatar without Pillow (mini-1 has no venv)."""
    import struct, zlib
    digest = hashlib.sha256(("avatar:" + title).encode()).digest()
    bg = tuple(24 + value % 48 for value in digest[:3])
    accent = tuple(120 + value % 110 for value in digest[3:6])
    cx, cy, radius = size * (0.34 + digest[6] / 900), size * 0.52, size * (0.13 + digest[7] / 1000)
    rows = []
    for y in range(size):
        row = bytearray([0])
        for x in range(size):
            dx, dy = x - cx, y - cy
            colour = accent if dx * dx + dy * dy < radius * radius else bg
            if (x // 32 + y // 32) % 9 == 0:
                colour = tuple(min(255, c + 12) for c in colour)
            row.extend(colour)
        rows.append(bytes(row))
    raw = b"".join(rows)
    def chunk(kind, payload):
        return (struct.pack(">I", len(payload)) + kind + payload +
                struct.pack(">I", zlib.crc32(kind + payload) & 0xffffffff))
    header = struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) +
            chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))


def upload_avatar(owner, profile_id, title, report):
    try:
        existing = items(owner.read("/v1/direct/profiles/avatars"))
        if any(entry.get("profileId") == profile_id for entry in existing):
            return
        owner.call("POST", "/v1/direct/profiles/" + urllib.parse.quote(profile_id, safe="") + "/avatar",
                   raw=_avatar_png(title), content_type="image/png", ok=(201,))
        report["created"]["avatars"].append(title)
    except Exception as error:  # an avatar is cosmetic; never stop the state build for it
        report.setdefault("warnings", []).append({"step": "avatar", "profile": title,
                                                   "error": type(error).__name__})


def current_revision(client, item_id):
    detail = client.read("/v1/items/" + urllib.parse.quote(item_id, safe=""))
    return int(((detail.get("personal") or {}).get("revision")) or 0)


def set_personal(client, item_id, field, value, report):
    revision = current_revision(client, item_id)
    body = {"operationId": op_id("personal-" + field), "expectedRevision": revision}
    body[field] = value
    if field == "progressMs":
        # Progress is only accepted beside an explicit un-watched action.
        body = {"operationId": op_id("progress"), "expectedRevision": revision,
                "watched": False, "progressMs": int(value)}
    try:
        return client.call("PUT", "/v1/items/" + urllib.parse.quote(item_id, safe="") + "/personal-state",
                           body, ok=(200,))
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "personal", "itemId": item_id,
                                                   "field": field, "code": refusal.code,
                                                   "status": refusal.status})
        return None


def batch_flags(client, pairs, report):
    """pairs: [(item_id, {watchlisted: True}), ...] in one real batch call."""
    if not pairs:
        return {}
    body = {"operationId": op_id("batch"), "items": [
        {"itemId": item_id, **fields} for item_id, fields in pairs]}
    try:
        result = client.call("PUT", "/v1/items/personal-state:batch", body, ok=(200,))
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "personal-batch", "code": refusal.code,
                                                   "status": refusal.status})
        return {}
    revisions = {}
    for entry in result.get("results") or []:
        personal = entry.get("personal") or {}
        if entry.get("ok") and personal.get("revision") is not None:
            revisions[entry["itemId"]] = int(personal["revision"])
    return revisions


def session_path(session_id):
    return "/v1/playback/sessions/" + urllib.parse.quote(session_id, safe="")


def claim_observer(client, session_id, generation):
    """Claim a session's observations; return the grant every sample carries.

    ``POST /v1/playback/sessions/{id}/observer`` is the only supported claim
    (playbackv1.ClaimObserver): the session must be this device's, and for a
    title the generation must be the presentation's. The grant is required by
    the current contract to carry an epoch, everyMs and leaseUntil.
    """
    body = {"generation": int(generation)}
    grant = client.call("POST", session_path(session_id) + "/observer", body,
                        ok=(200,), extra_headers=PLAYBACK_PROTOCOL)
    if not isinstance(grant, dict):
        raise Refusal("observer_grant_shape", 200, "document")
    epoch, every, lease = grant.get("epoch"), grant.get("everyMs"), grant.get("leaseUntil")
    if not isinstance(epoch, str) or not PLAYBACK_EPOCH.fullmatch(epoch):
        raise Refusal("observer_grant_shape", 200, "epoch")
    if not isinstance(every, int) or isinstance(every, bool) or every < 1:
        raise Refusal("observer_grant_shape", 200, "everyMs")
    if not isinstance(lease, str) or not PLAYBACK_TIMESTAMP.fullmatch(lease):
        raise Refusal("observer_grant_shape", 200, "leaseUntil")
    return grant


def observe_sample(client, session_id, sample):
    """Send one already-built sample and require the current acknowledgement.

    ``PUT .../observation`` answers 200 with acceptedSeq and leaseUntil
    (playbackv1.ObservationAck). A refused or malformed answer is an error,
    never a fabricated success.
    """
    ack = client.call("PUT", session_path(session_id) + "/observation", sample,
                      ok=(200,), extra_headers=PLAYBACK_PROTOCOL)
    if not isinstance(ack, dict):
        raise Refusal("observation_ack_shape", 200, "document")
    accepted, lease = ack.get("acceptedSeq"), ack.get("leaseUntil")
    if not isinstance(accepted, int) or isinstance(accepted, bool) or accepted < sample["seq"]:
        raise Refusal("observation_ack_shape", 200, "acceptedSeq")
    if not isinstance(lease, str) or not PLAYBACK_TIMESTAMP.fullmatch(lease):
        raise Refusal("observation_ack_shape", 200, "leaseUntil")
    return ack


class Observer:
    """The minimal session owner the fixture needs.

    Claim once, then report the newest reading: sequence numbers rise from one,
    the play clock is wall time actually spent playing, a seek moves the seek
    serial, and an accepted end stops reporting. This is deliberately not the
    client reporter's timing/retry runtime: a refused sample is an error here,
    not silently requeued or reclaimed.
    """

    def __init__(self, client, session_id, generation, now=time.monotonic):
        self.client = client
        self.session_id = session_id
        self.generation = int(generation)
        self.now = now
        self.epoch = None
        self.grant = None
        self.claimed_at = 0.0
        self.seq = 0
        self.seek_serial = 0
        self.play_clock = 0.0
        self.playing_since = None
        self.ended = False

    def claim(self):
        if self.ended:
            raise Refusal("observer_ended")
        grant = claim_observer(self.client, self.session_id, self.generation)
        self.epoch, self.grant = grant["epoch"], grant
        self.claimed_at = self.now()
        self.seq = 0
        self.play_clock = 0.0
        self.playing_since = None
        return grant

    def observe(self, state, position_ms, *, rate=1.0, seeked=False,
                buffered_ms=None, bandwidth_kbps=None, dropped_frames=None,
                observed_playback=None):
        if state not in PLAYBACK_STATES:
            raise Refusal("observation_state", detail=state)
        if self.ended:
            raise Refusal("observer_ended")
        if not isinstance(rate, (int, float)) or isinstance(rate, bool) or not 0 <= rate <= 4:
            raise Refusal("observation_rate", detail=str(rate))
        if self.epoch is None:
            self.claim()
        now = self.now()
        if self.playing_since is not None:
            self.play_clock += max(0.0, now - self.playing_since)
        if seeked:
            self.seek_serial += 1
        monotonic = max(0, int(round((now - self.claimed_at) * 1000)))
        play_clock = max(0, min(int(round(self.play_clock * 1000)), monotonic))
        self.playing_since = now if state == "playing" else None
        self.seq += 1
        sample = {"epoch": self.epoch, "state": state, "seq": self.seq,
                  "generation": self.generation, "positionMs": max(0, int(position_ms)),
                  "monotonicMs": monotonic, "playClockMs": play_clock,
                  "seekSerial": self.seek_serial, "rate": rate}
        for key, value in (("bufferedMs", buffered_ms), ("bandwidthKbps", bandwidth_kbps),
                           ("droppedFrames", dropped_frames)):
            if value is not None:
                sample[key] = max(0, int(value))
        if observed_playback is not None:
            sample["observedPlayback"] = observed_playback
        ack = observe_sample(self.client, self.session_id, sample)
        if state == "ended":
            self.ended = True
        return ack


def stop_session(client, session_id, position_ms=None):
    """Stop a session; with a position, the server drains the latest sample."""
    body = None if position_ms is None else {"positionMs": max(0, int(position_ms))}
    client.call("DELETE", session_path(session_id), body, ok=(204,),
                extra_headers=PLAYBACK_PROTOCOL)


def start_history(client, item_id, fraction, report, duration_ms=None):
    """Create a real, bounded playback history fact for a light source."""
    from playback_capabilities import capabilities
    session_id = None
    position = 0
    try:
        if not getattr(client, "playback_registered", False):
            client.call("PUT", "/v1/me/devices/current/capabilities", capabilities(), ok=(204,))
            client.playback_registered = True
        duration_ms = int(duration_ms or 0)
        if duration_ms <= 0:
            raise Refusal("history_duration_missing")
        key = op_id("play")
        deadline = time.monotonic() + 45
        while True:
            session = client.call("POST", "/v1/playback/sessions",
                                  {"itemId": item_id, "startFrom": "beginning"},
                                  extra_headers={"Idempotency-Key": key, **PLAYBACK_PROTOCOL},
                                  ok=(201, 202))
            session_id = session.get("id")
            presentation = session.get("presentation") or {}
            if presentation.get("url") and not session.get("retryAfterMs"):
                break
            if time.monotonic() >= deadline:
                raise Refusal("history_preparing_timeout")
            time.sleep(1)
        if not isinstance(session_id, str) or not session_id:
            raise Refusal("history_session_shape", detail="id")
        try:
            generation = int(presentation["generation"])
        except (KeyError, TypeError, ValueError):
            raise Refusal("history_session_shape", detail="generation")
        # A paused sample at a real partial position is a history fact; no play
        # time is claimed, and the server writes it when Stop drains the sample.
        position = max(1, min(duration_ms - 1, int(duration_ms * fraction)))
        Observer(client, session_id, generation).observe("paused", position, rate=1.0)
        stop_session(client, session_id, position)
        session_id = None
        report["created"]["history"].append(item_id)
        return True
    except Refusal as refusal:
        report.setdefault("failures", []).append({"step": "history", "itemId": item_id,
                                                  "code": refusal.code, "status": refusal.status})
        return False
    finally:
        if session_id:
            try:
                stop_session(client, session_id, position)
            except Refusal as refusal:
                report.setdefault("failures", []).append({"step": "history-stop",
                                                          "code": refusal.code})


def light_history_movies(movies, manifest_path):
    document = json.loads(Path(manifest_path or HERE / "manifest.json").read_text())
    versions = {}
    for entry in document.get("entries", []):
        if entry.get("kind") == "movie":
            versions.setdefault(entry["title"], []).append(entry)
    titles = {title for title, entries in versions.items()
              if all("transcode_source:light" in entry.get("features", []) for entry in entries)}
    return [item for item in movies if item.get("title") in titles]


def saved_resources(client, kind, maximum=400, report=None):
    """Page GET /v1/saved-resources inside the server's actual request contract.

    The handler refuses any query key outside cursor/limit/kind/pinned/shape and
    any limit outside 1..100 with 400 invalid_cursor. Its continuation is signed
    over kind, shape, pinned and limit, so each page repeats the same filters
    and limit and follows the top-level nextCursor.

    A repeated cursor or the caller's ``maximum`` ending the walk is recorded
    as a warning when ``report`` is given: it is a bounded stop, but silently
    dropping the rest of a window could hide an incomplete seed.
    """
    out, cursor, seen = [], "", set()
    repeated = truncated = False
    while len(out) < maximum:
        query = {"kind": kind, "limit": "100"}
        if cursor:
            query["cursor"] = cursor
        page = client.read("/v1/saved-resources?" + urllib.parse.urlencode(query))
        out.extend(page.get("resources") or [])
        following = page.get("nextCursor") or ""
        if not following:
            break
        if following in seen:
            repeated = True
            break
        if len(out) >= maximum:
            truncated = True
            break
        seen.add(following)
        cursor = following
    if report is not None:
        if repeated:
            report.setdefault("warnings", []).append(
                {"step": "saved-resources", "kind": kind, "code": "cursor_repeated",
                 "collected": len(out)})
        if truncated:
            report.setdefault("warnings", []).append(
                {"step": "saved-resources", "kind": kind, "code": "cursor_truncated",
                 "maximum": maximum, "collected": len(out)})
    return out[:maximum]


def ensure_collection(client, name, item_ids, report):
    for resource in saved_resources(client, "collection", report=report):
        if resource.get("name") == name:
            return resource
    body = {"operationId": op_id("collection"), "expectedRevision": 0, "kind": "collection",
            "name": name, "visibility": "private"}
    try:
        raw = client.call("POST", "/v1/saved-resources", body, ok=(200,))
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "collection", "name": name,
                                                   "code": refusal.code, "status": refusal.status})
        return None
    receipt = raw.get("receipt") or raw
    resource_id = receipt.get("resourceId")
    if item_ids and resource_id:
        revision = int(receipt.get("revision") or 0)
        try:
            raw = client.call("PUT", "/v1/saved-resources/" + urllib.parse.quote(resource_id, safe="") + "/entries",
                              {"operationId": op_id("collection-entries"), "expectedRevision": revision,
                               "addItemIds": list(item_ids)}, ok=(200,))
            receipt = raw.get("receipt") or raw
        except Refusal as refusal:
            report.setdefault("warnings", []).append({"step": "collection-entries", "name": name,
                                                       "code": refusal.code, "status": refusal.status})
    report["created"]["collections"].append({"name": name, "items": len(item_ids),
                                             "id": resource_id})
    return receipt


def ensure_playlist(client, name, item_ids, report):
    for playlist in items(client.read("/v1/playlists")):
        if playlist.get("name") == name:
            return playlist
    body = {"operationId": op_id("playlist-create"), "name": name, "itemIds": list(item_ids)}
    try:
        receipt = client.call("POST", "/v1/playlists", body, ok=(200,))
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "playlist", "name": name,
                                                   "code": refusal.code, "status": refusal.status})
        return None
    report["created"]["playlists"].append({"name": name, "items": len(item_ids),
                                           "id": receipt.get("playlistId")})
    return receipt


def ensure_view(client, name, definition, report):
    for resource in saved_resources(client, "view", report=report):
        if resource.get("name") == name:
            return resource
    body = {"operationId": op_id("view"), "expectedRevision": 0, "kind": "view",
            "name": name, "definition": definition}
    try:
        raw = client.call("POST", "/v1/saved-resources", body, ok=(200,))
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "saved-view", "name": name,
                                                   "code": refusal.code, "status": refusal.status})
        return None
    report["created"]["views"].append({"name": name, "id": (raw.get("receipt") or raw).get("resourceId")})
    return raw


def reading_entries(client, library_id, limit=100):
    out, cursor = [], ""
    while len(out) < limit:
        query = {"libraryId": library_id, "limit": str(min(100, limit))}
        if cursor:
            query["cursor"] = cursor
        page = client.read("/v1/reading/items?" + urllib.parse.urlencode(query))
        out.extend(page.get("items") or [])
        cursor = page.get("nextCursor") or ""
        if not cursor:
            break
    return out[:limit]


def annotate_reading(client, entry, report, index):
    body = {"itemId": entry["id"], "sourceId": entry.get("sourceId", "")}
    try:
        opened = client.call("POST", "/v1/reading/sessions", body, ok=(200,))
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "reading-session", "itemId": entry["id"],
                                                   "code": refusal.code, "status": refusal.status})
        return
    session_id = opened.get("sessionId") or opened.get("id")
    fmt = (opened.get("format") or entry.get("format") or "epub").lower()
    if not session_id:
        return
    headers = {"If-Match": '"' + opened["revision"] + '"'}
    try:
        if fmt in ("pdf", "cbz", "cb7", "cbr"):
            location = {"format": fmt, "pageIndex": 1 + index, "progression": 0.15 + index * 0.05}
            annotation_location = {"format": fmt, "pageIndex": 1 + index, "progression": 0.15 + index * 0.05}
            page = 1 + index
        else:
            location = {"format": "epub", "cfi": "epubcfi(/6/2!/4/2:0)",
                        "progression": 0.2, "section": opened["spine"][0]}
            annotation_location = dict(location)
            page = None
        try:
            current = client.read(f"/v1/reading/sessions/{urllib.parse.quote(session_id, safe='')}/progress", extra_headers=headers)
            revision = int(((current.get("progress") or {}).get("revision")) or 0)
        except Refusal:
            revision = 0
        client.call("PUT", f"/v1/reading/sessions/{urllib.parse.quote(session_id, safe='')}/progress",
                    {"expectedRevision": revision, "location": location}, ok=(200,), extra_headers=headers)
        if page is not None and fmt != "pdf":
            client.call("GET", f"/v1/reading/sessions/{urllib.parse.quote(session_id, safe='')}/pages/{page}",
                        ok=(200,), timeout=60, extra_headers=headers)
        try:
            existing = client.read(f"/v1/reading/sessions/{urllib.parse.quote(session_id, safe='')}/annotations", extra_headers=headers)
            revision = int(existing.get("revision") or 0)
        except Refusal:
            revision = 0
        stable = hashlib.sha256(entry["id"].encode()).hexdigest()[:12]
        for kind in ("highlight", "bookmark"):
            payload = {"operation": "upsert", "id": f"seed-{stable}-{kind}",
                       "expectedRevision": revision, "kind": kind, "colour": "green",
                       "location": annotation_location, "note": "Synthetic annotation",
                       "text": "Synthetic passage", "rects": []}
            if kind == "highlight" and fmt == "pdf":
                payload["rects"] = [{"x": 0.1, "y": 0.1, "width": 0.5, "height": 0.08}]
            try:
                result = client.call(
                    "PUT", f"/v1/reading/sessions/{urllib.parse.quote(session_id, safe='')}/annotations",
                    payload, ok=(200,), extra_headers=headers)
                revision = int(result.get("revision") or revision)
            except Refusal as refusal:
                report.setdefault("failures", []).append(
                    {"step": "reading-annotation", "itemId": entry["id"], "code": refusal.code,
                     "status": refusal.status})
                break
        report["created"]["reading"].append({"itemId": entry["id"], "format": fmt,
                                             "annotations": revision})
    except Refusal as refusal:
        report.setdefault("failures", []).append({"step": "reading-progress", "itemId": entry["id"],
                                                  "code": refusal.code, "status": refusal.status})
    finally:
        try:
            client.call("DELETE", f"/v1/reading/sessions/{urllib.parse.quote(session_id, safe='')}",
                        ok=(204,))
        except Refusal:
            pass


def ensure_api_keys(owner, saved, report):
    existing = {entry.get("name"): entry for entry in items(owner.read("/v1/admin/api-keys"))}
    wanted = [
        ("Synthetic Read Key", ["read"]),
        ("Synthetic Playback Key", ["read", "playback"]),
        ("Synthetic Library Key", ["read", "library"]),
    ]
    for name, scopes in wanted:
        if name in existing:
            continue
        raw = owner.call("POST", "/v1/admin/api-keys", {
            "name": name, "operationId": op_id("api-key"), "scopes": scopes,
        }, ok=(201,))
        credential = raw.get("credential") or {}
        saved.setdefault("apiKeys", []).append({"id": credential.get("id"), "name": name,
                                                "secret": raw.get("secret"), "scopes": scopes})
        report["created"]["apiKeys"].append(name)
    if "Synthetic Retired Key" not in existing:
        raw = owner.call("POST", "/v1/admin/api-keys", {
            "name": "Synthetic Retired Key", "operationId": op_id("api-key-retired"), "scopes": ["read"],
        }, ok=(201,))
        credential = raw.get("credential") or {}
        saved.setdefault("apiKeys", []).append({"id": credential.get("id"), "name": "Synthetic Retired Key",
                                                "secret": raw.get("secret"), "scopes": ["read"],
                                                "retired": True})
        try:
            owner.call("DELETE", "/v1/admin/api-keys/" + urllib.parse.quote(credential.get("id", ""), safe=""),
                       ok=(200,))
        except Refusal:
            pass
        report["created"]["apiKeys"].append("Synthetic Retired Key")


def ensure_webhooks(owner, saved, report):
    listing = owner.read("/v1/admin/webhooks")
    existing = {entry.get("name"): entry for entry in items(listing)}
    wanted = [
        ("Synthetic Local Sink", "http://127.0.0.1:32512/portico-webhook", True,
         ["playback.started", "playback.stopped", "scan.finished", "backup.succeeded"]),
        ("Synthetic Archive Sink", "http://127.0.0.1:32512/archive", False,
         ["title.added", "title.updated", "member.signed_in"]),
    ]
    for name, url, enabled, events in wanted:
        if name in existing:
            webhook = existing[name]
            if enabled and webhook.get("id"):
                try:
                    deliveries = items(owner.read("/v1/admin/webhooks/" +
                                                  urllib.parse.quote(webhook["id"], safe="") +
                                                  "/deliveries?limit=20"))
                    if not any(200 <= int(entry.get("status") or 0) < 300
                               for entry in deliveries):
                        owner.call("POST", "/v1/admin/webhooks/" +
                                   urllib.parse.quote(webhook["id"], safe="") + "/test",
                                   ok=(202,))
                except Refusal:
                    pass
            continue
        secret = secrets.token_urlsafe(32)
        try:
            raw = owner.call("POST", "/v1/admin/webhooks", {
                "operationId": op_id("webhook"), "name": name, "url": url,
                "events": events, "enabled": enabled, "secret": secret,
            }, ok=(201,))
        except Refusal as refusal:
            report.setdefault("warnings", []).append({"step": "webhook", "name": name,
                                                       "code": refusal.code, "status": refusal.status})
            continue
        webhook = raw.get("webhook") or {}
        saved.setdefault("webhooks", []).append({"id": webhook.get("id"), "name": name,
                                                 "secret": raw.get("secret") or secret})
        report["created"]["webhooks"].append(name)
        if enabled and webhook.get("id"):
            try:
                owner.call("POST", "/v1/admin/webhooks/" + urllib.parse.quote(webhook["id"], safe="") + "/test",
                           ok=(202,))
            except Refusal:
                pass


def ensure_library_channels(owner, movie_ids, tv_ids, report):
    if not movie_ids and not tv_ids:
        return
    existing = {entry.get("config", {}).get("name") for entry in
                owner.read("/v1/admin/library-channels").get("channels", [])}
    wanted = [
        ("Synthetic Movie Night", movie_ids, "movie", True, "automatic"),
        ("Synthetic Family Films", movie_ids, "movie", True, "limited"),
        ("Synthetic TV Marathon", tv_ids, "episode", True, "automatic"),
        ("Synthetic Off Air", movie_ids or tv_ids, "movie" if movie_ids else "episode", False, "automatic"),
    ]
    for name, library_ids, kinds, enabled, quality in wanted:
        if name in existing:
            continue
        try:
            config = owner.call("POST", "/v1/admin/library-channels/defaults",
                                {"timezone": "UTC"}, ok=(200,)).get("config")
            if not isinstance(config, dict) or not config.get("rules"):
                continue
            config["name"] = name
            config["description"] = "Synthetic fixture channel"
            config["enabled"] = enabled
            config["viewerAccess"] = "server-members"
            config["timezone"] = "UTC"
            config["quality"]["mode"] = quality
            if quality == "limited":
                config["quality"]["maxHeight"] = 1080
                config["quality"]["maxBitrate"] = 8000000
            for rule in config["rules"]:
                query = rule.setdefault("query", {})
                query["libraryIds"] = list(library_ids)
                query["kinds"] = [kinds]
                query["showIds"] = []
                query["genres"] = []
                query["yearFrom"] = 0
                query["yearThrough"] = 0
                query["recentDays"] = 0
                query["limit"] = 0
                query["order"] = "title"
                query.pop("filter", None)
                query.pop("text", None)
                rule.setdefault("weights", [])
            preview = owner.call("POST", "/v1/admin/library-channels/preview", config, ok=(200,))
            sample = ((preview.get("preview") or {}).get("firstDay") or [])
            if not sample and preview.get("preview", {}).get("rules"):
                if not any((r.get("eligible") or 0) > 0 for r in preview["preview"]["rules"]):
                    report.setdefault("warnings", []).append(
                        {"step": "library-channel", "name": name, "code": "no_candidates"})
                    continue
            owner.call("POST", "/v1/admin/library-channels", {
                "config": config, "expectedRevision": 0, "requestId": opaque("channel:" + name),
            }, ok=(200,))
            report["created"]["libraryChannels"].append(name)
        except Refusal as refusal:
            report.setdefault("warnings", []).append({"step": "library-channel", "name": name,
                                                       "code": refusal.code, "status": refusal.status})


def ensure_backup(owner, report):
    try:
        listing = owner.read("/v1/admin/backups")
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "backup-read", "code": refusal.code,
                                                   "status": refusal.status})
        return
    if listing.get("backups") or listing.get("lastBackup"):
        return
    try:
        owner.call("POST", "/v1/admin/backups", {"operationId": op_id("backup")}, ok=(202,))
        report["created"]["backups"].append("queued")
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            time.sleep(2)
            current = owner.read("/v1/admin/backups")
            if current.get("backups") or current.get("lastBackup"):
                break
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "backup", "code": refusal.code,
                                                   "status": refusal.status})


def ensure_notifications(owner, report):
    try:
        capabilities = owner.read("/v1/notifications/capabilities")
        data = capabilities.get("data") or capabilities
        audiences = data.get("audiences") or ["profile"]
        severities = data.get("severities") or ["info"]
    except Refusal:
        audiences, severities = ["profile"], ["info"]
    messages = [
        ("Synthetic library is ready", "The generated catalogue finished its first scan.", "info"),
        ("Synthetic backup queued", "A fixture backup was requested.", "info"),
        ("Synthetic storage notice", "This is a generated low-storage notice.", "warning"),
        ("Synthetic critical notice", "This is a generated critical notice.", "critical"),
    ]
    for index, (title, body, severity) in enumerate(messages):
        audience = audiences[index % len(audiences)]
        chosen = severity if severity in severities else severities[0]
        try:
            owner.call("POST", "/v1/admin/notifications/broadcast", {
                "operationId": op_id("notice"), "dedupeKey": "synthetic-v3-notice-" + str(index),
                "title": title, "body": body, "severity": chosen, "audience": audience,
                "expiresInDays": 60,
            }, ok=(200,))
            report["created"]["notifications"].append(title)
        except Refusal as refusal:
            report.setdefault("warnings", []).append({"step": "notification", "title": title,
                                                       "code": refusal.code, "status": refusal.status})


def ensure_dvr(owner, path, report):
    """Request and read back the recording folder through the real API."""
    try:
        document = owner.read("/v1/admin/dvr/recording-root")
        requested = ((document.get("requested") or "") if isinstance(document.get("requested"), str)
                     else "")
        effective = ((document.get("effective") or "") if isinstance(document.get("effective"), str)
                     else "")
        report["recordingRoot"] = {"requested": requested, "effective": effective,
                                    "pendingRestart": document.get("pendingRestart")}
        if requested != path:
            document = owner.call("PATCH", "/v1/admin/dvr/recording-root", {
                "expectedRevision": document.get("revision", 0), "operationId": op_id("dvr-root"),
                "path": path,
            }, ok=(200,))
            requested = document.get("requested") or ""
            effective = document.get("effective") or ""
            report["recordingRoot"] = {"requested": requested, "effective": effective,
                                        "pendingRestart": document.get("pendingRestart")}
        if requested != path:
            report.setdefault("failures", []).append({"step": "recording-root",
                                                       "actual": report["recordingRoot"]})
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "recording-root", "code": refusal.code,
                                                   "status": refusal.status})


def ensure_recording(owner, report):
    """Record one currently-airing programme that ends within a few minutes."""
    try:
        page = owner.read("/v1/guide/now-next?limit=50")
    except Refusal as refusal:
        report.setdefault("warnings", []).append({"step": "recording-guide", "code": refusal.code,
                                                   "status": refusal.status})
        return
    now = time.time()
    candidates = []
    for channel in ((page.get("page") or {}).get("channels") or []):
        if not channel.get("recordAvailable"):
            continue
        for programme in channel.get("programmes") or []:
            try:
                from datetime import datetime
                end = datetime.strptime(programme.get("end", ""), "%Y-%m-%dT%H:%M:%S%z").timestamp()
            except (ValueError, TypeError):
                continue
            if 20 <= end - now <= 420:
                candidates.append((end, channel, programme))
    if not candidates:
        report["recording"] = {"created": False, "reason": "no programme ending within 7 minutes"}
        return
    _, channel, programme = min(candidates, key=lambda item: item[0])
    try:
        result = owner.call("POST", "/v1/dvr/recordings", {
            "occurrence": {"channelId": channel.get("id"), "generation": channel.get("generation"),
                           "programmeId": programme.get("id"), "sourceId": channel.get("sourceId")},
            "useDefaults": True, "requestId": opaque("recording"),
        }, ok=(200, 201, 202))
        recording = result.get("result", result)
        report["recording"] = {"created": True, "id": recording.get("id"),
                               "programme": programme.get("title"),
                               "start": programme.get("start"), "end": programme.get("end"),
                               "state": recording.get("state")}
        report["created"]["recordings"].append(recording.get("id"))
    except Refusal as refusal:
        report["recording"] = {"created": False, "code": refusal.code,
                               "status": refusal.status}


def ensure_live(owner, base, report):
    """Register a remote M3U source on a URL the server itself can reach.

    Never reads a local playlist: the seeder runs on mini-1 while the source
    runs on the server host.  ``base`` must end in "/" and serve channels.m3u,
    guide.xml and the HLS paths the playlist names.
    """
    base = (base or "").strip()
    if not base:
        report["live"] = {"present": False, "reason": "no live base URL"}
        return
    if not base.endswith("/"):
        base += "/"
    existing = owner.read("/v1/admin/live-sources")
    for source in existing.get("sources") or []:
        if source.get("name") == "Synthetic Broadcast":
            report["live"] = {"present": True, "sourceId": source.get("id"), "created": False,
                              "channels": source.get("channels"), "base": base}
            return
    draft = {
        "id": opaque("live-source"), "expectedRevision": 0, "name": "Synthetic Broadcast",
        "kind": "m3u", "locator": base + "channels.m3u", "guideUrl": base + "guide.xml",
        "confirmedLanRoots": [base], "refreshMs": 21600000, "ownerLimit": 3,
        "useDiscoveredCapacity": False, "viewerAccess": "server-members",
        "mappings": [], "enabledGroups": [], "enabledChannels": [],
    }
    try:
        preview = owner.call("POST", "/v1/admin/live-sources/remote/preview", draft, ok=(200,))
    except Refusal as refusal:
        if refusal.status == 422 and "confirmRoots" in refusal.detail:
            try:
                roots = json.loads(refusal.detail).get("confirmRoots") or []
            except ValueError:
                roots = []
            draft["confirmedLanRoots"] = list(dict.fromkeys([base] + roots))
            try:
                preview = owner.call("POST", "/v1/admin/live-sources/remote/preview", draft, ok=(200,))
            except Refusal as second:
                report["live"] = {"present": False, "error": second.code,
                                  "status": second.status, "base": base}
                return
        else:
            report["live"] = {"present": False, "error": refusal.code,
                              "status": refusal.status, "base": base}
            return
    remote = preview.get("remotePreview") or {}
    preview_id = remote.get("previewId")
    if not preview_id:
        report["live"] = {"present": False, "error": "preview_missing", "base": base}
        return
    try:
        saved = owner.call("POST", "/v1/admin/live-sources/remote/save", {
            "previewId": preview_id, "requestId": opaque("live-save"),
        }, ok=(200,))
        source = saved.get("source") or {}
        report["live"] = {"present": True, "created": True, "sourceId": source.get("id"),
                          "channels": source.get("channels"), "programmes": source.get("programmes"),
                          "base": base}
        report["created"]["liveSources"].append("Synthetic Broadcast")
    except Refusal as refusal:
        report["live"] = {"present": False, "error": refusal.code,
                          "status": refusal.status, "base": base}


def readback_profile(client, label, report, expect_history):
    """Prove what was written is actually readable under this profile."""
    checks = []
    def check(name, ok, detail=None):
        checks.append({"profile": label, "check": name, "ok": bool(ok), "detail": detail})
        if not ok:
            report.setdefault("failures", []).append({"profile": label, "check": name,
                                                       "detail": detail})
    try:
        summary = client.read("/v1/me/saved/summary")
    except Refusal as refusal:
        check("saved-summary", False, refusal.code)
        summary = {}
    my_list = int((summary.get("myList") or {}).get("posters") or 0) + \
        int((summary.get("myList") or {}).get("squares") or 0)
    check("my-list-count", my_list > 0, my_list)
    check("collection-count", int(summary.get("collections") or 0) >= 2,
          summary.get("collections"))
    check("playlist-count", int(summary.get("playlists") or 0) >= 2,
          summary.get("playlists"))
    check("saved-view-count", int(summary.get("savedViews") or 0) >= 3,
          summary.get("savedViews"))
    collections = saved_resources(client, "collection", maximum=100, report=report)
    empty_collection = next((r for r in collections if r.get("name", "").endswith("Empty Collection")), None)
    if empty_collection:
        content = client.read("/v1/saved-resources/" + urllib.parse.quote(empty_collection["id"], safe="") +
                              "/content?limit=50")
        check("empty-collection", len(content.get("entries") or []) == 0,
              len(content.get("entries") or []))
    playlists = items(client.read("/v1/playlists"))
    empty_playlist = next((p for p in playlists if p.get("name", "").endswith("Empty Playlist")), None)
    if empty_playlist:
        content = client.read("/v1/playlists/" + urllib.parse.quote(empty_playlist["id"], safe="") +
                              "/content?limit=50")
        total = sum(len(section.get("entries") or []) for section in content.get("sections") or [])
        check("empty-playlist", total == 0, total)
    views = saved_resources(client, "view", maximum=100, report=report)
    empty_view = next((v for v in views if v.get("name", "").endswith("Empty View")), None)
    if empty_view:
        content = client.read("/v1/saved-resources/" + urllib.parse.quote(empty_view["id"], safe="") +
                              "/content?limit=50")
        matched = len((content.get("browse") or {}).get("entries") or [])
        check("empty-view", matched == 0, matched)
    if expect_history:
        history = client.read("/v1/personal-history?limit=20")
        check("personal-history", len(history.get("entries") or []) > 0,
              len(history.get("entries") or []))
    return checks


def profile_catalogue(client, libraries, report):
    """Only items this selected profile can actually see, by real routes."""
    items_by_kind = {}
    for library in libraries:
        maximum = 50 if library["kind"] in ("book", "comic", "audiobook") else 300
        try:
            found = library_items(client, library, maximum)
        except Refusal as refusal:
            report.setdefault("warnings", []).append(
                {"step": "profile-catalogue", "library": library.get("name"),
                 "code": refusal.code, "status": refusal.status})
            continue
        for item in found:
            kind = item.get("kind") or library["kind"]
            items_by_kind.setdefault(kind, []).append(item)
        if library["kind"] in ("book", "comic"):
            items_by_kind.setdefault("reading", []).extend(found[:12])
    return items_by_kind


def profile_state(client, label, items_by_kind, report, history=2, manifest_path=None):
    """Saved state for one selected profile; every call is an ordinary API call."""
    created = {"label": label}
    movies = items_by_kind.get("movie", [])
    episodes = items_by_kind.get("episode", [])
    songs = items_by_kind.get("song", [])
    books = items_by_kind.get("audiobook_file", []) + items_by_kind.get("part", [])
    reading = items_by_kind.get("reading", [])
    all_playable = movies + episodes + songs + books
    picks = (movies[:6] + episodes[:4] + songs[:4]) or all_playable[:8]
    revisions = batch_flags(client, [(item["id"], {"watchlisted": True}) for item in picks],
                            report)
    client and batch_flags(client, [(item["id"], {"favorite": True}) for item in picks[:4]], report)
    created["myList"] = len(picks)
    # Continue-watching is a real progress fact beside an explicit un-watched
    # action, and must stay genuinely partial for tiny fixture files.
    for item in (movies[:2] + episodes[:2] + books[:2] + songs[:2]):
        duration = int(float(item.get("durationMs") or 0))
        if duration <= 0:
            continue
        set_personal(client, item["id"], "progressMs", min(max(1000, duration // 5), duration - 1000),
                     report)
    # History uses only qualified light sources, avoiding accidental 4K work.
    for item in light_history_movies(movies, manifest_path)[:history]:
        start_history(client, item["id"], 0.25, report,
                      duration_ms=int(float(item.get("durationMs") or 0)))
    # Listening progress for a book is a partial position, not a fixed 45 s.
    for item in books[:1]:
        duration = int(float(item.get("durationMs") or 0))
        if duration > 0:
            set_personal(client, item["id"], "progressMs",
                         min(max(1000, duration // 4), duration - 1000), report)
    # Saved resources: real members, order and an empty variant.
    ensure_collection(client, PREFIX + label + " Picks", [item["id"] for item in picks[:5]], report)
    ensure_collection(client, PREFIX + label + " Empty Collection", [], report)
    ensure_playlist(client, PREFIX + label + " Evening Queue",
                    [item["id"] for item in all_playable[:8]], report)
    ensure_playlist(client, PREFIX + label + " Empty Playlist", [], report)
    if movies:
        library = movies[0]["libraryId"]
        ensure_view(client, PREFIX + label + " Films A-Z", {
            "libraryId": library, "pivot": "movies", "query": None,
            "sort": [{"field": "title", "direction": "asc"}], "presentation": "grid",
        }, report)
        ensure_view(client, PREFIX + label + " Recently Added", {
            "libraryId": library, "pivot": "movies", "query": None,
            "sort": [{"field": "dateAdded", "direction": "desc"}], "presentation": "grid",
        }, report)
        ensure_view(client, PREFIX + label + " Empty View", {
            "libraryId": library, "pivot": "movies",
            "query": {"field": "year", "operator": "equals", "value": 1801},
            "sort": [{"field": "title", "direction": "asc"}], "presentation": "grid",
        }, report)
    # Reading: progress, a bookmark/highlight pair and personal reading marks.
    for index, entry in enumerate(reading[:3]):
        annotate_reading(client, entry, report, index)
        if index == 0:
            try:
                client.call("PUT", f"/v1/items/{urllib.parse.quote(entry['id'], safe='')}/reading/mark",
                            {"operationId": op_id("read-mark"), "expectedRevision": 0, "read": True},
                            ok=(200,))
                client.call("PUT", f"/v1/items/{urllib.parse.quote(entry['id'], safe='')}/reading/favorite",
                            {"operationId": op_id("read-fav"), "expectedRevision": 0, "favorite": True},
                            ok=(200,))
            except Refusal as refusal:
                report.setdefault("warnings", []).append(
                    {"step": "reading-mark", "itemId": entry["id"], "code": refusal.code,
                     "status": refusal.status})
        if index == 1:
            try:
                client.call("PUT", f"/v1/items/{urllib.parse.quote(entry['id'], safe='')}/reading/my-list",
                            {"operationId": op_id("read-list"), "expectedRevision": 0, "listed": True},
                            ok=(200,))
            except Refusal:
                pass
    created["readback"] = readback_profile(client, label, report,
                                           expect_history=bool(history))
    report["profiles"].append(created)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", "--remote-root", default=os.environ.get("PORTICO_LIBRARY_ROOT", DEFAULT_SERVER_ROOT),
                        help="library root on the server (never probed locally when a manifest is given)")
    parser.add_argument("--manifest", "--manifest-path", type=Path, default=None,
                        help="locally downloaded manifest.json; declares server-side library roots")
    parser.add_argument("--recordings-root",
                        default="/home/runner/work/test-server-v3/state/recordings")
    parser.add_argument("--live-base", default="",
                        help="server-reachable base URL serving channels.m3u/guide.xml/HLS (e.g. http://127.0.0.1:32511/)")
    parser.add_argument("--no-live", action="store_true", help="skip live-source registration")
    parser.add_argument("--base", default=os.environ.get("PORTICO_TEST_BASE", DEFAULT_BASE))
    parser.add_argument("--credentials", type=Path, default=DEFAULT_CREDENTIALS)
    parser.add_argument("--report", type=Path, default=None)
    parser.add_argument("--no-bootstrap", action="store_true",
                        help="refuse when the server still needs first-run setup")
    parser.add_argument("--rescan", action="store_true", help="queue a full scan for every generated library")
    parser.add_argument("--scan-timeout", type=int, default=900)
    parser.add_argument("--history", type=int, default=2,
                        help="real playback history items per profile (0 disables)")
    parser.add_argument("--light", action="store_true",
                        help="state only; skip reading sessions and playback history")
    args = parser.parse_args(argv)

    root = str(args.root).rstrip("/") or "/"
    server_root = root
    report = {"schema": "portico-synthetic-seed/3", "root": str(root), "base": args.base,
              "startedAt": now(), "libraries": {}, "scans": [], "created":
              {key: [] for key in ("members", "profiles", "avatars", "collections", "playlists",
                                   "views", "history", "reading", "apiKeys", "webhooks",
                                   "libraryChannels", "backups", "notifications", "liveSources",
                                   "recordings")},
              "profiles": [], "warnings": [], "live": None}
    credentials = private_json(args.credentials)
    saved = credentials.setdefault("fixtureStateV3", {})
    saved["base"] = args.base
    saved.setdefault("profiles", {})
    owner_credentials = credentials.get("owner", credentials)
    installation = str(uuid.uuid4())
    owner = Client(args.base, installation)

    try:
        try:
            signed, token = sign_in(owner, owner_credentials["username"], owner_credentials["password"])
        except Refusal as refusal:
            if refusal.status not in (401, 403):
                raise
            if args.no_bootstrap:
                raise Refusal("setup_required", refusal.status)
            session = bootstrap_setup(owner, owner_credentials)
            token = session["accessToken"]
            signed, token = sign_in(owner, owner_credentials["username"], owner_credentials["password"])
        owner.token = token
        primary = next((p for p in signed.get("profiles") or [] if p.get("primary")), None)
        if primary:
            owner.token = select_profile(owner, primary["id"])
        saved.setdefault("accounts", {})[owner_credentials["username"]] = {
            "role": "owner", "primaryProfileId": primary.get("id") if primary else None,
            "signedInAt": now()}
        system = owner.read("/v1/system")
        report["server"] = {"id": system.get("id"), "version": system.get("version"),
                            "commit": system.get("commit")}
        saved["serverId"] = system.get("id")
        saved["serverVersion"] = system.get("version")
        setup_state = owner.read("/v1/setup/state")
        report["setup"] = {key: setup_state.get(key) for key in ("phase", "ready", "authMode")}

        # Libraries ---------------------------------------------------------
        declarations = manifest_libraries(server_root, args.manifest)
        if not declarations:
            raise Refusal("no_library_directories", detail=str(server_root))
        report["manifest"] = str(args.manifest) if args.manifest else None
        report["declared"] = [{"name": name, "kind": kind, "path": path}
                              for name, kind, path in declarations]
        library_ids, library_by_name = [], {}
        for name, kind, path in declarations:
            chosen, library_id = ensure_library(owner, name, kind, path, report)
            library_ids.append(library_id)
            library_by_name[chosen] = library_id
            saved.setdefault("libraries", {})[chosen] = {"id": library_id, "kind": kind,
                                                          "path": path}
        write_private(args.credentials, credentials)
        # Local metadata only, everywhere.
        for name, library_id in library_by_name.items():
            quiet_library(owner, name, library_id, report)

        # Scans -------------------------------------------------------------
        for name, library_id in library_by_name.items():
            sources = scan_status(owner, library_id)
            discovered = sum(int(source.get("discovered") or 0) for source in sources)
            complete = bool(sources) and all(source.get("status") in ("complete", "complete_with_warnings")
                                             for source in sources)
            if args.rescan or not complete or discovered == 0:
                try:
                    owner.call("POST", "/v1/libraries/" + urllib.parse.quote(library_id, safe="") +
                               "/scans" + ("?mode=full" if args.rescan else ""), {},
                               ok=(200, 201, 202))
                except Refusal as refusal:
                    report["warnings"].append({"step": "scan-queue", "library": name,
                                                "code": refusal.code, "status": refusal.status})
        for name, library_id in library_by_name.items():
            wait_scan(owner, library_id, args.scan_timeout, report, name)

        # Catalogue inventory ----------------------------------------------
        items_by_kind, items_by_library = {}, {}
        for name, library_id in library_by_name.items():
            library = report["libraries"][name]
            found = library_items(owner, library)
            items_by_library[name] = len(found)
            for item in found:
                kind = item.get("kind") or library.get("kind")
                items_by_kind.setdefault(kind, []).append(item)
            library.update(count=len(found))
        saved["counts"] = {name: count for name, count in items_by_library.items()}
        saved["kinds"] = {kind: len(values) for kind, values in items_by_kind.items()}
        report["counts"] = saved["counts"]

        # Accounts and profiles --------------------------------------------
        for member in credentials.get("members") or []:
            if not member.get("username") or not member.get("password"):
                continue
            ensure_member(owner, member["username"], member["password"],
                          member.get("name") or member["username"], library_ids, report)
        snapshot = owner.read("/v1/direct")
        profiles = snapshot.get("profiles") or []
        owner_primary = next((p for p in profiles if p.get("primary")), profiles[0] if profiles else None)
        kids = ensure_profile(owner, "Synthetic Kids", "mint", report)
        guest = ensure_profile(owner, "Synthetic Guest", "gold", report)
        kids_pin = saved["profiles"].get(kids["id"], {}).get("pin")
        reset_pin = not kids_pin or not kids.get("pinRequired")
        if not kids_pin:
            kids_pin = str(1000 + secrets.randbelow(9000))
        # Refresh the existing profile's library policy too, so newly registered
        # anime/reading libraries are available on repeat provisioning runs.
        profile_edit = {"expectedRevision": kids.get("revision", 0),
                        "policy": {"allowedLibraries": library_ids}}
        if reset_pin:
            profile_edit["pin"] = kids_pin
        owner.call("PATCH", "/v1/direct/profiles/" + urllib.parse.quote(kids["id"], safe=""),
                   profile_edit, ok=(200,))
        saved["profiles"][kids["id"]] = {"name": "Synthetic Kids", "pin": kids_pin}
        for profile, art in ((owner_primary, "blue"), (kids, "mint"), (guest, "gold")):
            if profile:
                upload_avatar(owner, profile["id"], "Profile " + profile.get("name", art), report)
        try:
            restrictions = owner.read("/v1/direct/profiles/" + urllib.parse.quote(kids["id"], safe="") +
                                       "/restrictions")
            owner.call("PUT", "/v1/direct/profiles/" + urllib.parse.quote(kids["id"], safe="") +
                       "/restrictions", {
                           "expectedRevision": restrictions.get("revision", 1),
                           "ratingSystem": "mpaa", "maximumAgeRating": "PG", "allowUnrated": True,
                           "blockedLabels": [], "allowDownloads": False, "allowLiveTv": False,
                           "allowDvr": False, "allowWatchTogether": False,
                       }, ok=(200,))
            report["created"]["profiles"].append("Synthetic Kids restrictions")
        except Refusal as refusal:
            report["warnings"].append({"step": "kids-restrictions", "code": refusal.code,
                                        "status": refusal.status})

        # Select each profile and build genuine per-profile state ----------
        reading = []
        for name in ("Synthetic Books", "Synthetic Comics", "Synthetic Magazines"):
            if name in library_by_name:
                try:
                    reading.extend(reading_entries(owner, library_by_name[name], limit=12))
                except Refusal as refusal:
                    report["warnings"].append({"step": "reading-items", "library": name,
                                                "code": refusal.code, "status": refusal.status})
        items_by_kind["reading"] = reading

        targets = []
        owner_login = (owner_credentials["username"], owner_credentials["password"])
        if owner_primary:
            targets.append(("Owner", owner_primary["id"], None) + owner_login)
        if kids:
            targets.append(("Kids", kids["id"], kids_pin) + owner_login)
        if guest:
            targets.append(("Guest", guest["id"], None) + owner_login)
        member_profile_ids = []
        for member in credentials.get("members") or []:
            profile_client = Client(args.base, str(uuid.uuid4()))
            try:
                _, member_token = sign_in(profile_client, member["username"], member["password"])
                profile_client.token = member_token
                member_snapshot = profile_client.read("/v1/direct")
                member_primary = next((p for p in member_snapshot.get("profiles") or []
                                       if p.get("primary")), None)
                if member_primary:
                    saved.setdefault("accounts", {})[member["username"]] = {
                        "role": "member", "primaryProfileId": member_primary["id"],
                        "signedInAt": now()}
                    upload_avatar(profile_client, member_primary["id"],
                                  "Profile " + (member.get("name") or member["username"]), report)
                    member_profile_ids.append((member.get("name") or member["username"],
                                               member_primary["id"], None,
                                               member["username"], member["password"]))
            except Refusal as refusal:
                report["warnings"].append({"step": "member-sign-in", "username": member["username"],
                                            "code": refusal.code, "status": refusal.status})
        targets.extend(member_profile_ids)

        for label, profile_id, pin, username, password in targets:
            try:
                client = open_profile(args.base, username, password, profile_id, pin)
            except Refusal as refusal:
                report.setdefault("failures", []).append({"step": "profile-select",
                                                           "profile": label,
                                                           "code": refusal.code,
                                                           "status": refusal.status})
                continue
            saved["profiles"].setdefault(profile_id, {}).update({
                "name": label, "username": username, "signedInAt": now(),
                "role": "restricted" if label == "Kids" else
                        "guest" if label == "Guest" else
                        "owner" if label == "Owner" else "adult"})
            if args.light:
                report["profiles"].append({"label": label, "profileId": profile_id, "light": True})
                continue
            library_rows = [{"name": name, "id": library_id,
                             "kind": report["libraries"][name]["kind"]}
                            for name, library_id in library_by_name.items()]
            catalogue = profile_catalogue(client, library_rows, report)
            profile_state(client, label if label.startswith("Synthetic") else "Synthetic " + label,
                          catalogue, report, history=max(0, args.history), manifest_path=args.manifest)

        # Server settings ---------------------------------------------------
        ensure_api_keys(owner, saved, report)
        write_private(args.credentials, credentials)
        ensure_webhooks(owner, saved, report)
        write_private(args.credentials, credentials)
        movie_ids = [lib for name, lib in library_by_name.items()
                     if report["libraries"][name]["kind"] == "movie"]
        tv_ids = [lib for name, lib in library_by_name.items()
                  if report["libraries"][name]["kind"] in ("tv", "anime")]
        ensure_library_channels(owner, movie_ids, tv_ids, report)
        ensure_backup(owner, report)
        ensure_notifications(owner, report)
        live_base = args.live_base
        if not live_base and args.manifest and Path(args.manifest).is_file():
            try:
                manifest_doc = json.loads(Path(args.manifest).read_text(encoding="utf-8"))
                live_doc = manifest_doc.get("live") if isinstance(manifest_doc, dict) else None
                if isinstance(live_doc, dict):
                    live_base = live_doc.get("base") or live_doc.get("url") or ""
                elif isinstance(live_doc, str):
                    live_base = live_doc
                if not live_base:
                    entries = manifest_doc.get("entries") if isinstance(manifest_doc, dict) else None
                    if isinstance(entries, list) and any(
                            isinstance(entry, dict) and entry.get("kind") == "live-channel"
                            for entry in entries):
                        # The generated live source is served by serve_live.py on
                        # the server host's loopback; never read it from mini-1.
                        live_base = "http://127.0.0.1:32511/"
            except (OSError, ValueError):
                pass
        ensure_dvr(owner, args.recordings_root, report)
        if not args.no_live and live_base:
            ensure_live(owner, live_base, report)
            if report.get("live", {}).get("present"):
                ensure_recording(owner, report)
        elif args.no_live:
            report["live"] = {"present": False, "reason": "disabled by --no-live"}
        report["finishedAt"] = now()
        report["ok"] = not report.get("failures")
    except Refusal as refusal:
        report["ok"] = False
        report["error"] = {"code": refusal.code, "status": refusal.status, "detail": redact(refusal.detail)}
    except Exception as error:
        report["ok"] = False
        report["error"] = {"code": "seed_shape", "type": type(error).__name__}
    finally:
        try:
            saved["updatedAt"] = now()
            write_private(args.credentials, credentials)
        except Exception:
            pass
        text = json.dumps(report, indent=2, sort_keys=True)
        if args.report is not None:
            try:
                args.report.parent.mkdir(parents=True, exist_ok=True)
                args.report.write_text(text + "\n", encoding="utf-8")
                os.chmod(args.report, 0o600)
            except OSError:
                pass
        print(text)
    return 0 if report.get("ok") else 1


def generate(root: Path, ffmpeg: str, full: bool = True) -> list[dict]:
    """State only: classified here so the driver can import every module."""
    del root, ffmpeg, full
    return []


if __name__ == "__main__":
    raise SystemExit(main())
