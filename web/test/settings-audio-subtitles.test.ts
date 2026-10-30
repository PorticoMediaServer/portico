import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {settingsRowIndex, settingsSpec, type SettingsRow} from '@core/presentation/settings-spec.ts';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {hasWebPreferenceEffect} from '../src/screens/settings/effects.ts';
import {appLanguageCode, maxPreferredLanguages, preferredLanguageChoices, preferredLanguageEntryName, preferredLanguageName, preferredLanguagePool, subtitlePreviewPaint} from '../src/screens/settings/audio-subtitles-model.ts';

const row = (id: string) => {
  const location = settingsRowIndex.get(id);
  assert.ok(location, id);
  return location.row;
};
function audioRows(): SettingsRow[] {
  const section = settingsSpec.find(candidate => candidate.scope === 'account' && candidate.id === 'audio-subtitles');
  assert.ok(section, 'account/audio-subtitles section');
  const flatten = (rows: readonly SettingsRow[]): SettingsRow[] => rows.flatMap(entry => [entry, ...flatten(entry.children ?? [])]);
  return [...(section.groups ?? []), ...(section.advanced?.groups ?? [])].flatMap(group => flatten(group.rows));
}
const backed = {
  'audioSubtitles.preferredAudio': 'playback.preferredAudioLanguages',
  'audioSubtitles.preferredSubtitles': 'playback.preferredSubtitleLanguages',
  'audioSubtitles.subtitleMode': 'playback.subtitleMode',
  'audioSubtitles.subtitleSize': 'playback.subtitleSize',
  'audioSubtitles.subtitleColor': 'playback.subtitleColor',
  'audioSubtitles.subtitleBackground': 'playback.subtitleBackground',
  'audioSubtitles.subtitlePosition': 'playback.subtitlePosition',
};

test('every Account Audio & subtitles value row is exposed through its real registry binding', () => {
  for (const [rowId, key] of Object.entries(backed)) {
    const binding = settingsBinding(rowId);
    assert.equal(binding?.form, 'preferences', rowId);
    assert.equal(binding?.key, key, rowId);
    assert.equal(binding?.scope, 'profile-server', rowId);
    assert.equal(hasWebPreferenceEffect(row(rowId)), true, rowId);
  }
});

test('the section draws exactly its seven value rows; the preview stays a synthetic row', () => {
  assert.deepEqual(audioRows().filter(entry => hasWebPreferenceEffect(entry)).map(entry => entry.id), Object.keys(backed));
  assert.equal(audioRows().some(entry => entry.id === 'audioSubtitles.subtitlePreview'), true);
  assert.equal(settingsBinding('audioSubtitles.subtitlePreview'), undefined);
});

test('the language pools follow the server rules: eight entries, Original audio only', () => {
  assert.equal(maxPreferredLanguages, 8);
  assert.equal(preferredLanguagePool('audio').includes('@original'), true);
  assert.equal(preferredLanguagePool('subtitles').includes('@original'), false);
  assert.equal(preferredLanguagePool('subtitles').includes('@appLanguage'), true);
  assert.equal(new Set(preferredLanguagePool('audio')).size, preferredLanguagePool('audio').length);
});

test('the drawn names keep the special labels and resolve real languages through Intl', () => {
  assert.equal(preferredLanguageName('@appLanguage', 'audio', 'en', 'en-US'), defaultI18n.t('next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.items.0.label'));
  assert.equal(preferredLanguageName('@original', 'audio', 'en', 'en-US'), defaultI18n.t('next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.items.1.label'));
  assert.equal(preferredLanguageName('ja', 'audio', 'en', 'en-US'), 'Japanese');
  assert.equal(preferredLanguageEntryName('@appLanguage', 'audio', 'en', 'en-GB'), 'English');
  assert.equal(appLanguageCode('fr-CA'), 'fr');
  assert.equal(preferredLanguageEntryName('@appLanguage', 'subtitles', 'en', 'de-DE'), 'German');
});

test('Add offers the drawn order without languages already present', () => {
  const choices = preferredLanguageChoices('audio', ['@appLanguage', 'en'], 'en', 'en-US');
  assert.equal(choices.some(choice => choice.code === '@appLanguage'), false);
  assert.equal(choices[0]?.code, '@original');
  assert.equal(choices.some(choice => choice.code === 'en'), false);
  assert.deepEqual(preferredLanguageChoices('subtitles', ['@appLanguage'], 'en', 'en-US')[0], {code: 'en', label: 'English'});
});

test('the preview uses the player palette at the drawn position and scale', () => {
  assert.deepEqual(subtitlePreviewPaint({}), {fontSize: '100%', color: '#fff', backgroundColor: 'rgba(0,0,0,0.65)', textShadow: 'none', fontFamily: 'sans-serif', scale: 1, position: 'bottom'});
  const large = subtitlePreviewPaint({size: 'extra-large', color: 'pink', background: 'outline', position: 'raised'});
  assert.equal(large.fontSize, '150%'); assert.equal(large.color, '#ff80c0'); assert.equal(large.scale, 1.5); assert.equal(large.position, 'raised');
  assert.equal(subtitlePreviewPaint({size: 'huge', color: 'blue', background: 'stripes', position: 'middle'}).position, 'bottom');
});

test('the picker copy is in the catalogue', () => {
  for (const key of ['next.settings.audioSubtitles.search', 'next.settings.audioSubtitles.addHint', 'next.settings.audioSubtitles.noMatch',
    'next.settings.audioSubtitles.allAdded', 'next.settings.audioSubtitles.previewLabel', 'next.settings.cancel'])
    assert.equal(defaultI18n.has(key), true, key);
});
