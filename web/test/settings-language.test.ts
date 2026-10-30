import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import type {PreferenceField, PreferenceSnapshot} from '../../packages/client-core/src/preferences.ts';
import {hasWebPreferenceEffect} from '../src/screens/settings/effects.ts';
import {shellLanguage} from '../src/shell/settings.ts';

const row = (id: string) => {
  const location = settingsRowIndex.get(id);
  assert.ok(location, id);
  return location.row;
};

/** The six canonical region.* keys exactly as the registry publishes them. */
const REGION = {
  'region.locale': 'en-US', 'region.hourCycle': 'auto', 'region.formats': 'auto',
  'region.dateOrder': 'auto', 'region.firstDay': 'auto', 'region.sizeUnits': 'binary',
} as const;

function snapshot(values: Partial<Record<keyof typeof REGION, string>> = {}): PreferenceSnapshot {
  const effective = {...REGION, ...values};
  const fields: readonly PreferenceField[] = Object.keys(REGION).map(key => ({
    key, type: key === 'region.locale' ? 'locale' as const : 'string' as const, default: REGION[key as keyof typeof REGION],
    scopes: ['profile-server' as const], group: 'region', labelKey: `pref.label.${key}`,
  }));
  return {deviceClass: 'web', registry: {revision: '1', fields}, documents: [], effective,
    effectiveSource: {}, clampedFields: [], registryRevision: '1'};
}

test('every Language & region row has a real web consumer', () => {
  for (const id of ['language.locale', 'language.hourCycle', 'language.formats', 'language.dateOrder', 'language.firstDay', 'language.sizeUnits']) {
    assert.equal(hasWebPreferenceEffect(row(id)), true, id);
  }
});

test('the shell language projects each confirmed region preference once', () => {
  const i18n = shellLanguage(snapshot({
    'region.locale': 'en-GB', 'region.hourCycle': 'h23', 'region.formats': 'en-CA',
    'region.dateOrder': 'dmy', 'region.firstDay': 'mon', 'region.sizeUnits': 'decimal',
  }));
  assert.equal(i18n.region.uiLocale, 'en-GB');
  assert.equal(i18n.region.formatLocale, 'en-CA');
  assert.equal(i18n.region.hour12, false);
  assert.equal(i18n.region.dateOrder, 'dmy');
  assert.equal(i18n.region.firstDay, 'mon');
  assert.equal(i18n.region.sizeUnits, 'decimal');
});

test('follow-system keeps the device locale and clock, and formats can still differ', () => {
  const i18n = shellLanguage(snapshot({'region.formats': 'en-GB'}));
  assert.equal(i18n.region.formatLocale, 'en-GB');
  assert.equal(i18n.region.dateOrder, 'auto');
  assert.equal(i18n.region.firstDay, undefined);
  assert.equal(i18n.region.sizeUnits, 'binary');
  // Auto defers to the device/Intl clock; an explicit h12/h23 is asserted above.
  assert.equal(i18n.region.hour12, undefined);
});
