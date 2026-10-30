import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
import {plainHttpOutsideHome} from '../src/connection-note.ts';
import {parseConnectionDocument} from '../src/server-administration.ts';

const server = (answer: () => Response) => new HttpLocalApi('http://192.168.1.20:32500', '', (async () => answer()) as typeof fetch);
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), {status, headers: {'Content-Type': 'application/json'}});

test('plain HTTP from outside the home networks is worth a note; anything else is not', async () => {
  assert.equal(await plainHttpOutsideHome(server(() => json({secure: false, remote: true, plainHttpOutsideHome: true, clientAddress: '203.0.113.9', accessUrls: []}))), true);
  assert.equal(await plainHttpOutsideHome(server(() => json({secure: false, remote: false, plainHttpOutsideHome: false, clientAddress: '192.168.1.7', accessUrls: []}))), false);
  assert.equal(await plainHttpOutsideHome(server(() => json({secure: true, remote: true, plainHttpOutsideHome: false, clientAddress: '203.0.113.9', accessUrls: []}))), false);
});

test('a server that does not answer the question, or answers unreadably, shows no note', async () => {
  assert.equal(await plainHttpOutsideHome(server(() => json({error: {code: 'not_found'}}, 404))), false);
  assert.equal(await plainHttpOutsideHome(server(() => json('yes'))), false);
  assert.equal(await plainHttpOutsideHome({connection: async () => { throw new TypeError('Failed to fetch'); }}), false);
});

test('the connection facts are read part by part and never claim plain HTTP on a secure connection', () => {
  assert.deepEqual(parseConnectionDocument({secure: true, remote: true, plainHttpOutsideHome: true, clientAddress: '203.0.113.9', accessUrls: ['https://media.example', 7, ''], later: 1}),
    {secure: true, remote: true, plainHttpOutsideHome: false, clientAddress: '203.0.113.9', accessUrls: ['https://media.example']});
  assert.deepEqual(parseConnectionDocument({}), {secure: false, remote: false, plainHttpOutsideHome: false, clientAddress: '', accessUrls: []});
  assert.throws(() => parseConnectionDocument('nope'));
  assert.throws(() => parseConnectionDocument({accessUrls: Array(17).fill('https://media.example')}), 'the bound on advertised addresses is exact');
});
