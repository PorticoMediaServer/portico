import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {searchSettings, settingsRoute} from '../src/kit/settings/search.ts';

test('search includes panel tabs and exact hidden Advanced rows, with no people or demo values', () => {
  const tokens = searchSettings('access tokens', defaultI18n.t, () => true);
  assert.ok(tokens.some(hit => hit.rowId === 'people.accessTokens' && hit.panelId === 'person' && hit.tabId === 'advanced'));
  assert.equal(searchSettings('nana_r', defaultI18n.t, () => true).length, 0);
  assert.equal(searchSettings('x', defaultI18n.t, () => true).length, 0);
  assert.equal(searchSettings('custom', defaultI18n.t, () => true).some(hit => hit.rowId === 'privacy.level'), false);
});
test('search never offers rows denied by the current producer authority', () => {
  assert.equal(searchSettings('access tokens', defaultI18n.t, (_row, section) => section.scope === 'account').some(hit => hit.section.scope === 'server'), false);
  assert.deepEqual(settingsRoute({scope: 'server', section: 'general', row: 'people.accessTokens'}, () => true), {scope: 'server', section: 'general'});
  assert.equal(settingsRoute({scope: 'server', section: 'general'}, section => section.scope === 'account').scope, 'account');
});
