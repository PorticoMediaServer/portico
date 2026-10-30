import test from 'node:test';
import assert from 'node:assert/strict';
import {canSeeServerPage, serverVisibilityKey, serverRoleKey} from '../src-next/kit/shell/authority.ts';

test('server presentation keeps the actual owner, administrator and member audiences distinct', () => {
  assert.equal(canSeeServerPage('owner', 'owner'), true);
  assert.equal(canSeeServerPage('owner', 'administrators'), true);
  assert.equal(canSeeServerPage('administrator', 'owner'), false);
  assert.equal(canSeeServerPage('administrator', 'administrators'), true);
  assert.equal(canSeeServerPage('member', 'owner'), false);
  assert.equal(canSeeServerPage('member', 'administrators'), false);
  assert.equal(serverVisibilityKey('administrator', 'owner'), undefined);
  assert.equal(serverVisibilityKey('member', 'administrators'), undefined);
  assert.equal(serverVisibilityKey('owner', 'owner'), 'next.shell.serverVisibility.owner');
  assert.equal(serverVisibilityKey('owner', 'administrators'), 'next.shell.serverVisibility.ownerShared');
  assert.equal(serverVisibilityKey('administrator', 'administrators'), 'next.shell.serverVisibility.administrator');
  assert.equal(serverRoleKey('administrator'), 'next.shell.administrator');
  assert.equal(serverRoleKey('member'), 'next.shell.member');
});
