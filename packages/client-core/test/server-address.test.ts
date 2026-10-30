import test from 'node:test';
import assert from 'node:assert/strict';
import {firstAnswering, homeLookingHost, serverAddress, serverAddressCandidates} from '../src/server-address.ts';

test('a bare home address is tried on the server’s own port first, over plain HTTP', () => {
  assert.deepEqual(serverAddressCandidates('192.168.1.20'), ['http://192.168.1.20:32500', 'https://192.168.1.20:32500', 'https://192.168.1.20', 'http://192.168.1.20']);
  assert.deepEqual(serverAddressCandidates(' nas '), ['http://nas:32500', 'https://nas:32500', 'https://nas', 'http://nas']);
  assert.equal(serverAddress('media.lan'), 'http://media.lan:32500');
  assert.equal(serverAddress('NAS.local/'), 'http://nas.local:32500');
});

test('a public name or address is tried over HTTPS first, and plain HTTP is still offered last', () => {
  assert.deepEqual(serverAddressCandidates('media.example.com'), ['https://media.example.com', 'https://media.example.com:32500', 'http://media.example.com:32500']);
  assert.deepEqual(serverAddressCandidates('203.0.113.9'), ['https://203.0.113.9', 'https://203.0.113.9:32500', 'http://203.0.113.9:32500']);
});

test('a typed port and a typed scheme are always kept', () => {
  assert.deepEqual(serverAddressCandidates('192.168.1.20:8096'), ['http://192.168.1.20:8096', 'https://192.168.1.20:8096']);
  assert.deepEqual(serverAddressCandidates('media.example.com:8443'), ['https://media.example.com:8443', 'http://media.example.com:8443']);
  assert.deepEqual(serverAddressCandidates('http://media.example.com:8080/'), ['http://media.example.com:8080']);
  assert.deepEqual(serverAddressCandidates('HTTPS://media.example.com'), ['HTTPS://media.example.com']);
});

test('IPv6 addresses keep their brackets and their port', () => {
  assert.deepEqual(serverAddressCandidates('[fd12:3456::1]'), ['http://[fd12:3456::1]:32500', 'https://[fd12:3456::1]:32500', 'https://[fd12:3456::1]', 'http://[fd12:3456::1]']);
  assert.deepEqual(serverAddressCandidates('[fd12:3456::1]:8443'), ['http://[fd12:3456::1]:8443', 'https://[fd12:3456::1]:8443']);
  assert.equal(serverAddress('fd12:3456::1'), 'http://[fd12:3456::1]:32500');
  assert.equal(serverAddress('2001:db8::7'), 'https://[2001:db8::7]');
});

test('what cannot be an address is refused before anything is contacted', () => {
  for (const typed of ['', '   ', 'two words', 'user@host', 'ftp://host', 'host:99999', 'host:0', 'x'.repeat(400), 'ho\\st', '-bad-']) {
    assert.throws(() => serverAddressCandidates(typed), (e: {code?: string}) => e.code === 'invalid_address', typed);
  }
});

test('which hosts look like home', () => {
  for (const host of ['localhost', '10.0.0.4', '192.168.1.20', '172.20.1.1', '169.254.3.4', 'nas', 'nas.local', 'media.home', 'box.internal', '[fd00::1]', '[fe80::1]']) assert.equal(homeLookingHost(host), true, host);
  for (const host of ['203.0.113.9', '8.8.8.8', 'media.example.com', '[2001:db8::1]', '100.64.0.1']) assert.equal(homeLookingHost(host), false, host);
});

test('the first candidate that answers is used; when none does, the most likely one’s failure is reported', async () => {
  const tried: string[] = [];
  const found = await firstAnswering(['http://nas:32500', 'https://nas:32500', 'https://nas'], async origin => { tried.push(origin); if (!origin.startsWith('https://nas:')) throw new Error('refused ' + origin); return 'pin'; });
  assert.deepEqual(found, {origin: 'https://nas:32500', value: 'pin'});
  assert.deepEqual(tried, ['http://nas:32500', 'https://nas:32500']);
  await assert.rejects(firstAnswering(['a', 'b'], async origin => { throw new Error('refused ' + origin); }), /refused a/);
});

test('cancelling stops the search', async () => {
  const controller = new AbortController();
  const tried: string[] = [];
  await assert.rejects(firstAnswering(['a', 'b', 'c'], async origin => { tried.push(origin); controller.abort(); throw new Error('cancelled'); }, controller.signal));
  assert.deepEqual(tried, ['a']);
});
