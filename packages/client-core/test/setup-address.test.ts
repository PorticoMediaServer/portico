import test from 'node:test';
import assert from 'node:assert/strict';
import {setupAddressAllowed} from '../src/setup-address.ts';

test('every S1 range is allowed over http, with its edges', () => {
  for (const host of ['127.0.0.1', '127.255.255.254', '10.0.0.1', '10.255.255.255', '172.16.0.1', '172.31.255.255', '192.168.1.20', '100.64.0.1', '100.127.255.255',
    '169.254.10.10', '[::1]', '[fc00::1]', '[fd12:3456::1]', '[fe80::1]', '[febf::1]', 'localhost', 'tower.local', 'nas'])
    for (const port of ['', ':32400']) assert.equal(setupAddressAllowed('http://' + host + port), true, host + port);
});
test('the three loopback spellings are equal', () => {
  for (const host of ['localhost', '127.0.0.1', '[::1]']) assert.equal(setupAddressAllowed('http://' + host + ':32400'), true);
});
test('public IP literals over http are refused', () => {
  for (const host of ['8.8.8.8', '1.1.1.1', '172.15.0.1', '172.32.0.1', '100.63.255.255', '100.128.0.1', '169.253.0.1', '192.167.1.1', '11.0.0.1', '0.0.0.0',
    '[2001:db8::1]', '[2606:4700::1111]', '[fe00::1]', '[fec0::1]', '[::]', '[fb00::1]'])
    assert.equal(setupAddressAllowed('http://' + host + ':32400'), false, host);
});
test('IPv4-mapped and zoned IPv6 are refused', () => {
  for (const origin of ['http://[::ffff:127.0.0.1]', 'http://[::ffff:7f00:1]', 'http://[::ffff:192.168.1.1]:32400', 'http://[fe80::1%25eth0]', 'http://[fe80::1%eth0]', 'http://[::127.0.0.1]'])
    assert.equal(setupAddressAllowed(origin), false, origin);
});
test('https is allowed for any host', () => {
  for (const origin of ['https://8.8.8.8', 'https://[2001:db8::1]:8443', 'https://server.example.test', 'https://192.168.1.20:32400', 'https://localhost'])
    assert.equal(setupAddressAllowed(origin), true, origin);
});
test('non-canonical spellings and other schemes are refused', () => {
  for (const origin of ['http://127.1', 'http://0x7f.0.0.1', 'http://2130706433', 'http://0177.0.0.1', 'http://LOCALHOST', 'http://localhost/', 'http://localhost/path', 'http://localhost?x=1',
    'http://localhost#x', 'http://user@localhost', 'http://localhost.', 'http://192.168.1.20:99999', 'ftp://localhost', 'ws://localhost', 'localhost:32400', '', 'http://', ' http://localhost',
    'http://[0:0:0:0:0:0:0:1]', 'http://localhost:32400/'])
    assert.equal(setupAddressAllowed(origin), false, origin);
});

test('a host name is a name: legacy numeric IPv4 spellings are refused, as on the server', () => {
  for (const origin of ['http://0xc6336401', 'http://0x7f.1', 'http://3325256705', 'http://nas.0x1', 'http://198.51.0x64.1'])
    assert.equal(setupAddressAllowed(origin), false, origin);
  for (const origin of ['http://tower.local:32400', 'http://nas', 'http://my-nas.home.arpa'])
    assert.equal(setupAddressAllowed(origin), true, origin);
});
