import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

test('Android decoder MIME names use the shared source codec vocabulary', () => {
  const native = readFileSync(new URL('../android/app/src/main/java/tv/getportico/portico/native/PorticoCapabilities.kt', import.meta.url), 'utf8');
  const shared = readFileSync(new URL('../../../packages/client-core/src/presentation/playback-info.ts', import.meta.url), 'utf8');
  const mappings = new Map([...native.matchAll(/"(video\/[^"]+)" to "([^"]+)"/g)].map(m => [m[1], m[2]]));
  for (const [mime, codec] of [['video/mpeg2', 'mpeg2video'], ['video/wvc1', 'vc1']]) {
    assert.equal(mappings.get(mime), codec);
    assert.match(shared, new RegExp(codec + ':'));
  }
  assert.match(native, /codecInfos.filter \{ !it.isEncoder \}/, 'capabilities must come from decoders');
});
