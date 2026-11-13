import test from 'node:test';
import assert from 'node:assert/strict';
import {readNetworkInterface, saveNetworkInterface} from '../src/server-admin/network-interface.ts';

test('interface choice preserves remote settings, carries its revision and shows automatic fallback', async () => {
  let selected = '', etag = '"3"';
  const calls: any[] = [];
  const api = {requestRaw: async (path: string, method: string, options: any) => {
    calls.push({path, method, ...options});
    if (method === 'PUT') {
      assert.equal(options.headers['If-Match'], etag);
      assert.equal(options.body.remoteAccess, 'off');
      assert.equal(options.body.interfaces, undefined);
      selected = options.body.advertisedInterface; etag = '"4"';
    }
    return {status: 200, headers: {etag}, body: {advertisedInterface: selected, remoteAccess: 'off', interfaces: [{name: 'eth0', addresses: ['192.168.1.2']}],
      ...(selected === 'gone' ? {interfaceNote: 'The selected interface is unavailable. Using Automatic until it returns.'} : {})}};
  }};
  const automatic = await readNetworkInterface(api);
  assert.equal(automatic.selected, '');
  assert.deepEqual(automatic.interfaces, [{name: 'eth0', addresses: ['192.168.1.2']}]);
  const saved = await saveNetworkInterface(api, automatic, 'gone');
  assert.equal(saved.selected, 'gone');
  assert.equal(saved.etag, '"4"');
  assert.match(saved.note!, /Using Automatic/);
  assert.ok(calls.every(call => call.path === '/v1/admin/settings/remote-access'));
});
