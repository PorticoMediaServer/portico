# Procedural Dolby Vision STN/PMT source fixtures

GPL-3.0-or-later, like the repository. All pictures, RPU and navigation are generated. No licensed real sample bytes are present.

Each folder is an actual tsMuxer-authored 66-second Blu-ray structure with a primary HEVC track and an explicit Dolby Vision EL STN entry. `stn-fel-bdmv` has no PMT dependency descriptor: EL membership is known and the base PID stays absent. `pmt-fel-bdmv` adds a declared profile7/compatibility6 dependency to the generated PMTs, with valid CRC. This authoring operation replaces only packet stuffing: PES bytes, packet positions, timestamps, CLPI and MPLS are unchanged.

The 320×180 base and 160×90 constant-gray enhancement pictures are small structural fixtures, not UHD-disc authoring conformance or meaningful FEL residual reconstruction. The source contains repeated one-second procedural sequences, not a continuous narrative. File duration, stream counts, chapters and dependency facts come from the actual helper output recorded in the manifest. The original three disc fixtures/manifest remain unchanged.

Reproduce with `scripts/generate-dovi-disc-fixtures.py --fixtures fixtures/video/hdr-generated --tsmuxer /exact/tsMuxeR --helper /exact/portico-disc --output /new/output --scratch /empty/lane/scratch`. The manifest pins input inventory, generator, exact tsMuxer executable and its release source commit. Host-library probe tests do not qualify canonical target archives, no-decryption closure, hardware, remote discs or complete playback.
