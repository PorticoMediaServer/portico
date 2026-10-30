import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

test('the one settings mount registers the Settings destination and owner Dashboard on the family factories', async () => {
  const registrations: {kind: string; route: string; factory: (context: unknown) => unknown}[] = [];
  const built: unknown[] = [];
  const mount = await screenModule(new URL('../src-next/shell/settings-mount.ts', import.meta.url), {
    react: {default: {}},
    // A side-effect import only loads the notification route registrations, which
    // this mount test neither composes nor asserts; the harness supplies the
    // namespace instead of evaluating a .tsx module under node.
    '../screens/settings-live/server/NotificationDestinations': {},
    '../screens/settings-live/SettingsRoute': {
      createNativeTVServerSettingsRenderer: (context: unknown) => ({render: (route: unknown) => ({screen: 'server-settings', route})}),
      createNativeEqualizerSettingsRenderer: (context: unknown) => {built.push(['equalizer', context]); return {render: (route: unknown) => ({screen: 'equalizer', route})};},
      createNativeSettingsRenderer: (context: unknown) => {built.push(['settings', context]); return {render: (route: unknown) => ({screen: 'settings', route})};},
      createNativeServerDashboardRenderer: (context: unknown) => {built.push(['dashboard', context]); return {render: (route: unknown) => ({screen: 'dashboard', route})};},
    },
    '../screens/settings-live/owner-libraries/LibrarySettingsRoute': {createNativeLibrarySettingsRenderer: (context: unknown) => {built.push(['library', context]); return {}; }},
    './route-registry': {nativeShellRoutes: {
      registerDestination: (kind: string, factory: never) => registrations.push({kind: 'destination', route: kind, factory}),
      registerRoute: (kind: string, factory: never) => registrations.push({kind: 'route', route: kind, factory}),
    }},
  });
  assert.deepEqual(registrations.map(value => [value.kind, value.route]), [['destination', 'settings'], ['route', 'server-dashboard'], ['route', 'server-settings'], ['route', 'settings-equalizer'], ['route', 'library-settings']]);
  assert.deepEqual(built, [], 'registering a factory composes nothing before a viewer binds');
  const context = {serverName: 'Demo server'};
  const settings = registrations[0]!.factory(context) as {render: (route: unknown) => unknown};
  assert.deepEqual(built, [['settings', context]]);
  const route = {kind: 'shell', destination: {kind: 'settings'}};
  assert.deepEqual(settings.render(route), {screen: 'settings', route});
  const dashboard = registrations[1]!.factory(context) as {render: (route: unknown) => unknown};
  assert.deepEqual(dashboard.render({kind: 'server-dashboard'}), {screen: 'dashboard', route: {kind: 'server-dashboard'}});
  const server = registrations[2]!.factory(context) as {render: (route: unknown) => unknown};
  assert.deepEqual(server.render({kind: 'server-settings'}), {screen: 'server-settings', route: {kind: 'server-settings'}});
  const equalizer = registrations[3]!.factory(context) as {render: (route: unknown) => unknown};
  assert.deepEqual(equalizer.render({kind: 'settings-equalizer'}), {screen: 'equalizer', route: {kind: 'settings-equalizer'}});
});
