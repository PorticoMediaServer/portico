import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsStructure, type SettingsPage, type SettingsRow} from '../src/presentation/settings-structure.ts';

/** ST lane evidence: the native structure's choice lists must carry every value the
 * server publishes for the preference they edit, or the row cannot show or set it. */

const publishedEnum = (key: string) => ({"playback.subtitleMode": ["off", "foreign", "forced", "always"], "playback.subtitleBackground": ["none", "outline", "shadow", "translucent", "opaque"], "playback.subtitleColor": ["white", "yellow", "cyan", "green", "pink"], "playback.subtitlePosition": ["bottom", "raised", "top"], "playback.subtitleFont": ["default", "serif", "mono", "rounded"]} as Record<string, string[]>)[key];

function rowFor(pageId: string, key: string): SettingsRow | undefined {
  const structure = settingsStructure({owner: false, platform: 'phone', capabilities: {}});
  const page = structure.headings.flatMap(heading => heading.pages).find(candidate => candidate.id === pageId) as SettingsPage | undefined;
  return page?.sections.flatMap(section => section.rows).find(row => 'key' in row && row.key === key);
}

for (const key of ['playback.subtitleMode', 'playback.subtitleBackground']) {
  test(`native Audio & subtitles offers every published ${key} choice`, () => {
    const published = publishedEnum(key);
    assert.ok(published?.length, `server publishes ${key}`);
    const row = rowFor('languages', key);
    assert.ok(row && 'control' in row && row.control.type === 'choice', `structure has a choice row for ${key}`);
    const offered = row.control.choices.map(choice => String(choice.value));
    assert.deepEqual([...offered].sort(), [...published!].sort(), `${key} choices match the registry`);
  });
}

// ST-3 (DECISIONS 2 Nov): phones and tablets draw the full subtitle appearance rows; TV keeps Size and Background.
test('phones draw every subtitle style row the player applies, and TV keeps the frame\'s two', () => {
  const keys = (platform: 'phone' | 'tv') => {
    const structure = settingsStructure({owner: false, platform, capabilities: {}});
    const page = structure.headings.flatMap(heading => heading.pages).find(candidate => candidate.id === 'languages') as SettingsPage;
    return page.sections.flatMap(section => section.rows).flatMap(row => 'key' in row ? [row.key] : []);
  };
  const style = ['playback.subtitleSize', 'playback.subtitleColor', 'playback.subtitleBackground', 'playback.subtitlePosition', 'playback.subtitleFont', 'playback.keepSubtitleStyling', 'playback.useSystemCaptionStyle'];
  assert.deepEqual(style.filter(key => keys('phone').includes(key)), style);
  assert.deepEqual(style.filter(key => keys('tv').includes(key)), ['playback.subtitleSize', 'playback.subtitleBackground']);
  for (const key of ['playback.subtitleColor', 'playback.subtitlePosition', 'playback.subtitleFont']) {
    const row = rowFor('languages', key) as Extract<SettingsRow, {kind: 'preference'}>;
    assert.deepEqual((row.control as {choices: {value: string}[]}).choices.map(choice => choice.value).sort(), publishedEnum(key)!.sort(), key);
  }
});
