import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createI18n, resolveRegion} from '@i18n';

const i18n = createI18n(resolveRegion(undefined, {locales: ['en-US'], timeZone: 'UTC'}));

test('the auth24e2 review carries the drawn switch wording and the count sentence', () => {
  assert.equal(i18n.t('services.claim.switchTitle', {server: 'Portico demo'}), 'Switch Portico demo to Portico Accounts');
  assert.equal(i18n.t('services.claim.switchLead'),
    'You asked to move this server from local sign-in. Approve with the Portico Account that will own it.');
  assert.equal(i18n.t('services.claim.becomesOwner', {account: 'Demo'}), 'Demo becomes the owner.');
  assert.equal(i18n.t('services.claim.otherPeople', {count: 86}),
    'The other 86 people sign in again with a Portico Account. Their profiles and history are kept.');
  assert.equal(i18n.t('services.claim.otherPeople', {count: 1}),
    'The other 1 person signs in again with a Portico Account. Their profiles and history are kept.');
  assert.equal(i18n.t('services.claim.returnHint', {server: 'Portico demo'}), 'Either way, a link then takes you back to Portico demo.');
});
