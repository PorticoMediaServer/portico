import assert from 'node:assert/strict';
import test from 'node:test';
import {EventEmitter} from 'node:events';
import {measurementLaunch, waitForMeasurementEndpoint} from './browser.mjs';

const child = () => Object.assign(new EventEmitter(), {stderr: new EventEmitter()});
const retired = chrome => {
  assert.equal(chrome.listenerCount('error'), 0);
  assert.equal(chrome.listenerCount('exit'), 0);
  assert.equal(chrome.stderr.listenerCount('data'), 0);
};
test('measurement launch has no desktop/keyring D-Bus dependency', () => {
  const {args, options} = measurementLaunch('/owned/profile', '/short/temp');
  for (const flag of ['--headless=new', '--password-store=basic', '--use-mock-keychain',
    '--disable-background-networking', '--disable-features=MediaRouter']) assert.ok(args.includes(flag));
  assert.equal(options.env.DBUS_SESSION_BUS_ADDRESS, 'unix:path=/dev/null');
  assert.equal(options.env.DBUS_SYSTEM_BUS_ADDRESS, 'unix:path=/dev/null');
  assert.equal(options.env.TMPDIR, '/short/temp');
});
test('a single launch accepts a split DevTools announcement and retires startup listeners', async () => {
  const chrome = child(), ready = waitForMeasurementEndpoint(chrome, 1000);
  chrome.stderr.emit('data', Buffer.from('unavailable desktop service\nDevTools lis'));
  chrome.stderr.emit('data', Buffer.from('tening on ws://127.0.0.1:1234/devtools/browser/public\n'));
  assert.equal(await ready, 'ws://127.0.0.1:1234/devtools/browser/public'); retired(chrome);
});
test('a missing endpoint fails once with bounded stderr and an explicit startup deadline', async () => {
  const chrome = child(), ready = waitForMeasurementEndpoint(chrome, 10);
  chrome.stderr.emit('data', Buffer.from('x'.repeat(20000) + '\npublic startup diagnostic'));
  await assert.rejects(ready, error => {
    assert.match(error.message, /within 10ms.*single headless launch/);
    assert.match(error.message, /public startup diagnostic/);
    assert.ok(error.message.length < 4300); return true;
  }); retired(chrome);
});
test('early process failure keeps its actual exit or spawn error, without waiting or retrying', async () => {
  for (const event of ['exit', 'error']) {
    const chrome = child(), ready = waitForMeasurementEndpoint(chrome, 1000);
    if (event === 'exit') chrome.emit('exit', 17, null);
    else chrome.emit('error', Object.assign(new Error('not installed'), {code: 'ENOENT'}));
    await assert.rejects(ready, event === 'exit' ? /before DevTools \(17\)/ : /failed to start \(ENOENT\)/);
    retired(chrome);
  }
});
