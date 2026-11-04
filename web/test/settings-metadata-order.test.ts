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
  // Advanced always renders: the artwork rows come from the main document.
  assert.doesNotMatch(page, /\{document\.advanced && draft\.advanced \? <SettingsAdvanced/);
});
