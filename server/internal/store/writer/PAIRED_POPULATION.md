# Paired N1 canonical population (test-only)

`attachN1PairedPopulation(t)` lets the paired N1/foreground component use an
already exported exact-one-million-resident `canonical-movies-v1` image instead
of the legacy tiny fixture. It supplies population only: no opener, issuer,
recipe, profile, command cap or qualification verdict changes.

## Environment

Both values are optional and must be present together:

- `PORTICO_PAIRED_POPULATION_DIRECTORY`: absolute directory containing the
  existing `population.sqlite` and `population.json` pair.
- `PORTICO_PAIRED_POPULATION_MANIFEST_SHA256`: the 64-character lowercase
  digest from the trusted producer receipt, not a digest recomputed from the
  manifest being checked.

Both absent or empty returns `nil`, preserving the legacy tiny component. One
present without the other, a relative directory or a malformed digest is fatal.
When enabled, the existing `readQualificationPopulation` verifies the actual
artifact and `Facts.Residents` must be exactly `1000000` before the existing
`UseQualificationPopulation` registers it for that exact `*testing.T`.

## Parent contract

Call `attachN1PairedPopulation(t)` before the actual `testExecutor` constructor
and use `seedInventoryFreshPopulation(..., true)` for the existing canonical
library. The population owns its library, kinds, membership and age baseline;
do not clone or reseed a separate library. Registration is consumed by exactly
the next canonical fixture owned by the same test, and an unused registration
fails cleanup. Subtests must attach their own image.

## Controls

Pure checks cover a missing pair, relative directory, malformed digest and the
exact-million resident gate. The opt-in
`PORTICO_PAIRED_POPULATION_FUNCTIONAL=1` control uses the existing tiny
`canonicalN1ControlArtifact` to verify the real read/register/copy transport and
the non-million refusal; it opens no executor and runs no paired constructor.
The actual attached constructor run belongs to the parent test.
