import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

/**
 * NM-ID-02 / BLK-TV-005 / FX-155 Settings confirmation source. The actual
 * NativeSettingsScreen runs on the installed Fabric reconciler with its real
 * controller, lifetime and Settings ports, the product focus registry and a
 * healthy in-memory reset-recommendations receipt. It proves the confirmation
 * root and leaves, busy keeps the same binding, and Back restores the exact
 * row opener. Element state is UNRUN here.
 */

const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};

test('NM-ID-02: the real Settings confirmation keeps its region, busy binding, Back and row opener', async () => {
  let resetFlight: ReturnType<typeof deferred> | undefined;
  const app = await standardHomeApp({accountRole: 'owner', mountUI: false, nativeResponse: async (request: any, fixture: any) => {
    // The real client posts the server's `post_me_recommendations_reset` route
    // (`/v1/me/recommendations:reset`), not a slash-separated guess.
    if (request.path === '/v1/me/recommendations:reset' && request.method === 'POST') {
      if (resetFlight) await resetFlight.promise;
      return {status: 200, body: {serverId: fixture.pin.serverId, operationId: request.body.operationId, clearedNotInterested: 0}};
    }
    return undefined;
  }});
  const host = await fabricHost();
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {createFocusRegistry, focusTestID} = await import('../src-next/kit/focus/registry.ts');
    const {FocusProvider} = await import('../src-next/kit/focus/FocusRegion.tsx');
    const {ThemeProvider} = await import('../src-next/kit/theme.ts');
    const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime.ts');
    const {createBoundNativeSettingsController} = await import('../src-next/screens/settings-live/settings-controller.ts');
    const {createNativeSettingsPorts} = await import('../src-next/screens/settings-live/settings-ports.ts');
    const {NativeSettingsScreen} = await import('../src-next/screens/settings-live/SettingsScreen.tsx');
    const {tvFocusIds} = await import('../src-next/shell/tv-focus.ts');
    const {Platform} = await import('react-native');

    let context: NativeShellBootstrapContext | undefined;
    runtime = await restoreNativeShellRuntime(next => {context = next; return createNativeShellBindings(next);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    assert.ok(context);
    const booted = context;
    // The screen's own commands require the actual Settings destination to be visible,
    // exactly as the shell opens it; a detached render is not the real caller.
    const route: import('../src-next/shell/routes').NativeShellRoute = {kind: 'shell', destination: {kind: 'settings'}};
    assert.equal(booted.navigation.open(route, booted.authority()), true, 'the real navigation opened the Settings destination');
    const priorTV = Platform.isTV;
    (Platform as {isTV: boolean}).isTV = true;
    const lifetime = new NativeScreenLifetime(booted);
    const controller = createBoundNativeSettingsController(lifetime, booted);
    // Device-only rows are not the subject; this read port keeps the real screen composition.
    const devicePreferences = {subscribe: () => () => {}, read: () => undefined} as unknown as import('../src-next/screens/settings-live/device-preference-consumer').NativeDevicePreferences;
    const ports = createNativeSettingsPorts(booted, lifetime, () => {});
    const registry = createFocusRegistry();
    const nodes = () => {
      const visit = (items: readonly any[]): any[] => items.flatMap(node => [node, ...visit(node.children ?? [])]);
      // standardHomeApp materialises the product's View/Text/Pressable boundaries in its own
      // host registry; host.root only carries the rendered raw text.
      return [...visit(host.root), ...app.hosts()];
    };
    const nodeWithTestID = (testID: string) => nodes().find(node => node.props?.testID === testID);
    const leafNode = (regionId: string, childId: string) => nodeWithTestID(focusTestID(regionId, childId));
    const press = (node: any) => node.props.onPress({nativeEvent: {}, stopPropagation() {}});
    const settle = async () => {await host.settle(); await app.settle();};
    // The Dialog moves initial focus on its own enter delay and keeps its surface for the
    // exit delay; a captured focus/retirement assertion waits for that actual timer.
    const settleOverlay = async () => {await settle(); await new Promise<void>(done => setTimeout(done, 240)); await settle();};
    try {
      await host.render(React.createElement(ThemeProvider, {width: 1920, children: React.createElement(FocusProvider, {registry, children:
        React.createElement(NativeSettingsScreen, {ports, controller, runtime, devicePreferences})})}));
      await settle();
      press(nodeWithTestID('settings-page-privacy')!);
      await settle();
      const rows = tvFocusIds(booted.authority()).surface('settings.rows.privacy');
      const opener = nodeWithTestID('resetRecommendations');
      assert.ok(opener, 'the real Settings privacy page offers Reset recommendations');
      assert.equal(registry.focus({regionId: rows, childId: 'resetRecommendations'}), true, 'the opener is focused in its actual rows region');
      press(opener);
      await settleOverlay();
      assert.ok(registry.child({regionId: 'settings-action-confirm', childId: 'cancel'}), 'the confirmation has its own root and Cancel leaf');
      assert.ok(registry.child({regionId: 'settings-action-confirm', childId: 'confirm'}), 'its destructive action is a distinct leaf');
      assert.equal(registry.capture()?.regionId, 'settings-action-confirm');
      assert.equal(registry.capture()?.childId, 'cancel', 'initial safe focus is Cancel');

      // Busy while the real reset receipt is pending; the same binding stays and is disabled.
      resetFlight = deferred();
      press(leafNode('settings-action-confirm', 'confirm'));
      await settle();
      const confirm = leafNode('settings-action-confirm', 'confirm');
      assert.ok(confirm, 'the busy action keeps its region identity');
      assert.equal(confirm!.props.disabled, true, 'busy makes that binding ineligible');
      assert.equal(leafNode('settings-action-confirm', 'cancel')?.props.disabled, true, 'busy also disables Cancel');
      resetFlight.resolve(); resetFlight = undefined;
      await settleOverlay();
      assert.equal(registry.child({regionId: 'settings-action-confirm', childId: 'confirm'}), undefined, 'the accepted reset closes its dialog');

      // Back dismisses and restores the exact row opener.
      press(nodeWithTestID('resetRecommendations')!);
      await settleOverlay();
      assert.ok(registry.child({regionId: 'settings-action-confirm', childId: 'cancel'}));
      await app.back();
      await settleOverlay();
      assert.equal(registry.child({regionId: 'settings-action-confirm', childId: 'cancel'}), undefined, 'Back closes without acting');
      assert.equal(registry.capture()?.regionId, rows);
      assert.equal(registry.capture()?.childId, 'resetRecommendations', 'Back returns to the original row opener');
    } finally {
      (Platform as {isTV: boolean}).isTV = priorTV;
      controller.dispose();
      lifetime.dispose();
      await host.unmount();
    }
  } finally {
    try {
      await host.unmount();
    } finally {
      try {
        await app.unmount();
      } finally {
        try {
          app.disposeLoader();
        } finally {
          runtime?.dispose();
        }
      }
    }
  }
});
