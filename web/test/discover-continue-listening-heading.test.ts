import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import {discoveryHeading} from '../src/screens/browse/discovery-heading.ts';

const heading = (key: string, fallback: string) => ({key, fallback});

test('a music or audiobook library\'s Discover heads its continuation "Continue listening", read by its section id (desktop-listen #books, phone-app)', () => {
  assert.equal(discoveryHeading({id: 'continue_listening', heading: heading('home.row.continueListening', 'Continue Listening')}), 'Continue listening');
  // Every other shelf keeps the server's named code.
  assert.equal(discoveryHeading({id: 'continue_watching', heading: heading('home.row.continueWatching', 'Continue Watching')}), 'Continue Watching');
  assert.equal(discoveryHeading({id: 'recently_added', heading: heading('home.row.recentlyAdded', 'Recently added')}), 'Recently added');
});

test('Home keeps "Continue Listening" in title case beside "Continue Watching": the server\'s continue_listening row and listen_next alike', () => {
  // /v1/home/rows/continue_listening names itself with the same shared code Discover's shelf carries.
  assert.equal(serverTextLabel(defaultI18n, {code: 'home.row.continueListening', fallback: 'Continue Listening'}), 'Continue Listening');
  assert.equal(defaultI18n.t('next.home.continueListening'), 'Continue Listening');
  assert.equal(defaultI18n.t('next.home.continueWatching'), 'Continue Watching');
});
