import assert from 'node:assert/strict';
import test from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {SettingsPageFailure, settingsRefusal} from '../src/kit/settings/SettingsPageFailure.tsx';

const noop = () => {};
const server = {name: 'Portico demo', openMySettings: noop};
const page = (error: unknown, owner = true) => renderToStaticMarkup(createElement(SettingsPageFailure, {retry: noop, home: noop, error, server: owner ? server : undefined}));

test('a refused owner read is told apart from a failed one', () => {
  assert.equal(settingsRefusal({status: 403, code: 'forbidden'}), 'owner');
  assert.equal(settingsRefusal({status: 403}), 'owner');
  assert.equal(settingsRefusal({code: 'not_permitted'}), 'owner');
  assert.equal(settingsRefusal({status: 403, code: 'unauthorized'}), 'owner');
  assert.equal(settingsRefusal({status: 401, code: 'unauthorized'}), undefined);
  assert.equal(settingsRefusal({status: 403, code: 'profile_pin_manage'}), 'pin');
  assert.equal(settingsRefusal({status: 403, code: 'prepared_limit'}), undefined);
  assert.equal(settingsRefusal({status: 500, code: 'internal'}), undefined);
  assert.equal(settingsRefusal(new Error('network')), undefined);
  assert.equal(settingsRefusal(undefined), undefined);
});

test('an owner page whose read was refused draws states frame 13c, not "Something didn\'t load"', () => {
  const html = page({status: 403, code: 'forbidden'});
  assert.match(html, /Only the owner can open this/);
  assert.match(html, /Server settings for Portico demo belong to the server owner\./);
  assert.match(html, /Server settings/);
  assert.match(html, /Open my settings/);
  assert.match(html, /Go to Home/);
  assert.doesNotMatch(html, /Something didn|ran into a problem/);
});

test('a management PIN refusal asks for the PIN behind the shell\'s dialog, on any page', () => {
  for (const owner of [true, false]) {
    const html = page({status: 403, code: 'profile_pin_manage'}, owner);
    assert.match(html, /Enter profile PIN/);
    assert.match(html, /Enter this profile’s PIN to manage this account\./);
    assert.match(html, /Try again/);
    assert.doesNotMatch(html, /Something didn|Only the owner/);
  }
});

test('a refusal on an account page, and any other failure, stays the page failure', () => {
  for (const html of [page({code: 'forbidden'}, false), page(new Error('network')), page(undefined)]) {
    assert.match(html, /This page ran into a problem/);
    assert.doesNotMatch(html, /Only the owner/);
  }
});
