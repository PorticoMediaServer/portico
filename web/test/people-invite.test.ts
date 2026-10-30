import test from 'node:test';
import assert from 'node:assert/strict';
import {InviteAttempts, defaultInviteChoices, inviteBody, inviteChoicesOf} from '../src/screens/settings/people-invite.ts';

const input = {role: 'member', allowedLibraries: ['one', 'two'], expiresInHours: 168, notes: 'private note'};
let counter = 0;
const ids = () => 'op-' + ++counter;

test('one explicit Invite owns one body and one operation id', () => {
  counter = 0;
  const attempts = new InviteAttempts(ids);
  const first = attempts.begin(input);
  assert.equal(first?.operationId, 'op-1');
  assert.deepEqual(first?.body, {role: 'member', allowedLibraries: ['one', 'two'], expiresInHours: 168, notes: 'private note', operationId: 'op-1'});
  assert.equal(attempts.begin(input), undefined, 'a second click while the first is in flight sends nothing');
  assert.equal(counter, 1, 'no second operation id is minted');
});

test('an uncertain outcome keeps the same attempt for the next explicit retry', () => {
  counter = 0;
  const attempts = new InviteAttempts(ids);
  const first = attempts.begin(input);
  attempts.failed({status: 503});
  const retry = attempts.begin(input);
  assert.equal(retry?.operationId, first?.operationId);
  assert.deepEqual(retry?.body, first?.body);
  assert.equal(counter, 1);
});

test('a definite refusal did not create anything, so the next Invite is a new command', () => {
  counter = 0;
  const attempts = new InviteAttempts(ids);
  attempts.begin(input);
  attempts.failed({status: 403});
  const next = attempts.begin(input);
  assert.equal(next?.operationId, 'op-2');
});

test('a created invitation ends the attempt and the next Invite starts a new one', () => {
  counter = 0;
  const attempts = new InviteAttempts(ids);
  attempts.begin(input);
  attempts.created();
  assert.equal(attempts.pending, undefined);
  assert.equal(attempts.begin(input)?.operationId, 'op-2');
});

test('a retired workspace never begins, reuses or publishes an attempt again', () => {
  counter = 0;
  const attempts = new InviteAttempts(ids);
  attempts.begin(input);
  attempts.retire();
  assert.equal(attempts.begin(input), undefined);
  assert.equal(attempts.pending, undefined);
  assert.equal(counter, 1);
});

test('the chosen values, not defaults, are what the create carries', () => {
  counter = 0;
  const attempts = new InviteAttempts(ids);
  const chosen = {role: 'administrator', allowedLibraries: ['films'], expiresInHours: 24, notes: 'for the review'};
  assert.deepEqual(attempts.begin(chosen)?.body, {...chosen, operationId: 'op-1'});
});

test('a refusing operation-id generator creates nothing and leaves no stuck gate', () => {
  let calls = 0;
  const attempts = new InviteAttempts(() => { calls++; throw new Error('no entropy'); });
  assert.throws(() => attempts.begin(input), /no entropy/);
  assert.equal(attempts.pending, undefined);
  assert.equal(calls, 1);
  const recovered = new InviteAttempts(() => 'op-late');
  assert.equal(recovered.begin(input)?.operationId, 'op-late');
});

const defaultAccess = {maxStreamsMode: 'inherit', maxStreams: 0, allowRemote: true, accessTokens: true, maxContentRating: ''};
test('the drawn form choices become one body with wire words, all-libraries and private notes', () => {
  assert.deepEqual(inviteBody({...defaultInviteChoices}), {role: 'member', allowedLibraries: [], expiresInHours: 168, access: defaultAccess});
  assert.deepEqual(inviteBody({...defaultInviteChoices, role: 'admin', allLibraries: false, libraryIds: ['films', 'shows'], expiresInHours: 72, notes: ' private '}),
    {role: 'admin', allowedLibraries: ['films', 'shows'], expiresInHours: 72, access: defaultAccess, notes: 'private'});
  assert.deepEqual(inviteBody({...defaultInviteChoices, porticoUsername: ' justin '}), {role: 'member', allowedLibraries: [], expiresInHours: 168, access: defaultAccess, porticoUsername: 'justin'});
});

test('the drawn access rows become the grant the server applies on joining, never zero for no limit', () => {
  assert.deepEqual(inviteBody({...defaultInviteChoices, streams: '2', awayAllowed: false, accessTokens: false, maxRating: 'PG-13'}).access,
    {maxStreamsMode: 'limit', maxStreams: 2, allowRemote: false, accessTokens: false, maxContentRating: 'PG-13'});
  assert.deepEqual(inviteBody({...defaultInviteChoices, streams: 'unlimited'}).access, {...defaultAccess, maxStreamsMode: 'unlimited'});
  // A retained attempt reads back exactly the choices it was sent with.
  const body = {operationId: 'op', ...inviteBody({...defaultInviteChoices, streams: '6', awayAllowed: false, maxRating: 'R', notes: 'n'})};
  const back = inviteChoicesOf(body);
  assert.deepEqual([back.streams, back.awayAllowed, back.accessTokens, back.maxRating, back.notes], ['6', false, true, 'R', 'n']);
  assert.deepEqual(inviteBody(back), inviteBody({...defaultInviteChoices, streams: '6', awayAllowed: false, maxRating: 'R', notes: 'n'}));
});


test('a non-All empty subset refuses before allocating an operation', () => {
  let ids = 0;
  const attempts = new InviteAttempts(() => String(++ids));
  assert.throws(() => attempts.begin(inviteBody({...defaultInviteChoices, allLibraries: false})), /invite_library_required/);
  assert.equal(ids, 0);
  assert.equal(attempts.pending, undefined);
  assert.deepEqual(inviteBody(defaultInviteChoices).allowedLibraries, []);
});
