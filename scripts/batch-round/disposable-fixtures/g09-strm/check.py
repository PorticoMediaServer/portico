#!/usr/bin/env python3
"""G09 server-side STRM checks against the approved-origin fixture.

Runs on the private fixture network. Every case records what was actually
contacted from the origin logs; raw STRM URLs, grants and tokens never enter a
receipt. Device decoding and focus remain G10 work.
"""

import argparse
import hashlib
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))
import api  # noqa: E402

HTTP_ORIGIN = "http://media.portico.test:8080"
HTTPS_ORIGIN = "https://media.portico.test:8443"


def ctl(base, token, method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, method=method,
                                 headers={"Content-Type": "application/json",
                                          "X-Fixture-Token": token})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read() or b"{}")


class OriginLog:
    def __init__(self, path):
        self.path = path

    def mark(self):
        try:
            return sum(1 for _ in open(self.path, "r", encoding="utf-8"))
        except OSError:
            return 0

    def since(self, mark):
        out = []
        try:
            with open(self.path, "r", encoding="utf-8") as handle:
                for index, line in enumerate(handle):
                    if index < mark:
                        continue
                    line = line.strip()
                    if line:
                        try:
                            out.append(json.loads(line))
                        except ValueError:
                            continue
        except OSError:
            return []
        return out


def grant_path(url, base):
    parts = urllib.parse.urlsplit(url)
    base_parts = urllib.parse.urlsplit(base)
    if parts.netloc and parts.netloc != base_parts.netloc:
        raise AssertionError("grant left the server origin")
    path = parts.path + (("?" + parts.query) if parts.query else "")
    if not path.startswith("/v1/media/"):
        raise AssertionError("grant is not a media route")
    return path


def safe_grant(url, base):
    path = grant_path(url, base)
    parts = urllib.parse.urlsplit(path).path.split('/')
    return '/v1/media/{grant}/' + '/'.join(parts[4:])


def fetch(client, path, headers=None):
    return client.request_raw("GET", path, headers=headers or {}, expect=(200, 206))


def playlist_segments(client, path):
    _, body, _ = fetch(client, path)
    if b'#EXT-X-STREAM-INF:' in body:
        variants = [line.strip() for line in body.decode().splitlines() if line.strip() and not line.startswith('#')]
        if len(variants) != 1:
            raise AssertionError('expected one HLS variant')
        path = grant_path(urllib.parse.urljoin(path, variants[0]), client.base)
        _, body, _ = fetch(client, path)
    out = []
    for line in body.decode("utf-8", "replace").splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            out.append(line)
    return path, body, out


class Recorder:
    def __init__(self):
        self.cases = []

    def case(self, name, passed, detail, blocked=False):
        self.cases.append({"case": name, "passed": bool(passed), "blocked": bool(blocked),
                           "detail": detail})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--state", required=True)
    parser.add_argument("--password-file", required=True)
    parser.add_argument("--control", default="http://172.30.67.10:8081")
    parser.add_argument("--bad-control", default="http://172.30.67.11:8082")
    parser.add_argument("--origin-log", required=True)
    parser.add_argument("--origin-tls-log", required=True)
    parser.add_argument("--bad-log", required=True)
    parser.add_argument("--zone", required=True)
    parser.add_argument("--media", required=True)
    parser.add_argument("--server-ip", default="172.30.67.20")
    parser.add_argument("--out", required=True)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    if not args.execute:
        print("PLAN: would exercise STRM direct/remux/HEVC, redirects, offline, replacement, DNS and revocation")
        return 0
    if os.path.exists(args.out):
        raise SystemExit("refusing to overwrite a previous receipt")

    token = open(os.environ["G09_CONTROL_TOKEN"], "r", encoding="utf-8").read().strip()
    origin = OriginLog(args.origin_log)
    origin_tls = OriginLog(args.origin_tls_log)
    bad = OriginLog(args.bad_log)
    rec = Recorder()
    try:
        ctl(args.bad_control, token, "POST", "/redirect",
            {"target": "http://172.30.67.11:8080/media/direct.mp4"})
    except (urllib.error.URLError, OSError) as err:
        rec.case("bad-origin-control", False, {"error": str(err)})

    if os.path.exists(args.state):
        client = api.Api.load(args.base, args.state)
        try:
            client.request("GET", "/v1/server")
        except api.ApiError as err:
            if err.status != 401:
                raise
            try:
                client.refresh()
                client.save_state(args.state)
            except api.ApiError:
                os.unlink(args.state)
                client = None
    else:
        client = None
    if client is None:
        password = open(args.password_file, "r", encoding="utf-8").read().strip()
        client = api.bootstrap(args.base, "fixture-owner", password, "G09 fixture owner")
        client.save_state(args.state)

    strm = os.path.join(args.media, "strm")
    os.makedirs(strm, exist_ok=True)
    cases = {
        "direct": HTTP_ORIGIN + "/media/direct.mp4",
        "https": HTTPS_ORIGIN + "/media/direct.mp4",
        "remux": HTTP_ORIGIN + "/media/remux.mkv",
        "hevc": HTTP_ORIGIN + "/media/hevc10.mkv",
        "redirect-ok": HTTP_ORIGIN + "/redirect/approved",
        "redirect-bad": HTTP_ORIGIN + "/redirect/unapproved",
        "redirect-meta": HTTP_ORIGIN + "/redirect/metadata",
        "offline": HTTP_ORIGIN + "/media/offline.mp4",
        "replace": HTTP_ORIGIN + "/media/replace.mp4",
    }
    for name, locator in cases.items():
        with open(os.path.join(strm, name + ".strm"), "w", encoding="utf-8") as handle:
            handle.write(locator + "\n")

    libraries = client.request("GET", "/v1/libraries")
    libraries = libraries if isinstance(libraries, list) else libraries.get("items", [])
    library = next((l for l in libraries if l.get("name") == "G09 fixture streams"), None)
    if library is None:
        created = client.request("POST", "/v1/libraries",
                                 {"name": "G09 fixture streams", "kind": "movie",
                                  "path": "/media/strm", "scanAfter": False}, expect=(201,))
        library = created.get("library", created)
    library_id = library["id"]
    client.request("POST", "/v1/libraries/%s/network-roots" % library_id,
                   {"roots": [HTTP_ORIGIN + "/", HTTPS_ORIGIN + "/"]}, expect=(200,))
    client.request("POST", "/v1/libraries/%s/scans" % library_id, expect=(202,))
    api.wait_inventory(client, library_id)
    entries = []
    for _ in range(30):
        entries = api.browse_items(client, library_id)
        if entries:
            break
        time.sleep(1.0)
    items = {}
    for entry in entries:
        title = (entry.get("title") or "").lower().replace(" ", "-")
        for name in sorted(cases, key=len, reverse=True):
            if title == name or title.startswith(name):
                items.setdefault(name, entry["id"])
                break

    def item(name):
        if name not in items:
            raise AssertionError("no catalog item for " + name)
        return items[name]

    def start(name, quality=None):
        return api.start_session(client, item(name), quality=quality)

    def stop(session):
        api.stop_session(client, session, 0)

    # 1. Direct MP4, real bytes, only Portico grants.
    try:
        mark = origin.mark()
        session = start("direct", {"mode": "original"})
        mode = session["presentation"]["mode"]
        path = grant_path(session["presentation"]["url"], client.base)
        _, body, _ = fetch(client, path, {"Range": "bytes=0-65535"})
        hits = origin.since(mark)
        rec.case("direct-mp4", mode == "direct" and len(body) > 0
                 and any(h["path"] == "/media/direct.mp4" for h in hits)
                 and all(h["client"] == args.server_ip for h in hits),
                 {"mode": mode, "grantPath": safe_grant(session["presentation"]["url"], client.base),
                  "bytes": len(body), "originHits": [h["path"] for h in hits],
                  "clients": sorted({h["client"] for h in hits})})
        stop(session)
    except (api.ApiError, AssertionError, urllib.error.URLError) as err:
        rec.case("direct-mp4", False, {"error": getattr(err, "code", str(err))})

    # 2. HTTPS direct with the fixture CA trusted by the server.
    try:
        mark = origin_tls.mark()
        session = start("https", {"mode": "original"})
        _, body, _ = fetch(client, grant_path(session["presentation"]["url"], client.base),
                           {"Range": "bytes=0-65535"})
        hits = origin_tls.since(mark)
        rec.case("https-direct", len(body) > 0 and any(h["path"] == "/media/direct.mp4" for h in hits),
                 {"bytes": len(body), "originHits": [h["path"] for h in hits]})
        stop(session)
    except (api.ApiError, AssertionError, urllib.error.URLError) as err:
        rec.case("https-direct", False, {"error": getattr(err, "code", str(err))})

    # 3. Matroska H.264 remux plus seek during the remux.
    try:
        mark = origin.mark()
        session = start("remux", {"mode": "limit", "maxHeight": 720})
        mode = session["presentation"]["mode"]
        container = session["presentation"].get("container")
        grant = grant_path(session["presentation"]["url"], client.base)
        playlist, body, segments = playlist_segments(client, grant)
        if segments:
            fetch(client, urllib.parse.urljoin(playlist, segments[0]))
        seeked = api.change_session(client, session, {"seek": {"positionMs": 5000,
                                                              "id": "g09-seek-1"},
                                                      "positionMs": 5000})
        _, _, after = playlist_segments(client, grant_path(seeked["presentation"]["url"], client.base))
        hits = origin.since(mark)
        rec.case("mkv-remux-seek", mode != "direct" and bool(segments) and bool(after)
                 and bool(seeked["presentation"].get("seekId"))
                 and any(h["path"] == "/media/remux.mkv" for h in hits),
                 {"mode": mode, "container": container, "segments": len(segments),
                  "postSeekSegments": len(after), "originHits": [h["path"] for h in hits]})
        stop(seeked)
    except (api.ApiError, AssertionError, urllib.error.URLError) as err:
        rec.case("mkv-remux-seek", False, {"error": getattr(err, "code", str(err))})

    # 4. HEVC 10-bit conversion.
    try:
        mark = origin.mark()
        session = start("hevc", {"mode": "limit", "maxHeight": 480})
        mode = session["presentation"]["mode"]
        playlist, _, segments = playlist_segments(client,
                                                  grant_path(session["presentation"]["url"], client.base))
        if segments:
            fetch(client, urllib.parse.urljoin(playlist, segments[0]))
        hits = origin.since(mark)
        rec.case("hevc-conversion", mode != "direct" and bool(segments)
                 and any(h["path"] == "/media/hevc10.mkv" for h in hits),
                 {"mode": mode, "container": session["presentation"].get("container"),
                  "segments": len(segments), "originHits": [h["path"] for h in hits]})
        stop(session)
    except (api.ApiError, AssertionError, urllib.error.URLError) as err:
        rec.case("hevc-conversion", False, {"error": getattr(err, "code", str(err))})

    # 5. In-root redirect follows; out-of-root and metadata redirects are refused
    #    with no contact to the refused origin.
    try:
        session = start("redirect-ok", {"mode": "original"})
        _, body, _ = fetch(client, grant_path(session["presentation"]["url"], client.base),
                           {"Range": "bytes=0-1023"})
        rec.case("redirect-approved", bool(body), {"bytes": len(body)})
        stop(session)
    except (api.ApiError, AssertionError, urllib.error.URLError) as err:
        rec.case("redirect-approved", False, {"error": getattr(err, "code", str(err))})

    for name, label in (("redirect-bad", "redirect-out-of-root"),
                        ("redirect-meta", "redirect-metadata")):
        try:
            bad_mark = bad.mark()
            start(name, {"mode": "original"})
            rec.case(label, False, {"error": "start unexpectedly accepted"})
        except api.ApiError as err:
            hits = bad.since(bad_mark)
            rec.case(label, not hits, {"code": err.code, "status": err.status,
                                       "refusedOriginHits": [h["path"] for h in hits]})
        except (AssertionError, urllib.error.URLError) as err:
            rec.case(label, False, {"error": str(err)})

    # 6. Offline origin answers a retryable source_unavailable, then recovers.
    try:
        ctl(args.control, token, "POST", "/offline", {"name": "offline.mp4", "offline": True})
        mark = origin.mark()
        try:
            start("offline", {"mode": "original"})
            rec.case("offline-refusal", False, {"error": "start unexpectedly accepted"})
        except api.ApiError as err:
            hits = origin.since(mark)
            rec.case("offline-refusal", err.code == "source_unavailable" and err.retryable
                     and any(h["path"] == "/media/offline.mp4" and h["status"] == 503 for h in hits),
                     {"code": err.code, "retryable": err.retryable,
                      "originStatuses": [h["status"] for h in hits]})
        ctl(args.control, token, "POST", "/offline", {"name": "offline.mp4", "offline": False})
        session = start("offline", {"mode": "original"})
        rec.case("offline-recovery", True, {"mode": session["presentation"]["mode"]})
        stop(session)
    except (api.ApiError, AssertionError, urllib.error.URLError) as err:
        rec.case("offline-refusal", False, {"error": getattr(err, "code", str(err))})

    # 7. Same-size, same-mtime replacement: the old session refuses, a new one
    #    uses the newly confirmed object.
    try:
        session = start("replace", {"mode": "original"})
        grant = grant_path(session["presentation"]["url"], client.base)
        got = fetch(client, grant, {"Range": "bytes=0-65535"})[1]
        target = os.path.join(args.media, "replace.mp4")
        before = ctl(args.control, token, "GET", "/state")["files"]["replace.mp4"]
        next_path = os.path.join(args.media, "replace-next.mp4")
        data = bytearray(open(target, "rb").read())
        data[1024] ^= 0xFF
        open(next_path, "wb").write(bytes(data))
        after = ctl(args.control, token, "POST", "/replace",
                    {"name": "replace.mp4", "source": "/media/replace-next.mp4",
                     "keepSize": True, "keepMtime": True})
        old_refused = False
        try:
            fetch(client, grant, {"Range": "bytes=65536-131071"})
        except api.ApiError:
            old_refused = True
        new_session = start("replace", {"mode": "original"})
        fresh = fetch(client, grant_path(new_session["presentation"]["url"], client.base),
                      {"Range": "bytes=0-65535"})[1]
        rec.case("same-size-mtime-replacement",
                 old_refused and before["etag"] != after["etag"]
                 and before["size"] == after["size"] and before["mtimeNs"] == after["mtimeNs"]
                 and hashlib.sha256(fresh).hexdigest() == hashlib.sha256(bytes(data)[:len(fresh)]).hexdigest(),
                 {"sizePreserved": before["size"] == after["size"],
                  "mtimePreserved": before["mtimeNs"] == after["mtimeNs"],
                  "etagChanged": before["etag"] != after["etag"],
                  "oldSessionRefused": old_refused, "freshProbeBytes": len(fresh)})
        stop(session)
        stop(new_session)
    except (api.ApiError, AssertionError, urllib.error.URLError, KeyError) as err:
        rec.case("same-size-mtime-replacement", False, {"error": getattr(err, "code", str(err))})

    # 8. Controlled DNS change to an unapproved private address is refused before
    #    contact; restoring the record restores playback.
    try:
        mark = origin.mark()
        with open(args.zone, "w", encoding="utf-8") as handle:
            json.dump({"media.portico.test": "10.0.0.66"}, handle)
        time.sleep(0.5)
        try:
            start("direct", {"mode": "original"})
            rec.case("dns-change-refused", False, {"error": "start unexpectedly accepted"})
        except api.ApiError as err:
            rec.case("dns-change-refused", not origin.since(mark),
                     {"code": err.code, "originHits": [h["path"] for h in origin.since(mark)]})
        with open(args.zone, "w", encoding="utf-8") as handle:
            json.dump({"media.portico.test": "172.30.67.10"}, handle)
        time.sleep(0.5)
        session = start("direct", {"mode": "original"})
        rec.case("dns-recovered", session["presentation"]["mode"] == "direct", {})
        stop(session)
    except (api.ApiError, AssertionError, urllib.error.URLError) as err:
        rec.case("dns-change-refused", False, {"error": getattr(err, "code", str(err))})

    # 9. Changing library approvals ends remote sessions; grants stop working.
    try:
        session = start("remux", {"mode": "limit", "maxHeight": 720})
        grant = grant_path(session["presentation"]["url"], client.base)
        playlist, _, segments = playlist_segments(client, grant)
        fetch(client, urllib.parse.urljoin(playlist, segments[0])) if segments else None
        client.request("POST", "/v1/libraries/%s/network-roots" % library_id,
                       {"roots": ["http://unused.portico.test:9/"]}, expect=(200,))
        stopped = True
        try:
            fetch(client, grant_path(session["presentation"]["url"], client.base))
            stopped = False
        except api.ApiError:
            stopped = True
        refused = False
        try:
            start("direct", {"mode": "original"})
        except api.ApiError:
            refused = True
        rec.case("approval-change-ends-sessions", stopped and refused,
                 {"grantStopped": stopped, "newStartRefused": refused})
        client.request("POST", "/v1/libraries/%s/network-roots" % library_id,
                       {"roots": [HTTP_ORIGIN + "/", HTTPS_ORIGIN + "/"]}, expect=(200,))
    except (api.ApiError, AssertionError, urllib.error.URLError, IndexError) as err:
        rec.case("approval-change-ends-sessions", False, {"error": getattr(err, "code", str(err))})

    if client._session_bridge is not None:
        try:
            client._session_bridge.close()
            rec.case('canonical-session-cleanup', True, {})
        except api.ApiError:
            rec.case('canonical-session-cleanup', False, {'code': 'stop_failed'})
    failed = [c for c in rec.cases if not c["passed"] and not c["blocked"]]
    receipt = {"phase": "g09-strm", "base": client.base, "cases": rec.cases,
               "failed": len(failed), "result": "FAIL" if failed else "PASS",
               "finishedAt": time.time()}
    fd = os.open(args.out, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(receipt, handle, indent=2)
        handle.write("\n")
    for case in rec.cases:
        print(("PASS " if case["passed"] else "FAIL ") + case["case"])
    print(receipt["result"])
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
