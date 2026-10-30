import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsStructure} from '../../../packages/client-core/src/presentation/settings-structure.ts';
import {nativeSettingsActionTarget} from '../src-next/screens/settings-live/settings-actions.ts';

test('the phone Appearance page draws the profile Customize Home link first', () => {
  const pages = settingsStructure({owner: false, platform: 'phone', capabilities: {}}).headings.find(heading => heading.id === 'account')!.pages;
  const row = pages.find(page => page.id === 'appearance')!.sections[0]!.rows[0]!;
  assert.equal(row.kind, 'action');
  assert.equal(row.id, 'customizeHome');
  assert.equal(row.label, 'next.settingsSpec.account.appearance.groups.0.rows.0.label');
  assert.equal(nativeSettingsActionTarget(row.id), 'customizeHome');
});

test('existing actions keep their own targets', () => {
  assert.equal(nativeSettingsActionTarget('linkTV'), 'linkTV');
  assert.equal(nativeSettingsActionTarget('signOut'), 'confirm');
  assert.equal(nativeSettingsActionTarget('resetRecommendations'), 'confirm');
  assert.equal(nativeSettingsActionTarget('searchSomethingElse'), 'confirm');
});
