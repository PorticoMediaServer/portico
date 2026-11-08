# EC5 canonical population screening

`ci/ec5_memory.py` can run the existing EC5 paired or sustained native target
against one already-built QL canonical population artifact. This is a
screening transport, not a measurement, fit verdict or qualification. It adds
no second fixture, issuer, connection opener, resource profile or permit.

## Invocation

Both coordinates are optional and must be supplied together:

```sh
sudo --non-interactive python3 -B ci/ec5_memory.py \
  --binary /absolute/path/to/writer.test \
  --output /absolute/private/evidence \
  --target paired \
  --config /absolute/path/to/paired-config.json \
  --population-directory /absolute/private/population \
  --population-manifest-sha256 TRUSTED_64_LOWERCASE_HEX
```

`--target sustained` accepts the same pair. Omitting both coordinates keeps the
existing legacy tiny control behavior for every target. `--target control`
rejects any supplied population.

The manifest SHA must come from the independently reviewed artifact receipt,
not from the file being checked. The directory must already be absolute.

## Validation and forwarding

The controller validates the pair before it creates or changes any cgroup:

- exactly one coordinate supplied fails;
- a relative population directory fails;
- anything other than 64 lowercase hexadecimal characters fails;
- any population supplied with the control target fails.

On a paired or sustained target it forwards only these child variables:

```text
PORTICO_PAIRED_POPULATION_DIRECTORY
PORTICO_PAIRED_POPULATION_MANIFEST_SHA256
```

The controller never opens the artifact, reads its manifest, opens SQLite or
executes SQL. The Go test owner remains responsible for calling the existing
`UseQualificationPopulation(t, dir, pin)` immediately before its existing
canonical constructor. The child therefore verifies the trusted manifest pin,
copies the immutable image for the same test owner, and keeps the current
opener/policy unchanged.

## Unchanged EC5 invariants

This extension does not change the assigned 2,000,000,000-byte cap, zero swap,
runner permissions, child identity, opener, issuer or profile. The controller
still creates only its unique `portico-ec5-<uuid>` leaf, writes
`cgroup.kill` only to that leaf, joins the child and populated event, removes
only that leaf with `rmdir`, and cleans up on signals and errors. Raw
sample/final comparison fields remain `qualification: false` with unavailable
whole-server fit.

## Focused controls

Pure Python controls, no root or native workload:

```sh
python3 -B -m unittest discover -s ci -p test_ec5_memory.py
```

The actual cgroup/native control remains the fixed GitHub `lane-focused.yml`
`ec5-memory` harness on Ubuntu with Go1.26.10. A local unit pass is protocol
evidence only and never establishes qualification.
