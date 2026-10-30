import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsStructure} from '../src/presentation/index.ts';

const appearance = (platform: 'web' | 'phone' | 'tv') => settingsStructure({owner: true, platform, capabilities: {}})
  .headings.find(heading => heading.id === 'account')!.pages.find(page => page.id === 'appearance')!;

test('the Appearance page starts with the profile Customize Home link on every client', () => {
  for (const platform of ['web', 'phone', 'tv'] as const) {
    const first = appearance(platform).sections[0]!.rows[0]!;
    assert.equal(first.kind, 'action', platform);
    assert.equal(first.id, 'customizeHome', platform);
    assert.equal(first.label, 'next.settingsSpec.account.appearance.groups.0.rows.0.label', platform);
    assert.equal(first.help, 'next.settingsSpec.account.appearance.groups.0.rows.0.help', platform);
    assert.equal(first.scope, 'profile', platform);
  }
});

test('the link is a row of its own, before the card and backdrop settings', () => {
  const rows = appearance('phone').sections[0]!.rows;
  assert.equal(rows[1]!.kind, 'composite');
  assert.equal(rows[1]!.id, 'cardSize');
  assert.equal(rows.some(row => row.id === 'customizeHome'), true);
});
