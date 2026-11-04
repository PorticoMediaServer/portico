import test from 'node:test';
import assert from 'node:assert/strict';
import {createDirectMember, directMemberCreateFailure, directMemberInputReady, directMemberUsername} from '../src/direct-member-create.ts';

const input = {username: '  Jules ', name: ' Jules ', password: 'long enough pass', allowedLibraries: []};

test('the username follows the server\'s rule: trimmed and lower-cased, 3 to 64 characters, no spaces or @', () => {
  assert.equal(directMemberUsername('  Jules '), 'jules');
  for (const bad of ['ab', 'a b', 'a@b.c', 'x'.repeat(65), '   ']) assert.equal(directMemberUsername(bad), undefined, bad);
  assert.equal(directMemberUsername('x'.repeat(64)), 'x'.repeat(64));
});

test('the form is ready only with a valid username, a profile name and an 8 to 72 byte password', () => {
  assert.equal(directMemberInputReady(input), true);
  assert.equal(directMemberInputReady({...input, name: '  '}), false);
  assert.equal(directMemberInputReady({...input, password: 'short'}), false);
  assert.equal(directMemberInputReady({...input, password: 'é'.repeat(37)}), false, '74 bytes is over the server\'s 72');
});

test('Add account posts the trimmed account once to POST /v1/direct/members and returns the new id', async () => {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {calls.push({path, method, body}); return {id: 'acc_1', username: 'jules', role: 'member'} as T;}};
  const created = await createDirectMember(api as never, {...input, allowedLibraries: ['lib_a']});
  assert.deepEqual(created, {id: 'acc_1', username: 'jules'});
  assert.deepEqual(calls, [{path: '/v1/direct/members', method: 'POST', body: {username: 'jules', name: 'Jules', password: 'long enough pass', allowedLibraries: ['lib_a']}}]);
});

test('an incomplete account is never sent, and a malformed answer is not a success', async () => {
  let sent = 0;
  const api = {request: async <T>(): Promise<T> => {sent++; return {} as T;}};
  await assert.rejects(createDirectMember(api as never, {...input, username: 'a b'}));
  assert.equal(sent, 0);
  await assert.rejects(createDirectMember(api as never, input), /invalid/);
});

test('a taken username (409 conflict) is told apart from every other refusal', () => {
  assert.equal(directMemberCreateFailure(Object.assign(new Error('x'), {code: 'conflict'})), 'taken');
  for (const code of ['invalid_request', 'not_permitted', 'writer_capacity', undefined]) assert.equal(directMemberCreateFailure(Object.assign(new Error('x'), {code})), 'failed');
});

// Sol 8 on #817: the server measures in UTF-8 bytes and checks the profile name too.
test('the username counts UTF-8 bytes after trimming and lower-casing, as the server does', () => {
  assert.equal(directMemberUsername('é'.repeat(32)), 'é'.repeat(32), '64 bytes is the most');
  assert.equal(directMemberUsername('é'.repeat(33)), undefined, '66 bytes is over');
  assert.equal(directMemberUsername('éa'), 'éa', '3 bytes is the least');
});

test('the profile name is at most 120 UTF-8 bytes with no control characters, as the server checks it', () => {
  assert.equal(directMemberInputReady({...input, name: '界'.repeat(40)}), true, '120 bytes');
  assert.equal(directMemberInputReady({...input, name: '界'.repeat(41)}), false, '123 bytes');
  for (const name of ['a\u0007b', 'a\u007fb', 'a\tb']) assert.equal(directMemberInputReady({...input, name}), false, JSON.stringify(name));
});
