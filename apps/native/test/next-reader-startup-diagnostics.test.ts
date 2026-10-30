import test from 'node:test';
import assert from 'node:assert/strict';
import {readerStartupDiagnostic, readerStartupProbe} from '../src-next/screens/read/startup-diagnostics';
import {appleReaderEntry} from '../src-next/screens/read/asset-origin';

test('startup probes report structural state without forwarding private event fields', () => {
  const handlers = new Map<string, () => void>(), packets: string[] = [];
  const window = {ReactNativeWebView: {postMessage: (value: string) => packets.push(value)},
    addEventListener: (kind: string, listener: () => void) => handlers.set(kind, listener)};
  const document = {getElementById: () => ({childElementCount: 3}), scripts: {length: 2},
    addEventListener: window.addEventListener};
  Function('window', 'document', 'location', 'setTimeout', readerStartupProbe)(window, document, {href: appleReaderEntry}, (callback: () => void) => callback());
  handlers.get('error')!(); handlers.get('securitypolicyviolation')!(); handlers.get('load')!();
  assert.deepEqual(packets.map(packet => readerStartupDiagnostic(appleReaderEntry, packet)?.phase), ['injected', 'error', 'csp', 'load', 'settled']);
  const raw = JSON.parse(packets[0]);
  assert.deepEqual(readerStartupDiagnostic(appleReaderEntry, JSON.stringify({...raw, secret: 'never log', url: 'private'})),
    {phase: 'injected', entry: true, bridge: false, root: true, children: 3, scripts: 2});
});

test('diagnostic parser rejects external origins and malformed or oversized fields', () => {
  const value = {kind: 'reader-startup-diagnostic', phase: 'settled', entry: true, bridge: true, root: true, children: 1, scripts: 2};
  assert.equal(readerStartupDiagnostic('https://outside.test', JSON.stringify(value)), undefined);
  for (const patch of [{phase: 'private text'}, {children: -1}, {scripts: 101}, {bridge: 'secret'}, {children: 1.2}])
    assert.equal(readerStartupDiagnostic(appleReaderEntry, JSON.stringify({...value, ...patch})), undefined);
  assert.equal(readerStartupDiagnostic(appleReaderEntry, 'x'.repeat(1025)), undefined);
});
