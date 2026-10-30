#!/usr/bin/env python3
"""Generates the gapless conformance fixtures (spec §18.8). macOS only: the AAC
and ALAC files are Apple's own encodes (afconvert), which carry iTunSMPB the way
real iTunes/Music files do. Needs ffmpeg/ffprobe with libmp3lame and libopus.

Every file is a stretch of one known signal, 0.5*sin(2*pi*441*n/rate) on both
channels, so an engine checks its output against the formula: the frame count
must be exact and each sample within the sidecar's tolerance (lossless 1e-4,
lossy 0.05, and within 64 frames of either end 0.1 for lossy codecs, whose
decoders ring at a cut that isn't frame-aligned). The 12-track album is one continuous signal cut at points that
are not codec-frame aligned, so a gap or overlap at any join shows as a phase
error. Sidecars say what the server's audio facts must find (spec §18.7):
durationFrames, trim {startFrames, endFrames, source}, relative to the raw
decoded output (every frame of every packet, before any signaled skip).

Run from this directory: ./generate.py (rewrites everything).
"""
import json, os, re, shutil, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
FREQ, AMP = 441, 0.5

def run(*args, capture=False):
    r = subprocess.run(args, check=True, capture_output=True)
    return r.stdout if capture else None

def source(path, rate, offset, frames):
    expr = f"{AMP}*sin(2*PI*{FREQ}*(n+{offset})/{rate})"
    run("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", f"aevalsrc={expr}|{expr}:s={rate}:n=4096",
        "-af", f"atrim=end_sample={frames}", "-c:a", "pcm_s24le" if rate > 48000 else "pcm_s16le", path)

def packets(path):
    """Raw frames and signaled trim, as the server reads them (spec §18.7): the raw
    total is every decoded frame with skipping off (skip_manual); the trims are
    the first packet's skip_samples and the last packet's discard_padding (how
    FFmpeg surfaces the LAME header, MP4 edit lists and Opus pre-skip)."""
    out = json.loads(run("ffprobe", "-v", "error", "-flags2", "+skip_manual", "-select_streams", "a:0", "-show_streams", "-show_packets", "-show_frames",
                         "-show_entries", "stream=sample_rate:packet=duration:packet_side_data=skip_samples,discard_padding:frame=nb_samples",
                         "-of", "json", path, capture=True))
    rate = int(out["streams"][0]["sample_rate"])
    both = out.get("packets_and_frames", [])
    frames = out.get("frames") or [x for x in both if x.get("type") == "frame"]
    pk = out.get("packets") or [x for x in both if x.get("type") == "packet"]
    raw = sum(int(f["nb_samples"]) for f in frames)
    start = sum(int(sd.get("skip_samples", 0)) for sd in pk[0].get("side_data_list", []))
    end = sum(int(sd.get("discard_padding", 0)) for sd in pk[-1].get("side_data_list", []))
    return rate, raw, start, end

def itunsmpb(path):
    tags = run("ffprobe", "-v", "error", "-show_entries", "format_tags=iTunSMPB:stream_tags=iTunSMPB", "-of", "default=nw=1:nk=1", path, capture=True).decode()
    m = re.search(r"\s*[0-9A-Fa-f]{8} ([0-9A-Fa-f]{8}) ([0-9A-Fa-f]{8}) ([0-9A-Fa-f]{16})", tags)
    return tuple(int(x, 16) for x in m.groups()) if m else None

def encode(wav, out, kind):
    ff = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", wav]
    if kind == "mp3": run(*ff, "-c:a", "libmp3lame", "-b:a", "192k", out)
    elif kind == "opus": run(*ff, "-c:a", "libopus", "-b:a", "128k", out)
    elif kind == "flac": run(*ff, "-c:a", "flac", out)
    elif kind == "wav": shutil.copyfile(wav, out)
    elif kind == "aac": run("afconvert", "-f", "m4af", "-d", "aac", "-b", "192000", wav, out)
    elif kind == "alac": run("afconvert", "-f", "m4af", "-d", "alac", wav, out)

SOURCES = {"mp3": ("lame", 0.05), "opus": ("opus-preskip", 0.05), "flac": ("flac", 1e-4), "wav": ("none", 1e-4), "aac": ("itunsmpb", 0.05), "alac": ("itunsmpb", 1e-4)}
EXT = {"mp3": "mp3", "opus": "opus", "flac": "flac", "wav": "wav", "aac": "m4a", "alac": "m4a"}

def fixture(name, kind, rate, offset, frames, tmp):
    wav = os.path.join(tmp, name + ".src.wav")
    source(wav, rate, offset, frames)
    out = os.path.join(HERE, name + "." + EXT[kind])
    encode(wav, out, kind)
    got_rate, raw, start, end = packets(out)
    smpb = itunsmpb(out) if kind in ("aac", "alac") else None
    if smpb:
        delay, _, total = smpb
        if total != frames or delay + total > raw:
            sys.exit(f"{name}: iTunSMPB {smpb} disagrees with {frames} frames (raw {raw})")
        start, end = delay, raw - delay - total
    if got_rate != rate or raw - start - end != frames:
        sys.exit(f"{name}: raw {raw} - start {start} - end {end} != {frames} (rate {got_rate})")
    sidecar = {"file": os.path.basename(out), "container": {"mp3": "mp3", "opus": "ogg", "flac": "flac", "wav": "wav", "aac": "mp4", "alac": "mp4"}[kind],
               "codec": {"wav": "pcm_s24le" if rate > 48000 else "pcm_s16le"}.get(kind, kind), "sampleRate": rate, "channels": 2,
               "durationFrames": frames, "rawFrames": raw, "trim": {"startFrames": start, "endFrames": end, "source": SOURCES[kind][0]},
               "signal": {"formula": "amplitude*sin(2*pi*frequency*(n+offset)/sampleRate)", "amplitude": AMP, "frequency": FREQ, "offset": offset},
               "tolerance": SOURCES[kind][1],
               # Lossy decoders ring at a cut that isn't frame-aligned: the first and last
               # edgeFrames are checked against edgeTolerance instead (F-title, Core Audio).
               "edgeFrames": 64, "edgeTolerance": 0.1 if SOURCES[kind][1] > 1e-3 else SOURCES[kind][1]}
    with open(os.path.join(HERE, name + ".json"), "w") as f:
        json.dump(sidecar, f, indent=1)
        f.write("\n")
    return sidecar

def main():
    os.chdir(HERE)
    for f in os.listdir(HERE):
        if f.endswith((".mp3", ".opus", ".flac", ".wav", ".m4a", ".json")):
            os.remove(os.path.join(HERE, f))
    tmp = os.path.join(HERE, ".tmp")
    os.makedirs(tmp, exist_ok=True)
    try:
        # Singles: 5.0137 s, deliberately not a whole number of codec frames.
        for name, kind, rate in [("single-mp3-lame", "mp3", 44100), ("single-aac-itunes", "aac", 44100), ("single-alac", "alac", 44100),
                                 ("single-opus", "opus", 48000), ("single-flac-44k16", "flac", 44100), ("single-flac-96k24", "flac", 96000),
                                 ("single-wav", "wav", 44100)]:
            fixture(name, kind, rate, 0, round(5.0137 * rate), tmp)
        # The album: 12 tracks of one continuous signal, cut off-frame, per codec
        # (lossless proves exact joins; lossy proves the trims).
        lengths = [110000 + i * 7919 for i in range(12)]
        album = {"tracks": {}, "note": "Each codec's tracks, in order, are one continuous signal: track k starts at offset sum(lengths[:k])."}
        for kind in ("flac", "mp3", "aac"):
            offset, names = 0, []
            for i, n in enumerate(lengths):
                name = f"album-{kind}-{i + 1:02d}"
                fixture(name, kind, 44100, offset, n, tmp)
                names.append(name)
                offset += n
            album["tracks"][kind] = names
        with open(os.path.join(HERE, "album.json"), "w") as f:
            json.dump(album, f, indent=1)
            f.write("\n")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

main()
