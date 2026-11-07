# Shared qualification population (test-only)

This fixture supplies population, not command authority. It changes no production opener, schema, issuer, accepting command or profile. It is never a qualification verdict. Only reviewed, merged source and the accepted sampling protocol can produce qualification evidence.

`TestExportQualificationPopulation` creates one canonical population through `testCanonicalExecutor`, asserts the shared connection policy before and after seeding, closes the executor, and exports a closed database plus a manifest. A 100k-or-larger export requires the explicit hosted environment. Exact Go 1.26.10 and bundled SQLite 3.53.4 are required. The workflow and its input/artifact protocol are documented in `ci/QUALIFICATION-FIXTURE.md` at the repository root.

The source-bound seeder records a deterministic logical population hash. The exported image receives its own actual SHA-256 and byte length; canonical bootstrap incarnation bytes mean independently generated images are not promised to be byte-identical. Every shard downloads the SAME artifact from the pinned producer run, rather than rebuilding the population. The manifest binds its producer source, schema, seed, resident count, shape, engine/toolchain and actual policy. The consumer source and binary are separately pinned in each workload receipt.

## Owner binding

In a writer-package test, call `UseQualificationPopulation(t, absoluteArtifactDirectory, pinnedManifestSHA256)` immediately before the existing owner fixture constructor. The manifest digest must come from the trusted producer receipt, not a digest freshly computed from the same untrusted manifest. This registers the image for exactly the next canonical fixture clone owned by that exact `*testing.T`. A second use, repeated registration or unused registration fails. Subtests must attach their own image; a parent's registration does not silently flow to a child.

The fixture path makes a fresh exclusive mutable copy while verifying the image's entire length and SHA-256 with bounded buffering. Existing `testCanonicalExecutor` opens that copy through its unchanged connector/openPool/Assert path. The artifact itself is never opened or mutated in place. No SQL string, DSN, profile, budget, proof or authority enters through this API.

Class target owners must build on the populated library represented by the manifest. Existing tiny fixtures that unconditionally insert their own parent library are not interchangeable with this image. A target must explicitly bind its class-specific original, current state, admissions, owner clocks, actual returns and receipts; the population cannot supply them. Measuring a tiny independent target library beside one million unrelated rows is not target-library qualification.

## Coverage and evidence

The initial shape is a minimal current movie catalogue, with its exact source-bound table counts and deterministic IDs recorded in `facts`. It does not imply fully enriched metadata, every media kind, search postings/credits, physical media, accepted Basic probing, production seal proof or a FULL security recipe. A class whose worst legal state needs more data must add that state through its reviewed test-only owner composition and identify it in its receipt.

Population setup, validation and file copying are outside command HOLD. Targets keep their actual physical custody/COMMIT clocks and warm/cold/error strata; no population setup latency is subtracted from a measured command after the fact. All refusal/error/fence/unserved or unknown samples remain visible. Standard runner functional tests of this transport are not host-profile or timing qualification.
