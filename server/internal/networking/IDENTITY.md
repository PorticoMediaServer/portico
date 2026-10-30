# Server identity, discovery and first contact

Authority: Justin/Claude final identity ruling, 23 October 2026, design
`69f2d45a972ff9ec1bd8743c760183026c92d555`. The explicit final ruling
supersedes the earlier pending Bonjour and anonymous-name-read proposals.

## Existing Server producer

`ServerIdentity` derives the server ID from the domain-separated SHA256 of the
current Ed25519 public key. The existing Bonjour TXT `id` is that server ID;
`fp` is unpadded base64url(SHA256(publicKey)). It is **not** a TLS fingerprint.
There is no new TXT field. Bonjour data or its friendly name alone does not
confirm an identity or certificate.

`POST /v1/networking/identity-proof` takes `{baseUrl, nonce}`. The base URL is a
canonical HTTP(S) origin whose host matches the actual request Host. The nonce
is exactly 32 bytes encoded as canonical unpadded base64url. The existing
`Accept: application/json; portico-route-proof=2` preference selects v2;
older preferences retain v1 and cannot establish the v2 certificate binding.

The existing response is `{payload, signature}`, both unpadded base64url. The
identity key signs the original payload bytes. Its fields are `kind`, `version`,
`serverId`, `publicKey`, `fingerprint`, `baseUrl`, `nonce`, `issuedAt`, `expiresAt`
and, when nonempty in v2, `tlsCertificateFingerprints`. The lifetime is thirty
seconds. `fingerprint` remains the identity-key SHA256. The certificate list is
SHA256 of actual serving leaf DER, from the managed/custom certificate sources;
client body fields or forwarded headers cannot supply an authorized leaf.

The Server signs the client's supplied challenge and origin. The client owns
challenge freshness, signature/lifetime/origin verification and the observed
connection leaf. The proof does not assert that the requesting client observed
a particular leaf or that any Bonjour announcement was on its local network.

## Canonical client decision

Apps owns the canonical cryptographic/home guard. Native Android and Apple
bridges may exchange the fixed identity proof using the observed pending leaf;
they do not make a separate trust decision.

For the Bonjour home path, all of the following must hold together:

1. The discovered TXT `id` equals the server ID derived from the proof public key.
2. SHA256 of that public key equals the discovered TXT `fp`.
3. The v2 proof signature verifies under that same key for this connection's fresh
   challenge and expected origin, within its lifetime.
4. The signed `tlsCertificateFingerprints` includes this connection's observed
   TLS leaf fingerprint.

Any failure uses the away path. Never relabel `fp`, infer home from an unrelated
announcement, or broaden CGNAT/Tailscale into home. Existing saved-identity,
owner-link and prior-origin mismatch fences still apply before adoption.

## Pending identity-code state

Only the fixed proof exchange is allowed before a matching code. In particular,
there is no pending anonymous `GET /v1/system`, other public request, generic
request bridge or method/path allowlist. The proof-only bridge sends no account
credentials, cookies or authorization headers, follows no redirects and remains
bound to the connection's observed leaf. A failure has no trust fallback.

The ask-code `{server}` is the address as typed, such as `media.example.com`.
No unconfirmed system name or Bonjour friendly name is requested or displayed.
After the entered code matches the identity-derived code, pin that observed
leaf, fetch `/v1/system` through the normal connection, then display the server
name. A wrong code leaves the pending leaf unpinned; leaving clears pending
state. The expected code is never displayed, logged or sent.

This is a client connection restriction, not a new authentication requirement on
the existing public Server system route. It adds no endpoint, certificate TXT
field, setup authority, policy setting, stored value, backup field, or exception
to Local zero-egress.

`IdentityCode` and `IdentityCodeTx` derive the owner's read-only code from the
current key-derived server identity. Local invitation/move/reset code responses
carry that existing server ID for the client-owned `id=` link component. The
canonical Apps guard still verifies the proof; neither a name nor a code DTO is
itself an admitted session.
