#!/usr/bin/env python3
"""Sync the checked Hosted identity transport leaves from immutable Git objects."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


FILES = {
    "claimweb/protocol.go": "claimweb/protocol.go",
    "claimweb/owner_start.go": "claimweb/owner_start.go",
    "claimweb/owner_start_test.go": "claimweb/owner_start_test.go",
    "claimweb/owner_switch_count.go": "claimweb/owner_switch_count.go",
    "claimweb/owner_switch_count_test.go": "claimweb/owner_switch_count_test.go",
    "claimweb/setup_start.go": "claimweb/setup_start.go",
    "claimweb/setup_start_test.go": "claimweb/setup_start_test.go",
    "claimweb/testdata/owner_start.json": "claimweb/testdata/owner_start.json",
    "claimweb/pending_request.go": "claimweb/pending_request.go",
    "claimweb/pending_request_test.go": "claimweb/pending_request_test.go",
    "internal/serverreturn/registration.go": "serverreturn/registration.go",
    "internal/serverreturn/transport_validation.go": "serverreturn/transport_validation.go",
    "internal/serverreturn/exchange.go": "serverreturn/exchange.go",
    "internal/serverreturn/owner_exchange.go": "serverreturn/owner_exchange.go",
    "internal/serverreturn/setup_exchange.go": "serverreturn/setup_exchange.go",
    "internal/serverreturn/setup_exchange_test.go": "serverreturn/setup_exchange_test.go",
    "internal/serverreturn/owner_exchange_test.go": "serverreturn/owner_exchange_test.go",
    "internal/wirejson/object.go": "wirejson/object.go",
}
IMPORTS = {
    b'"portico.local/hosted/internal/serverreturn"': b'"github.com/PorticoMediaServer/portico/server/internal/serverreturn"',
    b'"portico.local/hosted/internal/wirejson"': b'"github.com/PorticoMediaServer/portico/server/internal/wirejson"',
}


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-repo", required=True, type=Path)
    parser.add_argument("--source-commit", required=True)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--write", action="store_true")
    mode.add_argument("--check", action="store_true")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_commit):
        parser.error("source-commit must be an immutable full commit ID")
    if git(args.source_repo, "rev-parse", args.source_commit + "^{commit}").decode().strip() != args.source_commit:
        parser.error("source-commit did not resolve exactly")
    # The source working tree is never read. The checked commit must already
    # belong to the locally fetched origin/main, rather than a feature draft.
    subprocess.run(["git", "-C", str(args.source_repo), "merge-base", "--is-ancestor", args.source_commit, "refs/remotes/origin/main"], check=True)
    root = Path(__file__).resolve().parent.parent
    prefix = "hosted-services/hosted/"
    outputs = {}
    entries = []
    for source, target in FILES.items():
        source_path = prefix + source
        target_path = "server/internal/" + target
        raw = git(args.source_repo, "show", args.source_commit + ":" + source_path)
        mapped = raw
        for old, new in IMPORTS.items():
            mapped = mapped.replace(old, new)
        if b"portico.local/hosted/" in mapped:
            raise ValueError("unexpected private dependency in " + source_path)
        outputs[target_path] = mapped
        entries.append({
            "source": source_path,
            "target": target_path,
            "sourceBlob": git(args.source_repo, "rev-parse", args.source_commit + ":" + source_path).decode().strip(),
            "sourceSHA256": hashlib.sha256(raw).hexdigest(),
            "targetSHA256": hashlib.sha256(mapped).hexdigest(),
        })
    manifest = {
        "sourceRepository": "PorticoMediaServer/portico-internal",
        "sourceCommit": args.source_commit,
        "importMapping": {a.decode().strip('"'): b.decode().strip('"') for a, b in IMPORTS.items()},
        "files": entries,
    }
    outputs["server/internal/claimweb/source.json"] = (json.dumps(manifest, indent=2) + "\n").encode()
    stale = [path for path, raw in outputs.items() if not (root / path).is_file() or (root / path).read_bytes() != raw]
    if args.check and stale:
        raise ValueError("identity protocol sync differs: " + ", ".join(stale))
    if args.write:
        for path, raw in outputs.items():
            target = root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            temporary = target.with_name(target.name + ".sync-tmp")
            temporary.write_bytes(raw)
            temporary.replace(target)
    print(f"Identity protocol: {len(entries)} complete files from {args.source_commit}; {'current' if args.check else 'written'}")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
