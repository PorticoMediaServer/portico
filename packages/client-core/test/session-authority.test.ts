import test from 'node:test';
import assert from 'node:assert/strict';
import type {LocalSession} from '../src/index.ts';
import {AuthorityChangedError, authorityTupleOf, bindAuthority, compareSessionAuthority, sameAuthorityTuple, sessionAuthority, type AuthoritySource, type AuthorityTuple} from '../src/session-authority.ts';

function session(token = 'token_one', generation = '1', change: Partial<LocalSession['viewer']> = {}, family = 'family_one'): LocalSession {
  const until = new Date(Date.now() + 600_000).toISOString();
  return {accessToken: token, tokenGeneration: generation, sessionFamilyId: family, expiresAt: until, authorizationHorizon: until, refreshToken: 'secret_refresh', viewer: {serverId: 'srv_one', accountId: 'owner', profileId: 'profile', role: 'owner', authority: 'local', ...change}};
}

test('a sign-in compares by family and generation, never by token alone', () => {
  const held = session();
  assert.equal(compareSessionAuthority(held, session()), 'same');
  assert.equal(compareSessionAuthority(held, session('token_two', '2')), 'rotated');
  assert.equal(compareSessionAuthority(held, session('token_nine', '12345678901234567890')), 'rotated', 'generations compare as big integers');
  assert.equal(compareSessionAuthority(session('token_two', '2'), held), 'stale');
  assert.equal(compareSessionAuthority(held, session('token_two', '1')), 'replaced', 'same generation with another token');
  for (const other of [session('token_two', '2', {}, 'family_two'), session('token_two', '2', {profileId: 'kids'}), session('token_two', '2', {accountId: 'other'}), session('token_two', '2', {serverId: 'srv_two'}), session('token_two', '2', {authority: 'portico'}), session('token_two', 'x')])
    assert.equal(compareSessionAuthority(held, other), 'replaced');
});

test('the authority view carries no secrets', () => {
  const view = sessionAuthority(session());
  assert.deepEqual(view, {serverId: 'srv_one', authority: 'local', accountId: 'owner', profileId: 'profile', sessionFamilyId: 'family_one', tokenGeneration: '1'});
  assert.ok(!JSON.stringify(view).includes('token_one') && !JSON.stringify(view).includes('secret'));
  assert.ok(Object.isFrozen(view));
});

function source(initial: AuthorityTuple | undefined) {
  let value = initial;
  const listeners = new Set<() => void>();
  const s: AuthoritySource & {set(next: AuthorityTuple | undefined): void; listeners: Set<() => void>} = {
    authority: () => value,
    subscribe(listener) {listeners.add(listener); return () => {listeners.delete(listener);};},
    set(next) {value = next; for (const listener of [...listeners]) listener();},
    listeners,
  };
  return s;
}

test('a binding survives token rotation and ends on any change of sign-in', () => {
  const tuple = authorityTupleOf(session());
  assert.deepEqual(authorityTupleOf(session('token_two', '2')), tuple, 'rotation does not change the tuple');
  for (const next of [{...tuple, profileId: 'kids'}, {...tuple, familyId: 'family_two'}, {...tuple, accountId: 'other'}, {...tuple, serverId: 'srv_two'}, {...tuple, epoch: 1}, undefined]) {
    const s = source(tuple), binding = bindAuthority(s);
    assert.equal(binding.isCurrent(), true);
    s.set({...tuple});
    assert.equal(binding.isCurrent(), true, 'an equal tuple keeps the binding');
    s.set(next);
    assert.equal(binding.signal.aborted, true);
    assert.equal(binding.isCurrent(), false);
    assert.throws(() => binding.assertCurrent(), AuthorityChangedError);
    assert.equal(s.listeners.size, 0, 'it stops watching');
    s.set(tuple);
    assert.equal(binding.isCurrent(), false, 'a binding that ended stays ended');
  }
});

test('a read sees a change before any notification', () => {
  let value: AuthorityTuple = authorityTupleOf(session());
  const binding = bindAuthority({authority: () => value, subscribe: () => () => {}});
  value = {...value, profileId: 'kids'};
  assert.equal(binding.isCurrent(), false);
  assert.equal(binding.signal.aborted, true);
});

test('binding to nothing, disposing, and a source that notifies inside subscribe', () => {
  const empty = bindAuthority(source(undefined));
  assert.equal(empty.signal.aborted, true);
  assert.equal(empty.isCurrent(), false);

  const s = source(authorityTupleOf(session())), binding = bindAuthority(s);
  binding.dispose();
  binding.dispose();
  assert.equal(binding.signal.aborted, true);
  assert.equal(s.listeners.size, 0);

  let released = 0, value: AuthorityTuple | undefined = authorityTupleOf(session());
  const eager = bindAuthority({authority: () => value, subscribe: listener => {value = undefined; listener(); return () => {released++;};}});
  assert.equal(eager.signal.aborted, true);
  assert.equal(released, 1, 'the subscription is still released');
});

test('tuples compare every field, including the optional ones', () => {
  const tuple = authorityTupleOf(session());
  assert.equal(sameAuthorityTuple(tuple, {...tuple}), true);
  assert.equal(sameAuthorityTuple(tuple, {...tuple, profileId: undefined}), false);
  assert.equal(sameAuthorityTuple(tuple, undefined), false);
  assert.equal(sameAuthorityTuple(undefined, undefined), false);
});
