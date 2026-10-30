import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {nativeServerHost} from '../src-next/shell/model';

const {react} = hooks();
let television = true;
const {renderNativeSettingsPanel} = await screenModule(new URL('../src-next/screens/settings-live/settings-panels.tsx', import.meta.url), {
  react, 'react-native': {},
  '../accounts/AccountRoutes': {ProfileRoute: 'ProfileRoute'},
  './NativeAccountSecurityPanels': {NativeAccountSecurityPanel: 'NativeAccountSecurityPanel'},
  './DeviceAboutPanel': {DeviceAboutPanel: 'DeviceAboutPanel'},
  './NativeConnectionRows': {NativeConnectionStatusRow: 'NativeConnectionStatusRow', NativeConnectionAddressRow: 'NativeConnectionAddressRow', NativeTestConnectionRow: 'NativeTestConnectionRow', NativeInsecureConnectionsRow: 'NativeInsecureConnectionsRow'},
  './owner-libraries/IntroCreditsReview': {IntroCreditsReview: 'IntroCreditsReview'},
  '../../../../../packages/client-core/src/installation': {installationIdentity: () => undefined},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {},
  '../../kit/controls': {},
  '../../../../../packages/client-core/src/freshness-actions': {},
  '../../../../../packages/client-core/src/playback-v1/http': {},
  '../../../../../packages/client-core/src/playback-v1/local-http': {},
  '../../../../../packages/client-core/src/playback-v1/sessions': {},
  '../../../../../packages/client-core/src/playback-controls': {},
  '../../../../../packages/client-core/src/server-admin/now-playing': {},
  '../../kit/overlays': {}, '../../kit/shell': {SettingsRow: 'SettingsRow'},
  '../../kit/shell/visual': {}, '../../kit/theme': {useTheme: () => ({isTV: television})},
  '../../shell/model': {nativeServerHost},
  '../../../../../packages/i18n/src': {defaultI18n: {bytes: (value: number) => String(value)}},
});

test('custom Settings navigation rows provide stable distinct focus identities before TV activation', () => {
  const opened: unknown[] = [];
  const deps = {context: {serverName: 'Actual server'}, t: (id: string) => id, openRoute: (route: unknown) => opened.push(route)};
  const ids = new Set<string>();
  for (const id of ['profiles', 'servers', 'equalizer']) {
    const element = renderNativeSettingsPanel(id, deps, {});
    const row = typeof element.type === 'function' ? element.type(element.props) : element;
    assert.equal(row.type, 'SettingsRow');
    assert.equal(row.props.testID, `settings-panel-${id}`);
    assert.equal(ids.has(row.props.testID), false);
    ids.add(row.props.testID);
    row.props.onPress();
  }
  assert.deepEqual(opened, [{kind: 'profiles'}, {kind: 'servers'}, {kind: 'settings-equalizer'}]);
});

test('the profile panel embeds management in the exact Settings route without navigating to the picker', () => {
  const opened: unknown[] = [];
  const route = {kind: 'shell', destination: {kind: 'settings'}};
  const context = {navigation: {getSnapshot: () => ({route, revision: 4})}};
  const deps = {context, openRoute: (value: unknown) => opened.push(value)};
  television = false;
  try {
    const panel = renderNativeSettingsPanel('profiles', deps, {});
    const element = panel.type(panel.props);
    assert.equal(element.type, 'ProfileRoute');
    assert.equal(element.props.context, context);
    assert.equal(element.props.route, route);
    assert.equal(element.props.embedded, true);
    element.props.onBack();
    assert.deepEqual(opened, []);
  } finally {television = true;}
});

test('the enabled security panels receive their exact kind and Settings dependencies', () => {
  const deps = {context: {}, lifetime: {}, t: (id: string) => id, openRoute() {}};
  const runtime = {};
  for (const kind of ['password', 'twoStep', 'devices']) {
    const element = renderNativeSettingsPanel(kind, deps, runtime);
    assert.equal(element.type, 'NativeAccountSecurityPanel');
    assert.equal(element.props.kind, kind);
    assert.equal(element.props.context, deps.context);
    assert.equal(element.props.lifetime, deps.lifetime);
    assert.equal(element.props.runtime, runtime);
  }
});
