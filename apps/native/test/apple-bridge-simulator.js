/* Run only in a fresh disposable simulator, through the real Debug app. Build this entry with
 * react-native bundle, then launch with PORTICO_TEST_BUNDLE_PATH. No server or account is used.
 * Tests the bridge's byte staging contract; the 10 MiB fixture is not an image-decoder test.
 */
const React = require('react');
const {AppRegistry, NativeModules, Platform, Text, View} = require('react-native');
function report(result) {
  NativeModules.PorticoRuntime.recordTestEvent(JSON.stringify({appleBridgeResult: result}));
}
const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a4AAAAABJRU5ErkJggg==';
const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
function base64(bytes) {
  let value = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const a = bytes[i], b = bytes[i + 1], c = bytes[i + 2];
    value += alphabet[a >> 2] + alphabet[((a & 3) << 4) | ((b ?? 0) >> 4)]
      + (i + 1 < bytes.length ? alphabet[((b & 15) << 2) | ((c ?? 0) >> 6)] : '=')
      + (i + 2 < bytes.length ? alphabet[c & 63] : '=');
  }
  return value;
}
async function run() {
  const downloads = NativeModules.PorticoDownloads;
  const assertions = [];
  function check(ok, name) { assertions.push({name, passed: !!ok}); if (!ok) throw Error(name); }
  async function refuses(code, call, name) {
    try { await call(); } catch (error) { check(error.code === code, name); return; }
    check(false, name);
  }
  const stages = new Set();
  async function begin() { const {handle} = await downloads.beginCoverImage('image/png'); stages.add(handle); return handle; }
  async function remove(handle) { await downloads.deleteCoverImage(handle); stages.delete(handle); }
  let heartbeat = 0, longestGapMs = 0, lastBeat = Date.now();
  const timer = setInterval(() => { const now = Date.now(); longestGapMs = Math.max(longestGapMs, now - lastBeat); lastBeat = now; heartbeat++; }, 4);
  try {
    const started = NativeModules.PorticoRuntime.launchStartedAt;
    check(Number.isFinite(started) && started <= Date.now() && Date.now() - started < 60000, 'native process launch clock');
    const facts = await NativeModules.PorticoCapabilities.facts();
    check(facts && typeof facts === 'object', 'capabilities bridge returns facts');
    const route = await NativeModules.PorticoSystemMedia.currentRoute();
    check(route && typeof route === 'object', 'system media route bridge returns facts');
    if (Platform.isTV) {
      // Downloads and document export are handheld-only product surfaces.
      report({assertions, failures: 0, scope: 'tvOS boot, capabilities and system-media route', heartbeat, longestGapMs});
      return;
    }
    await refuses('unsupported_image', () => downloads.beginCoverImage('text/plain'), 'unsupported MIME refused');
    const partial = await begin();
    await refuses('invalid_image', () => downloads.appendCoverImage(partial, 'AA=A'), 'malformed base64 refused');
    await refuses('invalid_image', () => downloads.appendCoverImage(partial, 'AB=='), 'noncanonical tail bits refused');
    await refuses('invalid_image', () => downloads.appendCoverImage(partial, base64(new Uint8Array(65537))), 'chunk above 64 KiB refused');
    await refuses('invalid_image', () => downloads.appendCoverImage(partial, base64(new Uint8Array(65538))), 'largest same-length encoded chunk refused');
    const count = await downloads.appendCoverImage(partial, png);
    check(count > 8, 'valid raster bytes appended');
    await refuses('invalid_image', () => downloads.finishCoverImage(partial, count + 1), 'wrong final size refused');
    await remove(partial);
    await refuses('invalid_handle', () => downloads.finishCoverImage(partial, count), 'cancelled partial cannot finish');
    const wrong = await begin();
    await downloads.appendCoverImage(wrong, 'AAAAAAAAAAAAAAAA');
    await refuses('unsupported_image', () => downloads.finishCoverImage(wrong, 12), 'wrong raster signature refused');
    await remove(wrong);
    const maximum = await begin();
    const maximumChunk = new Uint8Array(65536);
    maximumChunk.set([137, 80, 78, 71, 13, 10, 26, 10]);
    check(await downloads.appendCoverImage(maximum, base64(maximumChunk)) === 65536, 'exact 64 KiB chunk accepted');
    check((await downloads.finishCoverImage(maximum, 65536)).size === 65536, 'exact 64 KiB file finished');
    await remove(maximum);
    const large = await begin();
    const chunk = new Uint8Array(24576);
    chunk.set([137, 80, 78, 71, 13, 10, 26, 10]);
    let sent = chunk.length;
    check(await downloads.appendCoverImage(large, base64(chunk)) === sent, 'first large-image chunk acknowledged');
    chunk.fill(0);
    const encoded = base64(chunk), limit = 10 * 1024 * 1024;
    while (sent < limit) {
      const size = Math.min(chunk.length, limit - sent);
      const actual = await downloads.appendCoverImage(large, size === chunk.length ? encoded : base64(chunk.subarray(0, size)));
      if (actual !== sent + size) throw Error('large-image byte acknowledgement');
      sent += size;
    }
    check(sent === limit, 'all 10 MiB acknowledged in bounded chunks');
    await refuses('invalid_image', () => downloads.appendCoverImage(large, 'AA=='), '10 MiB ceiling enforced');
    const lease = await downloads.finishCoverImage(large, sent);
    check(lease.handle === large && lease.size === limit && lease.uri.startsWith('file:'), '10 MiB file finished and synced');
    await refuses('invalid_handle', () => downloads.appendCoverImage(large, 'AA=='), 'finished stage cannot append');
    await remove(large);
    check(heartbeat > 0, 'JavaScript timers progressed during chunk staging');
    if (!Platform.isTV) {
      const save = NativeModules.PorticoHighlightsSave;
      check(!!save, 'invitation save module built');
      const operation = 'P255InvitationSimulator000000000';
      check(operation.length === 32, 'owned export operation identity');
      await refuses('invalid_invitation_picture', () => save.saveInvitationPicture(operation, 'AAAA'), 'non-PNG invitation refused');
      const pending = save.saveInvitationPicture(operation, png);
      setTimeout(() => save.cancelInvitationPicture(operation), 750);
      check(await pending === 'cancelled', 'real invitation export picker cancels');
    }
    report({assertions, failures: 0, stagedBytes: sent, heartbeat, longestGapMs});
  } catch (error) {
    report({assertions, failures: 1, error: String(error), heartbeat, longestGapMs});
  } finally {
    clearInterval(timer);
    for (const handle of stages) await downloads.deleteCoverImage(handle);
  }
}
let began = false;
function Probe() {
  return React.createElement(View, {style: {flex: 1, backgroundColor: '#071018'}, onLayout() {
    NativeModules.PorticoRuntime.bootDrawn();
    if (!began) { began = true; setTimeout(run, 500); }
  }}, React.createElement(Text, {style: {color: 'white'}}, 'Apple bridge simulator checks'));
}
AppRegistry.registerComponent('Portico', () => Probe);
