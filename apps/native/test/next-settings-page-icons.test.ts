import test from 'node:test';
import assert from 'node:assert/strict';
import {nextIcons} from '../../../packages/design/src/next-icons.ts';
import {settingsStructure} from '../../../packages/client-core/src/presentation/settings-structure.ts';
import {nativeSettingsIcon} from '../src-next/screens/settings-live/settings-icon.ts';

// Owner, 13 Nov: Audio & subtitles, Appearance and This device all drew the slider glyph. Every settings page draws a glyph
// of its own: none falls back to sliders but Appearance, which is sliders, and no two account pages share one.
test('every settings page draws its own glyph, not the slider fallback', () => {
  const structure = settingsStructure({owner: true, platform: 'phone', capabilities: {liveTV: true}});
  for (const heading of structure.headings) for (const page of heading.pages) {
    const glyph = nativeSettingsIcon(page.icon);
    assert.ok(Object.hasOwn(nextIcons, glyph), `${page.id}: ${glyph}`);
    if (page.icon !== 'sliders') assert.notEqual(glyph, 'i-sliders', `${page.id} (${page.icon}) falls back to sliders`);
  }
  const account = structure.headings.find(heading => heading.id === 'account')!.pages.map(page => nativeSettingsIcon(page.icon));
  assert.equal(new Set(account).size, account.length, account.join(', '));
});
