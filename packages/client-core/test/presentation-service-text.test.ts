import test from 'node:test';
import assert from 'node:assert/strict';
import {serviceI18n, serviceProblem, serviceText, setServiceI18n} from '../src/presentation/service-text.ts';
import {localeChoices} from '../src/presentation/region-choices.ts';
import {createI18n, defaultI18n, type I18n} from '../../i18n/src/index.ts';
import {ApiError} from '../src/index.ts';

test('serviceText reads the catalogue and follows the active viewer locale', () => {
  setServiceI18n(defaultI18n);
  assert.equal(serviceI18n(), defaultI18n);
  const before = serviceText('error.queueTooLarge');
  assert.ok(before && !before.includes('error.queueTooLarge'));
  const custom = {...defaultI18n, t: (() => 'custom words') as I18n['t']};
  setServiceI18n(custom);
  assert.equal(serviceText('error.queueTooLarge'), 'custom words');
  setServiceI18n(defaultI18n);
  assert.equal(serviceText('error.queueTooLarge'), before);
});

test('serviceProblem prefers a carried messageId, then the shared presenter, then the fallback', () => {
  setServiceI18n(defaultI18n);
  const carried = {messageId: 'error.queueTooLarge'};
  assert.equal(serviceProblem(carried, 'library', 'error.genericLoad'), defaultI18n.t('error.queueTooLarge'));
  // An unknown error id falls through to the shared presenter, never raw text.
  const offline = new TypeError('Failed to fetch');
  const said = serviceProblem(offline, 'library', 'error.genericLoad');
  assert.ok(said && !said.includes('Failed to fetch'));
  // Truly unknown errors get this operation's own fallback message.
  const strange = {code: 'something_brand_new'};
  assert.equal(serviceProblem(strange, 'library', 'error.genericLoad'), defaultI18n.t('error.genericLoad'));
  const retryable = new ApiError(503, 'timeout', 'slow');
  assert.ok(serviceProblem(retryable, 'library', 'error.genericLoad').length > 0);
  setServiceI18n(defaultI18n);
});

test('locale choices start with follow-device, then the catalogue, keeping foreign values', () => {
  const i18n = createI18n();
  const choices = localeChoices(i18n);
  assert.equal(choices[0]!.id, 'auto');
  assert.ok(choices.length > 2);
  assert.ok(choices.some(c => c.id === 'en-US'));
  const kept = localeChoices(i18n, 'xx-YY');
  assert.ok(kept.some(c => c.id === 'xx-YY' && c.label === 'xx-YY'));
  const known = localeChoices(i18n, 'en-US');
  assert.equal(known.filter(c => c.id === 'en-US').length, 1, 'no duplicate for known locales');
});

