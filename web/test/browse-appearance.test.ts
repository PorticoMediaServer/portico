import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {appearancePreferences} from '@core/preference-effects.ts';
import {parsePreferenceSnapshot} from '@core/preferences.ts';
import {browsePresentation, cardScale} from '../src/screens/browse/appearance.ts';

const wire = JSON.parse(readFileSync(new URL('../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences;
const confirmed = parsePreferenceSnapshot(wire);
const base = {view: 'grid' as const, cardWidth: 190, cardSize: 'medium' as const, backdrops: true};

test('the confirmed facade changes the library default while a genuine library override wins', () => {
  const values = appearancePreferences({...confirmed, effective: {...confirmed.effective, 'appearance.libraryView': 'list'}}, false);
  assert.equal(browsePresentation(base, values).view, 'list');
  assert.equal(browsePresentation(base, values, 'grid').view, 'grid');
  assert.equal(browsePresentation(base, values, 'list').view, 'list');
});
test('confirmed card scale applies to reserved geometry and backdrop removal without a raw preference map', () => {
  const values = appearancePreferences({...confirmed, effective: {...confirmed.effective, 'appearance.cardSizePercent': 125, 'appearance.showBackdrops': false}}, false);
  const presentation = browsePresentation(base, values);
  assert.equal(cardScale(values), 1.25);
  assert.equal(presentation.cardWidth, 237.5);
  assert.equal(presentation.backdrops, false);
  assert.equal(browsePresentation(base).cardWidth, 190);
});
