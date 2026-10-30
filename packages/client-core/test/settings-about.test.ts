import test from 'node:test';
import assert from 'node:assert/strict';
import {findSettingsPage, settingsStructure, type SettingsPlatform} from '../src/presentation/index.ts';

const platforms: readonly SettingsPlatform[] = ['web', 'phone', 'tv'];

/** tv-more frame 32 and account-device.json: About Portico is the last group of This device on
 * every platform, not a page of its own, and an address that still says "about" lands there. */
test('About Portico is the last group of This device on web, phone and television', () => {
  for (const platform of platforms) {
    const structure = settingsStructure({platform, owner: false, capabilities: {}} as Parameters<typeof settingsStructure>[0]);
    const account = structure.headings.find(heading => heading.id === 'account')!;
    assert.equal(account.pages.some(page => page.id === 'about'), false, `${platform} still has a separate About page`);
    const device = account.pages.find(page => page.id === 'device')!;
    const last = device.sections.at(-1)!;
    assert.equal(last.id, 'about', `${platform} does not end This device with the About group`);
    assert.deepEqual(last.rows.map(row => row.kind === 'custom' ? row.id : undefined), ['about']);
    const resolved = findSettingsPage(structure, 'about');
    assert.equal(resolved?.heading.id, 'account');
    assert.equal(resolved?.page.id, 'device', `${platform} sends a legacy about address to the wrong page`);
  }
});

test('the legacy map never captures a server page address', () => {
  const structure = settingsStructure({platform: 'web', owner: true, capabilities: {manageServer: true}} as Parameters<typeof settingsStructure>[0]);
  assert.equal(findSettingsPage(structure, 'server-dashboard')?.heading.id, 'server');
  // An unknown address still falls back to the first account page, never to a server page.
  assert.notEqual(findSettingsPage(structure, 'server-about')?.heading.id, 'server');
});
