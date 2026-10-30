These half-second stereo test tones contain 22,050 frames at 44,100 Hz. There are no recordings or third-party model weights. `manifest.json` pins every encoded input; `provenance.json` pins the source and test encoder tools. The tools are not runtime dependencies or redistributed binaries.

Reproduce `source.wav` with Python 3:

```python
import math, struct, wave
with wave.open("source.wav", "wb") as w:
    w.setnchannels(2)
    w.setsampwidth(2)
    w.setframerate(44100)
    w.writeframes(b"".join(struct.pack("<hh",
        int(16000 * math.sin(2 * math.pi * 997 * n / 44100)),
        int(12000 * math.sin(2 * math.pi * 1301 * n / 44100)))
        for n in range(22050)))
```

Using the pinned tools from disposable scratch, encode with `java -Xmx128m -jar jmac-1.74.jar c2 source.wav tone.ape`, `mppenc --silent source.wav tone-sv7.mpc`, and `mpcenc --silent source.wav tone-sv8.mpc`. The mppenc source-only build repair is recorded in provenance. The PCM source hash is the regeneration check; small libm/compiler differences can change generated sample rounding, so tests use the committed inputs and hashes.

The decode suite requires actual FFmpeg/ffprobe. It compares APE's lossless conversion sample-for-sample and every converted audible count with construction. SV7/SV8 tests complete exact raw trim from their bounded source headers. A scanner test must independently compose header duration into actual stored probe facts; decoder tests alone do not prove Basic scan integration.
