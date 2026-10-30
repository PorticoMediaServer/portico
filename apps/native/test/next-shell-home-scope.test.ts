import assert from 'node:assert/strict';
import test from 'node:test';
import {nativeHomeSelectionScope} from '../src-next/shell/selection-scope';
import type {LocalSession} from '../../../packages/client-core/src';

const viewer: LocalSession['viewer'] = {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'member'};

test('Home selection retains the actual local or hosted principal without changing session contracts', () => {
  for (const authority of ['local', 'hosted']) {
    const principal = {...viewer, authority};
    assert.deepEqual(nativeHomeSelectionScope(principal), {serverId: 'server', accountId: 'account', profileId: 'profile', authority, viewId: 'home'});
    assert.equal(principal.role, 'member');
    assert.equal(principal.authority, authority);
  }
});

test('unknown authority strings are rejected rather than coerced or granted local access', () => {
  for (const authority of ['', 'LOCAL', 'Hosted', ' hosted', 'local ', 'web', 'server', 'unknown']) {
    const principal = {...viewer, authority};
    assert.throws(() => nativeHomeSelectionScope(principal), {message: 'invalid_home_authority', code: 'invalid_response'});
    assert.equal(principal.authority, authority);
  }
});
