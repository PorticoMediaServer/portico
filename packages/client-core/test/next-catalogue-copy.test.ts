import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogue, createI18n, enUS, messageArguments, resolveRegion} from '../../i18n/src/index.ts';

test('the TV Forgot PIN lede keeps its exact period without changing the older handoff heading', () => {
  const i18n = createI18n(resolveRegion(undefined, {locales: ['en-US']}));
  assert.equal(i18n.t('next.auth.tv.forgotPINBody'), 'Manage profiles, PINs and two-step verification on your phone or computer.');
  assert.equal(i18n.t('account.tvHandoffTitle'), 'Manage profiles, PINs and two-step verification on your phone or computer');
});

test('selection visibility copy stays separate from owner-only policy copy', () => {
  const i18n = createI18n(resolveRegion(undefined, {locales: ['en-US']}));
  assert.equal(i18n.t('next.selection.onlyYou'), 'Only you');
  assert.equal(i18n.t('next.selection.ownerOnly'), 'Owner only');
  assert.equal(i18n.t('next.shell.ownerOnly'), 'Owner only');
});

test('new catalogue words follow regional spelling and fall back to the US source', () => {
  for (const [locale, color, program] of [
    ['en-US', 'Color backdrop', 'Open the program'],
    ['en-CA', 'Colour backdrop', 'Open the program'],
    ['en-GB', 'Colour backdrop', 'Open the programme'],
    ['en-AU', 'Colour backdrop', 'Open the programme'],
    ['fr-FR', 'Color backdrop', 'Open the program'],
  ]) {
    const i18n = createI18n(resolveRegion(undefined, {locales: [locale]}));
    assert.equal(i18n.t('next.artwork.case.colour'), color, locale);
    assert.equal(i18n.t('next.states.shortcut.programme'), program, locale);
    assert.equal(i18n.t('next.overlays.reload'), 'Try again', locale);
    assert.equal(i18n.t('next.states.reload'), 'Try again', locale);
  }
  assert.equal(catalogue('en-CA')['next.states.shortcut.programme'], undefined);
  assert.equal(catalogue('en-GB')['next.overlays.reload'], undefined);
});

test('recording confirmation substitutes program and server without respelling their values', () => {
  const id = 'next.overlays.deleteRecordingBody';
  const values = {program: "Alex's {Programme} Hour", server: "North's Color Server"};
  const expected = `${values.program}, recorded today at 1:30 PM on Summit Sports, is deleted from ${values.server} for everyone. This can't be undone.`;
  assert.deepEqual(messageArguments(enUS[id]), ['program', 'server']);
  for (const locale of ['en-US', 'en-CA', 'en-GB'] as const) {
    assert.deepEqual(messageArguments(catalogue(locale)[id] ?? enUS[id]), ['program', 'server'], locale);
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    assert.equal(i18n.t(id, values), expected, locale);
  }
  assert.equal(catalogue('en-CA')[id], undefined);
  assert.equal(catalogue('en-GB')[id], undefined);
});
