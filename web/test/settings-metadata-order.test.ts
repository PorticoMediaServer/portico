import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const page = readFileSync(new URL('../src/screens/settings/metadata/MetadataSettingsPage.tsx', import.meta.url), 'utf8');
const at = (needle: string) => {const index = page.indexOf(needle); assert.ok(index > 0, needle); return index;};

test('Metadata follows its spec: Lookup holds online, adult, then what each library uses (server-metadata.json)', () => {
  const lookup = at("<SettingsGroup title={t('server.metadata.lookups')}>");
  assert.ok(lookup < at("metadata.settings.includeAdult'") && at("metadata.settings.includeAdult'") < at('<MetadataLibraries libraries='));
  assert.ok(at('<MetadataLibraries libraries=') < at("metadata.groups.1.title')}>"));
});

test('the local artwork limit and picture size sit in Advanced › Artwork, not a second main Artwork group', () => {
  assert.doesNotMatch(page, /<SettingsGroup title=\{t\('server\.metadata\.artwork'\)\}>/);
  const advancedArtwork = at("metadata.advanced.groups.2.title')} advanced>");
  assert.ok(advancedArtwork < at("aria-label={t('metadata.settings.artworkLimit')}"));
  assert.ok(at('<SettingsAdvanced open={advancedOpen}') < advancedArtwork);
  // Advanced is drawn whenever the server sends one of its fields, the artwork rows from the main document among them.
  assert.doesNotMatch(page, /\{document\.advanced && draft\.advanced \? <SettingsAdvanced/);
});

test('each Advanced row, the TheTVDB key and the artwork limit are offered only when the server sends their field', () => {
  for (const guard of ["document.tvdbCredentialConfigured !== undefined && secret('tvdb'", 'hasLimit ? <>', 'draft.edge !== undefined ? <SettingsRow',
    'extra?.castPhotos !== undefined ?', 'advanced?.refreshAll ? <SettingsRow', 'music ? <SettingsGroup']) at(guard);
  at("key === 'tmdb' && document.tmdbCredentialUnreadable ? 'server.metadata.keyUnreadable'");
});
