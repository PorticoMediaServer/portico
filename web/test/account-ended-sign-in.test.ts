import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {savedSignInUnusable} from '../src/shell/connection-retry.ts';

// AU-5: a dropped or changed saved sign-in left /profiles on "Loading profiles" forever and the Servers dialog blank.
// AU-6: in the phone chooser an ended sign-in read as "can't reach", and its Try again retried a row already removed.
const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('Who\'s watching moves on when its saved sign-in ended: sign-in on the server\'s own page, else the chooser', () => {
  const source = read('../src/screens/accounts/Profiles.route.tsx');
  assert.match(source, /if \(state\.phase === 'welcome' && state\.ended\) void router\.navigate\(forgetDestination\(currentServedServerOrigin\(\)\)\.to === 'sign-in' \? \{to: '\/sign-in', replace: true\} : \{to: '\/servers', replace: true\}\);/);
});

test('the sign-in view never draws an empty page, and an ended sign-in signs in again or goes back to choosing', () => {
  const source = read('../src/screens/accounts/AccountSignInView.tsx');
  assert.match(source, /else if \(state\.phase === 'welcome' && state\.ended\) \{/);
  assert.match(source, /if \(destination\.to === 'sign-in'\) void entry\.inspect\(destination\.origin\);/);
  assert.match(source, /<Button onClick=\{cancel\}>\{defaultI18n\.t\('settings\.switchServer'\)\}<\/Button>\n  <\/AuthStage>;\n\}/, 'Switch server is offered even with nothing to say');
});

test('an ended saved sign-in in the chooser opens sign-in for its address instead of a dead Try again', () => {
  for (const code of ['refresh_refused', 'session_replaced', 'saved_sign_in_damaged']) assert.equal(savedSignInUnusable({code, signOut: true}), 'ended', code);
  assert.equal(savedSignInUnusable({code: 'request_failed', retryable: true}), undefined, 'a server that did not answer is still can\'t reach');
  const source = read('../src/screens/accounts/Servers.route.tsx');
  assert.match(source, /if \(savedSignInUnusable\(failure\) === 'ended'\) \{\n\s+setAttempt\(undefined\); setError\(undefined\); setOrigin\(saved\.connection\.logicalOrigin\); setMode\('entry'\);\n\s+void entry\.inspect\(saved\.connection\.logicalOrigin\);/);
});
