import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createI18n, resolveRegion} from '@i18n';
import {servicesNoticeTime, servicesRelativeTime, servicesSignInDate} from '../src/services/time.ts';

const i18n = createI18n(resolveRegion(undefined, {locales: ['en-US'], timeZone: 'UTC'}));
const now = Date.parse('2026-10-23T14:00:00Z');
test('Hosted dates match the drawn now, minutes, days and weeks instead of a hidden timezone or early date fallback', () => {
  assert.equal(servicesRelativeTime(i18n, new Date(now).toISOString(), now), 'now');
  assert.equal(servicesRelativeTime(i18n, '2026-10-23T13:55:00Z', now), '5 minutes ago');
  assert.equal(servicesRelativeTime(i18n, '2026-10-17T14:00:00Z', now), '6 days ago');
  assert.equal(servicesRelativeTime(i18n, '2026-10-02T14:00:00Z', now), '3 weeks ago');
  assert.equal(servicesRelativeTime(i18n, 'not a date', now), '');
});
test('the sign-in date is today only on the device-region calendar day', () => {
  assert.equal(servicesSignInDate(i18n, '2026-10-23T00:10:00Z', now), 'today');
  assert.equal(servicesSignInDate(i18n, '2026-10-14T00:10:00Z', now), 'Oct 14, 2026');
  const pacific = createI18n(resolveRegion(undefined, {locales: ['en-US'], timeZone: 'America/Los_Angeles'}));
  assert.equal(servicesSignInDate(pacific, '2026-10-23T00:10:00Z', now), 'Oct 22, 2026');
  assert.equal(servicesSignInDate(i18n, 'not a date', now), '');
});

test('notification time keeps the separate short calendar column from the MAIN formatter', () => {
  assert.equal(servicesNoticeTime(i18n, '2026-10-23T13:55:00Z', now), '5 minutes ago');
  assert.equal(servicesNoticeTime(i18n, '2026-10-23T09:00:00Z', now), 'Today');
  assert.equal(servicesNoticeTime(i18n, '2026-10-19T09:00:00Z', now), 'Monday');
  assert.equal(servicesNoticeTime(i18n, '2026-10-01T09:00:00Z', now), 'Oct 1');
});
