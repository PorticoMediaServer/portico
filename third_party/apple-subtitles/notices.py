"""Extract notices only from checksum-verified pinned archives; retain corresponding source."""
import hashlib
import json
from pathlib import Path
import shutil
import sys
import tarfile

lock_path, downloads, out = map(Path, sys.argv[1:])
lock = json.loads(lock_path.read_text())
recipe = lock_path.parent
ffmpeg_lock = recipe / lock['components']['ffmpeg']['sourceLock']
checked = json.loads(ffmpeg_lock.read_text())
receipt = downloads.parent / 'src/ffmpeg/portico-source-receipt.json'
expected_receipt = {'sourceCommit': checked['ffmpeg']['commit'], 'sourceSha256': checked['ffmpeg']['sha256'],
                    'patches': checked['ffmpeg']['patches'], 'sourceDateEpoch': checked['sourceDateEpoch']}
if json.loads(receipt.read_text()) != expected_receipt:
    raise SystemExit('Corresponding source has no matching checked Jellyfin patch receipt')
notices = out / "notices"
source = out / "source"
notices.mkdir(parents=True, exist_ok=True)
source.mkdir(parents=True, exist_ok=True)
combined = ["Portico Apple subtitles — third-party notices\n"]
for name, entry in lock["components"].items():
    archive = downloads / entry["file"]
    if hashlib.sha256(archive.read_bytes()).hexdigest() != entry["sha256"]:
        raise SystemExit(f"Checksum mismatch: {name}")
    shutil.copyfile(archive, source / archive.name)
    combined.append(f"\n{name} {entry['version']} — {entry['licence']}\n{entry['url']}\n")
    with tarfile.open(archive) as tar:
        members = {m.name.split("/", 1)[-1]: m for m in tar.getmembers() if m.isfile()}
        for licence in entry["licenceFiles"]:
            if licence not in members:
                raise SystemExit(f"Missing licence: {name}/{licence}")
            data = tar.extractfile(members[licence]).read()
            dest = notices / name / licence
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(data)
            combined.append(data.decode("utf-8", errors="replace"))
shutil.copyfile(lock_path, source / "sources.lock.json")
shutil.copyfile(ffmpeg_lock, source / ffmpeg_lock.name)
shutil.copyfile(receipt, source / receipt.name)
combined.append('\nFFmpeg uses the checked Jellyfin source and all recorded patches. GPL-3.0-or-later.\n'
                'The source archive, full patch lock, application receipt and build recipe are in source/.\n')
(notices / "THIRD-PARTY-NOTICES.txt").write_text("\n".join(combined))
with tarfile.open(source / "apple-subtitle-recipe.tar", "w") as tar:
    for path in sorted(recipe.rglob("*")):
        if path.is_file() and not set(path.relative_to(recipe).parts) & {"work", "out", "Frameworks", "__pycache__"}:
            tar.add(path, arcname=str(path.relative_to(recipe)), recursive=False)
(source / "SHA256SUMS").write_text("".join(f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n" for p in sorted(source.iterdir()) if p.is_file() and p.name != "SHA256SUMS"))
