import test from 'node:test';
import assert from 'node:assert/strict';
import {nextIcons} from '../../design/src/next-icons.ts';
import {settingsStructure} from '../src/presentation/settings-structure.ts';

// Owner, 13 Nov: Audio & subtitles, Appearance and This device all drew the slider glyph, because two of them named icons the
// app does not have and the apps fall back to sliders. Every page names a glyph that exists, and no two account pages share one.
test('every settings page names an icon in the set, and the account pages each have their own', () => {
  const structure = settingsStructure({owner: true, platform: 'phone', capabilities: {liveTV: true}});
  for (const heading of structure.headings) for (const page of heading.pages) assert.ok(Object.hasOwn(nextIcons, `i-${page.icon}`), `${page.id}: i-${page.icon}`);
  const account = structure.headings.find(heading => heading.id === 'account')!.pages.map(page => page.icon);
  assert.equal(new Set(account).size, account.length, account.join(', '));
});
