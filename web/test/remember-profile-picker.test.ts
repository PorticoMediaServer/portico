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
const {AccountProfilesView} = await import('../src/screens/accounts/AccountProfilesView.tsx');
const {ServerProfilePIN} = await import('../src/kit/accounts/ServerProfiles.tsx');

const profiles = [{id: 'demo', name: 'Demo', eligible: true, primary: true}, {id: 'alex', name: 'Alex', eligible: true, pinRequired: true}];

function renderPicker(rememberProfiles: boolean) {
  const state = {phase: 'profiles', snapshot: {profiles, rememberProfiles}, server: {name: 'Portico demo'}};
  const binding = {subscribe: () => () => {}, getSnapshot: () => state, loadProfiles() {}, chooseProfile() {}, profileRecovery() {}};
  return renderToStaticMarkup(createElement(AccountProfilesView, {binding: binding as never, onSwitchServer() {}}));
}

test('the real profile picker starts checked when the server remembers', () => {
  const markup = renderPicker(true);
  assert.match(markup, /checked=""[^>]*\/?>Remember this profile on this device/);
  assert.doesNotMatch(markup, /browser tab/);
});

test('the picker and PIN page only offer remembering where the server allows it', () => {
  assert.doesNotMatch(renderPicker(false), /Remember this profile/);
  const pin = {serverName: 'Portico demo', profile: profiles[1]!, onComplete() {}, onOtherProfiles() {}, onForgotPIN() {}, onSwitchServer() {}, autoFocus: false};
  assert.doesNotMatch(renderToStaticMarkup(createElement(ServerProfilePIN, pin)), /Remember this profile/);
  assert.match(renderToStaticMarkup(createElement(ServerProfilePIN, {...pin, remember: true, onRememberChange() {}})), /checked=""[^>]*\/?>Remember this profile on this device/);
});
