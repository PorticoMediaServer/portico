import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ServerProfiles, ServerProfilePIN} = await import('../src/kit/accounts/ServerProfiles.tsx');

const profiles = [{id: 'demo', name: 'Demo', eligible: true, primary: true}, {id: 'alex', name: 'Alex', eligible: true, pinRequired: true}];
const base = {serverName: 'Portico demo', profiles, onSelect() {}, onSwitchServer() {}};

// auth.html frame 13 (J-31): Remember this profile on this device sits under the faces, whether or
// not a profile has a PIN, and only where the server would remember (Remember PIN for isn't Off).
test('the picker offers Remember this profile on this device, unticked, when the server remembers', () => {
  const markup = renderToStaticMarkup(createElement(ServerProfiles, {...base, remember: false, onRememberChange() {}}));
  assert.match(markup, /Remember this profile on this device/);
  assert.doesNotMatch(markup, /checked/);
  assert.doesNotMatch(markup, /browser tab/);
});

test('the picker and PIN page leave the checkbox out where the server would not remember', () => {
  assert.doesNotMatch(renderToStaticMarkup(createElement(ServerProfiles, base)), /Remember this profile/);
  const pin = {serverName: 'Portico demo', profile: profiles[1]!, onComplete() {}, onOtherProfiles() {}, onForgotPIN() {}, onSwitchServer() {}, autoFocus: false};
  assert.doesNotMatch(renderToStaticMarkup(createElement(ServerProfilePIN, pin)), /Remember this profile/);
  assert.match(renderToStaticMarkup(createElement(ServerProfilePIN, {...pin, remember: true, onRememberChange() {}})), /checked=""[^>]*\/?>Remember this profile on this device/);
});
