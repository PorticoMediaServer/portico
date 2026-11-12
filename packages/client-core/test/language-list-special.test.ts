import test from 'node:test';
import assert from 'node:assert/strict';
import {languageName} from '../src/presentation/language-list.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const lookup = (key: string) => defaultI18n.has(key as never) ? defaultI18n.t(key as never) : undefined;

test('the preferred-language lists name "@appLanguage" and "@original" in words, never as codes', () => {
  assert.equal(languageName('@appLanguage', lookup), 'Portico’s language');
  assert.equal(languageName('@original', lookup), 'Original language');
  assert.equal(languageName('fr', lookup), 'French');
});
