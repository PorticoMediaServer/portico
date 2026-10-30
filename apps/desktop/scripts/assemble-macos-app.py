#!/usr/bin/env python3
"""Assemble an unsigned/ad-hoc development Portico.app from the checked desktop inputs.

The retained Tauri executable embeds the web frontend at build time and carries a linker
ad-hoc signature; stage-native-macos.py gives every staged dylib its own local ad-hoc
signature. This assembler writes the layout the shell expects (Contents/MacOS/<product>,
Contents/Resources/native/libmpv.2.dylib) after re-verifying the pinned lock, the native
manifest's source cohort and every recorded byte. It never mutates an input.

No web files are copied into the bundle: with tauri/custom-protocol the frontend is already
embedded, so the web root is a correspondence input only. Every web-root entry must appear
as an embedded asset key in the executable; that check proves the retained web root
corresponds to the frontend embedded in the retained binary, not that the embedded content
bytes equal the web-root files.

Info.plist carries LSMinimumSystemVersion only when tauri.conf.json sets
bundle.macOS.minimumSystemVersion; otherwise the key is omitted rather than guessed.

No Developer ID, notarization, universal or release claim. --ad-hoc-sign optionally seals
the finished bundle with `codesign --force --sign -` (no keychain credentials).
"""
import argparse
import hashlib
import json
import os
import pathlib
import plistlib
import re
import shutil
import stat
import struct
import subprocess
import zipfile

ABI = {"avcodec": 62, "avformat": 62, "avutil": 60, "avfilter": 11,
       "avdevice": 62, "swscale": 9, "swresample": 6}
CPU_TYPE_ARM64 = 0x0100000C
MH_EXECUTE = 2
MH_DYLIB = 6
SYSTEM_CODESIGN = "/usr/bin/codesign"
SHA256 = re.compile(r"[a-f0-9]{64}")
DYLIB = re.compile(r"lib[A-Za-z0-9_.+-]+\.dylib")
MANIFEST = "native-manifest.json"
WEB_MAX_FILES = 4096
WEB_MAX_BYTES = 268435456


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_json(path, label):
    try:
        value = json.loads(path.read_text())
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError("cannot read the " + label + ": " + str(path)) from error
    if not isinstance(value, dict):
        raise ValueError("the " + label + " is not a JSON object: " + str(path))
    return value


def macho(path):
    """Return (cpuType, cpuSubtype, fileType) for a thin 64-bit little/big-endian Mach-O."""
    try:
        with path.open("rb") as source:
            header = source.read(32)
    except OSError as error:
        raise ValueError("cannot read Mach-O input: " + path.name) from error
    if len(header) < 32:
        raise ValueError("not a 64-bit Mach-O file: " + path.name)
    if header[:4] == b"\xcf\xfa\xed\xfe":
        endian = "<"
    elif header[:4] == b"\xfe\xed\xfa\xcf":
        endian = ">"
    else:
        raise ValueError("not a thin 64-bit Mach-O file: " + path.name)
    cputype, cpusubtype, filetype = struct.unpack(endian + "III", header[4:16])
    return cputype, cpusubtype, filetype


def require_arm64(path, filetype):
    cputype, cpusubtype, actual = macho(path)
    if cputype != CPU_TYPE_ARM64:
        raise ValueError("not an arm64 Mach-O file: " + path.name)
    if actual != filetype:
        raise ValueError("unexpected Mach-O file type for " + path.name + ": " + str(actual))
    return cpusubtype


def require_path(path, label, directory=False):
    if not path.is_absolute():
        raise ValueError("all inputs and the output must use absolute paths: " + label)
    if path.is_symlink() and not path.exists():
        raise ValueError("missing " + label + ": " + str(path))
    if directory:
        if not path.is_dir():
            raise ValueError("missing " + label + " directory: " + str(path))
    elif not path.is_file():
        raise ValueError("missing " + label + ": " + str(path))


def verified_lock(lock):
    if lock.get("schemaVersion") != 2:
        raise ValueError("the native dependency lock schema is not 2")
    ffmpeg = lock.get("ffmpeg")
    limits = lock.get("packageLimits")
    if not isinstance(ffmpeg, dict) or not isinstance(limits, dict):
        raise ValueError("the native dependency lock is incomplete")
    if ffmpeg.get("sharedAbi") != ABI:
        raise ValueError("the lock does not pin the desktop FFmpeg ABI")
    installed = limits.get("installedBytes")
    if not isinstance(installed, int) or isinstance(installed, bool) or installed <= 0:
        raise ValueError("the lock has no positive installed-size bound")
    for key in ("sourceCommit", "sourceSha256", "preparedReceiptSha256"):
        if not isinstance(ffmpeg.get(key), str) or not ffmpeg[key]:
            raise ValueError("the lock has no " + key)
    if not isinstance(ffmpeg.get("requiredPatches"), int) or ffmpeg["requiredPatches"] <= 0:
        raise ValueError("the lock has no required patch count")
    if not isinstance(ffmpeg.get("postSeriesPatches"), list):
        raise ValueError("the lock has no post-series patch list")
    return ffmpeg, installed


def verified_native(stage, ffmpeg, installed_limit):
    manifest_path = stage / MANIFEST
    if not manifest_path.is_file() or manifest_path.is_symlink():
        raise ValueError("the native stage has no regular " + MANIFEST)
    manifest = read_json(manifest_path, "native manifest")
    if manifest.get("status") != "development-only; unqualified for release":
        raise ValueError("the native stage is not the development-only build")
    if manifest.get("localSignature") != "ad-hoc; no Developer ID or notarization":
        raise ValueError("the native stage does not carry the expected local signature")
    if manifest.get("ffmpegAbi") != ABI:
        raise ValueError("the native stage does not carry the pinned FFmpeg ABI")
    receipt = manifest.get("sourceReceipt")
    if not isinstance(receipt, dict):
        raise ValueError("the native manifest has no embedded source receipt")
    if (receipt.get("sourceCommit") != ffmpeg["sourceCommit"]
            or receipt.get("sourceSha256") != ffmpeg["sourceSha256"]):
        raise ValueError("the native source does not match the pinned lock cohort")
    patches = receipt.get("patches")
    if (not isinstance(patches, list) or len(patches) != ffmpeg["requiredPatches"]
            or any(not isinstance(patch, dict) or not SHA256.fullmatch(str(patch.get("sha256")))
                   for patch in patches)):
        raise ValueError("the native source patches do not match the pinned lock cohort")
    if receipt.get("postSeriesPatches") != ffmpeg["postSeriesPatches"]:
        raise ValueError("the native post-series patches do not match the pinned lock cohort")
    if manifest.get("sourceReceiptSha256") != ffmpeg["preparedReceiptSha256"]:
        raise ValueError("the native prepared receipt does not match the pinned lock cohort")
    installed = manifest.get("installedBytes")
    if not isinstance(installed, int) or isinstance(installed, bool) or not 0 < installed <= installed_limit:
        raise ValueError("the native manifest exceeds the pinned installed-size bound")
    files = manifest.get("files")
    if not isinstance(files, list) or not files:
        raise ValueError("the native manifest lists no runtime files")
    records = []
    seen = set()
    total = 0
    for record in files:
        if not isinstance(record, dict):
            raise ValueError("the native manifest has a malformed file record")
        name = record.get("name")
        size = record.get("bytes")
        digest = record.get("sha256")
        input_digest = record.get("inputSha256")
        if not isinstance(name, str) or not DYLIB.fullmatch(name) or name in seen:
            raise ValueError("the native manifest has an invalid or repeated file name")
        if not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise ValueError("the native manifest has an invalid size for " + name)
        if not SHA256.fullmatch(str(digest)) or not SHA256.fullmatch(str(input_digest)):
            raise ValueError("the native manifest has an invalid hash for " + name)
        if not isinstance(record.get("dependencies"), list):
            raise ValueError("the native manifest has no dependency list for " + name)
        source = stage / name
        if not source.is_file() or source.is_symlink():
            raise ValueError("missing staged native runtime file: " + name)
        if source.stat().st_size != size:
            raise ValueError("staged native size does not match the manifest: " + name)
        if sha256(source) != digest:
            raise ValueError("staged native bytes do not match the manifest: " + name)
        require_arm64(source, MH_DYLIB)
        seen.add(name)
        total += size
        if total > installed_limit:
            raise ValueError("the staged native closure exceeds the pinned installed-size bound")
        records.append((name, source, digest))
    if "libmpv.2.dylib" not in seen:
        raise ValueError("the native stage does not carry libmpv.2.dylib")
    expected = seen | {MANIFEST}
    actual = {entry.name for entry in stage.iterdir()}
    if actual != expected:
        unexpected = sorted(actual - expected)
        missing = sorted(expected - actual)
        raise ValueError("the native stage is not the exact checked closure; unexpected="
                         + repr(unexpected) + " missing=" + repr(missing))
    return manifest, records, total, sha256(manifest_path), manifest_path.stat().st_size


def web_name(value):
    normalized = pathlib.PurePosixPath(value)
    if (not value or value != normalized.as_posix() or normalized.is_absolute()
            or ".." in normalized.parts or "\\" in value):
        raise ValueError("unsafe web-root entry name: " + repr(value))
    return normalized.as_posix()


def directory_entries(root):
    entries = []
    for directory, dirnames, filenames in os.walk(root, followlinks=False):
        base = pathlib.Path(directory)
        for name in dirnames:
            if (base / name).is_symlink():
                raise ValueError("the web root contains a symbolic link: " + name)
        for name in filenames:
            entry = base / name
            if entry.is_symlink() or not entry.is_file():
                raise ValueError("the web root contains a non-regular entry: " + name)
            entries.append((web_name(entry.relative_to(root).as_posix()), entry))
    return entries


def zip_entries(archive):
    entries = []
    seen = set()
    with zipfile.ZipFile(archive) as source:
        for info in source.infolist():
            if info.is_dir():
                web_name(info.filename.rstrip("/"))
                continue
            name = web_name(info.filename)
            if name in seen:
                raise ValueError("the web archive repeats an entry: " + name)
            if info.flag_bits & 0x1:
                raise ValueError("the web archive contains an encrypted entry: " + name)
            mode = info.external_attr >> 16
            if stat.S_IFMT(mode) not in (0, stat.S_IFREG):
                raise ValueError("the web archive contains a non-regular entry: " + name)
            seen.add(name)
            entries.append((name, info))
    return entries


def verified_web_root(path, binary_bytes):
    if path.is_dir():
        kind = "directory"
        entries = directory_entries(path)
        if len(entries) > WEB_MAX_FILES:
            raise ValueError("the web root exceeds the entry bound")
        if sum(entry.stat().st_size for _, entry in entries) > WEB_MAX_BYTES:
            raise ValueError("the web root exceeds the size bound")
        material = [(name, sha256(entry)) for name, entry in entries]
    elif path.is_file() and path.suffix == ".zip":
        kind = "zip"
        entries = zip_entries(path)
        if len(entries) > WEB_MAX_FILES:
            raise ValueError("the web root exceeds the entry bound")
        if sum(info.file_size for _, info in entries) > WEB_MAX_BYTES:
            raise ValueError("the web root exceeds the size bound")
        material = []
        with zipfile.ZipFile(path) as source:
            for name, info in entries:
                material.append((name, hashlib.sha256(source.read(info)).hexdigest()))
    else:
        raise ValueError("the web root must be an existing directory or .zip file")
    material.sort()
    if not any(name == "index.html" for name, _ in material):
        raise ValueError("the web root has no index.html")
    # Asset-key presence proves correspondence to these files, not byte equality with the
    # frontend embedded in the executable.
    for name, _ in material:
        if b"/" + name.encode() not in binary_bytes:
            raise ValueError("a web-root entry is not embedded in the executable: " + name)
    digest = hashlib.sha256(b"".join(name.encode() + b"\0" + value.encode() + b"\n"
                                     for name, value in material)).hexdigest()
    return kind, len(material), digest


def app_identity(config):
    product = config.get("productName")
    version = config.get("version")
    identifier = config.get("identifier")
    if not isinstance(product, str) or not product or "/" in product or ":" in product:
        raise ValueError("tauri.conf.json has no usable productName")
    if not isinstance(version, str) or not re.fullmatch(r"[0-9A-Za-z.+-]+", version):
        raise ValueError("tauri.conf.json has no usable version")
    if not isinstance(identifier, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.-]*", identifier):
        raise ValueError("tauri.conf.json has no usable identifier")
    bundle = config.get("bundle") if isinstance(config.get("bundle"), dict) else {}
    category = None
    raw_category = bundle.get("category")
    if isinstance(raw_category, str) and raw_category.strip():
        slug = raw_category.strip().lower().replace(" ", "-")
        if re.fullmatch(r"[a-z0-9-]+", slug):
            category = "public.app-category." + slug
    copyright_text = bundle.get("copyright")
    if not isinstance(copyright_text, str) or not copyright_text.strip():
        copyright_text = None
    macos_bundle = bundle.get("macOS") if isinstance(bundle.get("macOS"), dict) else {}
    raw_minimum = macos_bundle.get("minimumSystemVersion")
    if raw_minimum is None:
        minimum_system = None
    elif isinstance(raw_minimum, str) and raw_minimum.strip():
        minimum_system = raw_minimum.strip()
    else:
        raise ValueError("tauri.conf.json has no usable bundle.macOS.minimumSystemVersion")
    return product, version, identifier, category, copyright_text, minimum_system


def info_plist(product, version, identifier, category, copyright_text, minimum_system):
    info = {
        "CFBundleDevelopmentRegion": "en",
        "CFBundleDisplayName": product,
        "CFBundleExecutable": product,
        "CFBundleIdentifier": identifier,
        "CFBundleInfoDictionaryVersion": "6.0",
        "CFBundleName": product,
        "CFBundlePackageType": "APPL",
        "CFBundleShortVersionString": version,
        "CFBundleVersion": version,
        "NSHighResolutionCapable": True,
        "NSPrincipalClass": "NSApplication",
    }
    if minimum_system:
        info["LSMinimumSystemVersion"] = minimum_system
    if category:
        info["LSApplicationCategoryType"] = category
    if copyright_text:
        info["NSHumanReadableCopyright"] = copyright_text
    return info


def input_state(binary, web_root, stage, lock_path, config_path):
    return (sha256(binary), sha256(lock_path), sha256(config_path),
            tuple(sorted((entry.name, sha256(entry)) for entry in stage.iterdir())),
            verified_web_root(web_root, binary.read_bytes())[2])


def ad_hoc_sign(bundle, codesign):
    for arguments in (["--force", "--sign", "-", "--timestamp=none", str(bundle)],
                      ["--verify", "--strict", str(bundle)]):
        try:
            result = subprocess.run([codesign] + arguments, capture_output=True, text=True)
        except OSError as error:
            raise ValueError("cannot run the system codesign: " + str(error)) from error
        if result.returncode:
            raise ValueError("ad-hoc signing failed: " + result.stderr.strip()[-400:])


def assemble(binary, web_root, native_stage, lock_path, tauri_config, output,
             ad_hoc_sign_bundle=False, codesign=SYSTEM_CODESIGN):
    inputs = [("binary", binary, False), ("web root", web_root, None),
              ("native stage", native_stage, True), ("dependency lock", lock_path, False),
              ("tauri configuration", tauri_config, False)]
    for label, path, directory in inputs:
        if not path.is_absolute():
            raise ValueError("all inputs and the output must use absolute paths: " + label)
        if directory is None:
            if not (path.is_dir() or path.is_file()):
                raise ValueError("missing " + label + ": " + str(path))
        else:
            require_path(path, label, directory)
    require_path(output.parent, "output parent", True)
    if output.exists() or output.is_symlink():
        raise ValueError("output must be a new path: " + str(output))
    if output.suffix != ".app":
        raise ValueError("output must name a new .app bundle")
    resolved_output = output.resolve()
    for label, path, _ in inputs:
        resolved = path.resolve()
        if (resolved_output == resolved or resolved.is_relative_to(resolved_output)
                or resolved_output.is_relative_to(resolved)):
            raise ValueError("the output overlaps the " + label)
    before = input_state(binary, web_root, native_stage, lock_path, tauri_config)

    lock = read_json(lock_path, "native dependency lock")
    ffmpeg, installed_limit = verified_lock(lock)
    manifest, records, total, manifest_digest, manifest_size = verified_native(
        native_stage, ffmpeg, installed_limit)
    binary_bytes = binary.read_bytes()
    binary_digest = hashlib.sha256(binary_bytes).hexdigest()
    require_arm64(binary, MH_EXECUTE)
    web_kind, web_files, web_digest = verified_web_root(web_root, binary_bytes)
    product, version, identifier, category, copyright_text, minimum_system = app_identity(
        read_json(tauri_config, "tauri configuration"))
    info = info_plist(product, version, identifier, category, copyright_text, minimum_system)

    partial = output.with_name(output.name + ".partial-" + str(os.getpid()))
    if partial.exists() or partial.is_symlink():
        raise ValueError("the assembly scratch path already exists: " + str(partial))
    published = False
    try:
        contents = partial / "Contents"
        macos = contents / "MacOS"
        native = contents / "Resources" / "native"
        native.mkdir(parents=True)
        executable = macos / product
        executable.parent.mkdir(parents=True)
        shutil.copyfile(binary, executable)
        executable.chmod(0o755)
        if sha256(executable) != binary_digest:
            raise ValueError("the copied executable does not match the input")
        info_path = contents / "Info.plist"
        with info_path.open("wb") as stream:
            plistlib.dump(info, stream, sort_keys=True)
        (contents / "PkgInfo").write_bytes(b"APPL????")
        for name, source, digest in records:
            installed = native / name
            shutil.copyfile(source, installed)
            installed.chmod(0o755)
            if sha256(installed) != digest:
                raise ValueError("a copied native runtime file does not match the manifest: " + name)
        shutil.copyfile(native_stage / MANIFEST, native / MANIFEST)
        if sha256(native / MANIFEST) != manifest_digest:
            raise ValueError("the copied native manifest does not match the input")
        receipt = {
            "schemaVersion": 1,
            "status": "development-only; unsigned/ad-hoc; unqualified for release",
            "product": product, "version": version, "identifier": identifier,
            "nativeAbi": ABI,
            "localSignature": "ad-hoc; no Developer ID or notarization",
            "adHocSignedBundle": bool(ad_hoc_sign_bundle),
            "inputs": {
                "binary": {"name": binary.name, "bytes": len(binary_bytes), "sha256": binary_digest},
                "dependencyLock": {"name": lock_path.name, "sha256": sha256(lock_path)},
                "tauriConfiguration": {"name": tauri_config.name, "sha256": sha256(tauri_config)},
                "nativeManifest": {"name": MANIFEST, "bytes": manifest_size, "sha256": manifest_digest},
                "nativeFiles": len(records), "nativeBytes": total,
                "webRoot": {"name": web_root.name, "kind": web_kind,
                            "files": web_files, "sha256": web_digest,
                            "correspondence": "asset-key presence only; embedded bytes are not compared"},
            },
        }
        if minimum_system:
            receipt["minimumSystemVersion"] = minimum_system
        receipt_path = contents / "Resources" / "assembly-receipt.json"
        receipt_path.write_text(json.dumps(receipt, indent=2, sort_keys=True) + "\n")
        if ad_hoc_sign_bundle:
            ad_hoc_sign(partial, codesign)
        if input_state(binary, web_root, native_stage, lock_path, tauri_config) != before:
            raise ValueError("an input changed during assembly; nothing was published")
        os.rename(partial, output)
        published = True
        print(json.dumps({"app": str(output), "product": product, "version": version,
                          "identifier": identifier, "nativeFiles": len(records),
                          "nativeBytes": total, "webFiles": web_files,
                          "adHocSigned": bool(ad_hoc_sign_bundle)}))
    finally:
        if not published and partial.exists():
            shutil.rmtree(partial)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", required=True, type=pathlib.Path)
    parser.add_argument("--web-root", required=True, type=pathlib.Path)
    parser.add_argument("--native-stage", required=True, type=pathlib.Path)
    parser.add_argument("--lock", required=True, type=pathlib.Path)
    parser.add_argument("--tauri-config", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--ad-hoc-sign", action="store_true")
    args = parser.parse_args(argv)
    try:
        assemble(args.binary, args.web_root, args.native_stage, args.lock,
                 args.tauri_config, args.output, args.ad_hoc_sign)
    except ValueError as error:
        parser.error(str(error))


if __name__ == "__main__":
    main()
