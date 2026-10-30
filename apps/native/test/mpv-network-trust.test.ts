/** How the Android mpv engine trusts the server: mpv itself may speak only plain HTTP (to the
 * in-app relay, which checks the server's certificate with the app's own HTTP client), and
 * should it ever be handed an HTTPS address it verifies the certificate instead of mpv's
 * default of trusting it unchecked. Read from the engine's source, as the options are set
 * there. Apple ships no mpv engine (AVPlayer only), so there is no second engine to bypass
 * the app's own HTTP trust. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, existsSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
const android = read('../android/app/src/main/java/tv/getportico/portico/native/PorticoMpvController.kt');
const androidRelay = read('../android/app/src/main/java/tv/getportico/portico/native/PorticoMpvRelay.kt');
const appleReadingStream = read('../Native/PorticoReadingByteStream.swift');

/** Every value the Android engine gives an mpv option. */
function optionValues(source: string, name: string): string[] {
  return [...source.matchAll(new RegExp(`"${name}" to "([^"]*)"`, 'g'))].map(m => m[1]);
}

test('Android: mpv verifies TLS certificates', () => {
  assert.deepEqual(optionValues(android, 'tls-verify'), ['yes']);
  assert.doesNotMatch(android, /tls-verify", "no"|"tls-verify" to "no"/);
});

test("Android: mpv's own network access is plain HTTP to the relay, never TLS", () => {
  for (const name of ['stream-lavf-o', 'demuxer-lavf-o']) {
    const values = optionValues(android, name);
    assert.equal(values.length, 1, `${name} is set once`);
    const protocols = decodeURIComponent(values[0].replace(/^protocol_whitelist=%\d+%/, '')).split(',');
    assert.deepEqual(protocols.sort(), ['crypto', 'file', 'http', 'tcp']);
    for (const refused of ['https', 'tls', 'rtmps', 'ftp', 'udp']) assert.ok(!protocols.includes(refused), refused);
  }
  assert.match(android, /access-references["]?(?:, | to )"no"/);
});

test("the Android relay fetches with the app's own HTTP client, its certificate checks, and refuses redirects", () => {
  // The relay trusts exactly what every other Portico connection trusts: the system's authorities
  // or the owner's certificate pinned for that address (PorticoServerTrust). It has no trust of its own.
  assert.match(androidRelay, /OkHttpClient\.Builder\(\)\.porticoTrust\(\)\.followRedirects\(false\)\.followSslRedirects\(false\)/);
  assert.doesNotMatch(androidRelay, /hostnameVerifier|sslSocketFactory|X509TrustManager/, 'no trust of its own on Android');
});

test('Apple ships no mpv engine, and its own streams still take the pinned certificate guard', () => {
  assert.equal(existsSync(new URL('../Native/PorticoMpvController.swift', import.meta.url)), false);
  assert.equal(existsSync(new URL('../Native/PorticoMpvRelay.swift', import.meta.url)), false);
  assert.match(appleReadingStream, /PorticoServerTrust\.handle\(challenge, completion: completionHandler\)/);
});
