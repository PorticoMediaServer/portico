These are procedural Dolby Vision RPU metadata fixtures, generated with the pinned public dovi_tool dolby_vision library. No supplied licensed sample or picture is included. The generated output and Portico recipe use the repository GPL-3.0-only licence; the generator dependency is MIT (exact source and licence hashes in third_party/ffmpeg/portico-libdovi-source.json).

The ten sets each contain 24 Annex B RPUs: profiles 5, 8.1, 8.4, generated profile 7 MEL and generated profile 7 FEL metadata, each in CM 2.9 and 4.0. Profile 7 starts from a generated 8.1 RPU, applies the library's ToMel conversion, and for FEL uses a nonzero NLQ residual slope. This is valid parser/BSF metadata, not a mastered FEL picture or residual reconstruction proof. Actual BL/EL video joins and target playback are separate gates.

Regenerate into a new directory (do not overwrite original fixtures):

    python3 scripts/generate-dovi-metadata.py --dovi-source VERIFIED_SOURCE --dovi-archive VERIFIED_ARCHIVE --scratch Portico/.scratch/LANE/dovi-generator --output NEW_DIRECTORY

The script verifies the archive SHA and extracted crate bytes, uses the committed dependency lock with offline Cargo and one compiler worker, and records hashes without machine paths. It reparses serialized bytes to check profile, CM version, MEL/FEL class and CRC rejection, and checks P7-to-8.1 removes residual dependency. Compare every generated file and the manifest before replacing these fixtures.
