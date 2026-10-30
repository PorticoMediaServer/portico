"""Fetch only the locked fixture tool inside its owner's disposable directory."""

import hashlib
import json
import pathlib
import signal
import sys
import time
import urllib.error
import urllib.request
import zipfile


def fetch(lock_path, root, *, open_url=urllib.request.urlopen, clock=time.monotonic):
    lock = json.loads(pathlib.Path(lock_path).read_text())["targets"]["linux-amd64"]
    root = pathlib.Path(root)
    archive = root / "rclone.zip"
    archive_owned = False
    # Artifact setup has a wall-clock bound of its own; it is not a media
    # provider Retry-After, request deadline or permission to reroute a test.
    deadline = clock() + 120
    urls = [lock["url"], "https://github.com/rclone/rclone/releases/download/v1.75.2/rclone-v1.75.2-linux-amd64.zip"]
    try:
        for index, url in enumerate(urls):
            remaining = deadline - clock()
            if remaining <= 0:
                raise RuntimeError("locked rclone acquisition exceeded 120 seconds")
            try:
                request = urllib.request.Request(url, headers={"User-Agent": "Portico-test-dependency/1"})
                digest, total = hashlib.sha256(), 0
                with open_url(request, timeout=min(30, remaining)) as source, archive.open("xb") as target:
                    archive_owned = True
                    while True:
                        if clock() >= deadline:
                            raise RuntimeError("locked rclone acquisition exceeded 120 seconds")
                        # One network read returns partial progress rather than
                        # waiting for an entire MiB while a server trickles data.
                        chunk = source.read1(1 << 20)
                        if clock() >= deadline:
                            raise RuntimeError("locked rclone acquisition exceeded 120 seconds")
                        if not chunk:
                            break
                        total += len(chunk)
                        if total > 64 << 20:
                            raise RuntimeError("locked rclone archive exceeds 64 MiB")
                        digest.update(chunk)
                        target.write(chunk)
                break
            except urllib.error.HTTPError as error:
                if error.code != 403 or index != 0:
                    raise
                if archive_owned:
                    archive.unlink(missing_ok=True)
                    archive_owned = False
                print("Canonical archive returned 403; using the same-hash official release asset.", flush=True)
        if digest.hexdigest() != lock["sha256"]:
            raise RuntimeError("locked rclone archive digest mismatch")
        with zipfile.ZipFile(archive) as package:
            data = package.read(lock["archiveRoot"] + "/" + lock["binary"])
        if hashlib.sha256(data).hexdigest() != lock["files"][lock["binary"]]:
            raise RuntimeError("locked rclone executable digest mismatch")
        target = root / "rclone"
        with target.open("xb") as output:
            output.write(data)
        target.chmod(0o700)
        print("Official locked rclone archive and executable verified; no owner state used.", flush=True)
        return target
    finally:
        if archive_owned:
            archive.unlink(missing_ok=True)


if __name__ == "__main__":
    def expired(_signal, _frame):
        raise RuntimeError("locked rclone acquisition exceeded 120 seconds")

    # This fixture entry point is Linux-only. The alarm also bounds a blocked
    # DNS/TLS/socket call; progress cannot extend the overall setup deadline.
    signal.signal(signal.SIGALRM, expired)
    signal.alarm(120)
    try:
        fetch(sys.argv[1], sys.argv[2])
    finally:
        signal.alarm(0)
