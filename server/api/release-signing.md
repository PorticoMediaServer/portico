# Release signing custody

The release private key must remain off development computers. Use a dedicated hardware signer or an isolated release runner with an injected, protected secret. Do not generate a production signing key through the development release script. Never commit private keys, signer exports, or credentials.

Publish the Ed25519 public key with each release and through an independently controlled distribution channel. Installers and updaters must verify the signed manifest against their pinned public key before accepting any artifact, then verify every artifact digest. A failed signature or unknown key stops installation. A signature with a key downloaded from the same untrusted manifest does not establish trust.

Key rotation requires an overlap release signed by the existing key that authenticates the next public key, a documented activation date, and a revocation procedure. Keep a recovery copy outside the build environment under separate access control. Signing authorization must be restricted to reviewed release commits and auditable release jobs.

The build scripts now require a configured signer and matching pinned public key, and seal final artifacts. Production custody, operating-system signing and installer trust provisioning still need operational verification. This document does not claim that an offline production signer or installer verification has been provisioned.
