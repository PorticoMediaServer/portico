import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import type {NativeShellRoute} from '../src-next/shell/routes';
import type {Group} from '../../../packages/client-core/src/social-playback';
import type {GroupSessionSnapshot} from '../../../packages/client-core/src/group-session';
import type {NativeTogetherService} from '../src-next/screens/settings-live/together-controller';

/**
 * NM-ID-02 / BLK-TV-005 / FX-155 Together confirmation source. The actual
 * NativeTogetherTVRoom is mounted on the installed Fabric reconciler with the
 * real controller/lifetime, the product focus registry and a healthy in-memory
 * room service. It proves the confirmation is its own region with Cancel and
 * the destructive action, busy keeps that binding and re-enables it, and Back
 * restores the exact opener. Element state is UNRUN here.
 */

const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};
const stamp = '2026-10-27T00:00:00Z';
const group = (): Group => ({
  id: 'group-1', name: 'Fixture room', state: 'live', hostAuthority: {deviceId: 'device-1', playbackId: null, state: 'fresh'},
  hostMemberId: 'profile', revision: 1, playbackRevision: 1, settingsRevision: 1, governanceRevision: 1, queueRevision: 1, reconnectGeneration: 1,
  lastCommand: '', lastCommandId: '', endedReason: '', eventOrdinal: 1, createdAt: stamp, updatedAt: stamp,
  permissions: {isHost: true, canControl: true, canManageQueue: true},
  authority: {deviceId: 'device-1', playbackId: null, state: 'fresh'},
  host: {presence: 'connected', lastSeenAt: stamp, pauseAt: null, endAt: null},
  timeline: {itemId: '', currentEntryId: '', state: 'idle', anchorPositionUs: 0, anchorAt: stamp, rate: {numerator: 1, denominator: 1}, queuePosition: 0, waitUntil: null},
  settings: {shuffleEnabled: false, repeatMode: 'none'},
  sync: {noCorrectionUnderMs: 100, rateCorrectionMinimum: '0', rateCorrectionMaximum: '0', rateCorrectionMaxMs: 0, seekAtOrOverMs: 1000},
  readiness: {aggregate: 'ready', ready: 1, buffering: 0, lagging: 0, stale: 0, memberCount: 1},
  members: [{id: 'profile', displayName: 'Fixture viewer', private: false, role: 'host', state: 'joined', readiness: 'ready', positionUs: 0, reportedAt: null, presence: 'connected', joinedAt: stamp}],
  queue: {revision: 1, position: 0, entries: [], eligibility: null}, viewerMemberId: 'profile',
} as unknown as Group);
const snapshot = (busy = false): GroupSessionSnapshot => ({phase: 'live', group: group(), busy, queue: group().queue} as unknown as GroupSessionSnapshot);

test('NM-ID-02: the real Together confirmation keeps its region, busy binding, Back and opener', async () => {
  let endFlight: ReturnType<typeof deferred> | undefined;
  const app = await standardHomeApp({accountRole: 'owner', mountUI: false});
  const host = await fabricHost();
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {createFocusRegistry, focusTestID} = await import('../src-next/kit/focus/registry.ts');
    const {FocusProvider} = await import('../src-next/kit/focus/FocusRegion.tsx');
    const {ThemeProvider} = await import('../src-next/kit/theme.ts');
    const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime.ts');
    const {NativeTogetherController} = await import('../src-next/screens/settings-live/together-controller.ts');
    const {NativeTogetherTVRoom} = await import('../src-next/screens/settings-live/TogetherTVRoom.tsx');
    const {tvFocusIds} = await import('../src-next/shell/tv-focus.ts');
    const {Platform} = await import('react-native');

    let context: NativeShellBootstrapContext | undefined;
    runtime = await restoreNativeShellRuntime(next => {context = next; return createNativeShellBindings(next);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    assert.ok(context);
    const booted = context;
    const route: NativeShellRoute = {kind: 'together'};
    assert.equal(booted.navigation.open(route, booted.authority()), true);
    const priorTV = Platform.isTV;
    (Platform as {isTV: boolean}).isTV = true;

    // One healthy in-memory room service; the room's own controller and lifetime run unchanged.
    let current = snapshot();
    const listeners = new Set<() => void>();
    const publish = (next: GroupSessionSnapshot) => {current = next; for (const listener of [...listeners]) listener();};
    const service = {
      getSnapshot: () => current,
      subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
      refreshDirectory: async () => true, open: async () => true, create: async () => true, join: async () => true, dismiss: async () => true,
      serverNow: () => Date.parse(stamp), refreshQueue: async () => true, transport: async () => true, invite: async () => true,
      makeHost: async () => true, queueRemove: async () => true,
      leave: async () => {publish(snapshot()); return true;},
      end: async () => {endFlight = deferred(); publish(snapshot(true)); await endFlight.promise; publish(snapshot(false)); return true;},
    } as unknown as NativeTogetherService;
    const lifetime = new NativeScreenLifetime(booted);
    const controller = new NativeTogetherController(lifetime, service);
    // useSyncExternalStore compares snapshots by identity: an idle bounded media
    // source publishes one stable object, replaced only by a real media read.
    const mediaSnapshot = Object.freeze({items: Object.freeze({})});
    const media = {select: () => {}, subscribe: () => () => {}, getSnapshot: () => mediaSnapshot} as unknown as import('../src-next/screens/settings-live/together-media').NativeTogetherMedia;
    const ports = {context: booted};
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
    // The Dialog moves initial focus on its own enter delay; a captured focus assertion waits
    // for that actual timer rather than assuming it fired with the first settle.
    const settleOverlay = async () => {await settle(); await new Promise<void>(done => setTimeout(done, 240)); await settle();};
    const footer = tvFocusIds(booted.authority()).surface('together.room.footer');
    const Room = () => {
      const state = React.useSyncExternalStore(controller.subscribe, controller.getSnapshot);
      return React.createElement(NativeTogetherTVRoom, {state, group: state.group!, controller, media, ports, runtime});
    };
    try {
      await host.render(React.createElement(ThemeProvider, {width: 1920, children: React.createElement(FocusProvider, {registry, children: React.createElement(Room)})}));
      await settle();
      const end = nodeWithTestID('together-end');
      assert.ok(end, 'the host room offers End for everyone');
      assert.equal(registry.focus({regionId: footer, childId: 'together-end'}), true, 'the opener is focused in its footer region');
      press(end);
      await settleOverlay();
      assert.ok(registry.child({regionId: 'together-confirm', childId: 'cancel'}), 'the confirmation has its own root and Cancel leaf');
      assert.ok(registry.child({regionId: 'together-confirm', childId: 'confirm'}), 'its destructive action is a distinct leaf');
      assert.equal(registry.capture()?.regionId, 'together-confirm');
      assert.equal(registry.capture()?.childId, 'cancel', 'initial safe focus is Cancel');
      // The drawn host may omit a false `disabled`; the offered leaf and its real handler are
      // what prove the action is present and enabled, not a missing node reading as undefined.
      const offered = leafNode('together-confirm', 'confirm');
      assert.ok(offered, 'the confirmation offers its destructive action leaf');
      assert.equal(typeof offered!.props.onPress, 'function', 'the offered action has its real press handler');
      assert.equal(offered!.props.disabled ?? false, false, 'the enabled action is not disabled');

      // Confirm is busy while the real end() is pending; the same binding stays and is disabled.
      press(offered!);
      await settle();
      const confirm = leafNode('together-confirm', 'confirm');
      assert.ok(confirm, 'the busy action keeps the same leaf identity');
      assert.equal(confirm!.props.disabled, true, 'busy makes that binding ineligible');
      endFlight!.resolve(); endFlight = undefined;
      await settleOverlay();
      assert.equal(registry.child({regionId: 'together-confirm', childId: 'confirm'}), undefined, 'the accepted end closes its dialog');

      // Back dismisses and restores the exact opener (returnFocusRef=the End button).
      press(nodeWithTestID('together-end'));
      await settle();
      assert.ok(registry.child({regionId: 'together-confirm', childId: 'cancel'}));
      await app.back();
      await settleOverlay();
      assert.equal(registry.child({regionId: 'together-confirm', childId: 'cancel'}), undefined, 'Back closes without ending');
      assert.equal(registry.capture()?.regionId, footer);
      assert.equal(registry.capture()?.childId, 'together-end', 'Back returns to the original End opener');

      // The host Leave path uses the same root but returns to the Leave opener.
      const leave = nodeWithTestID('together-leave');
      assert.ok(leave);
      registry.focus({regionId: footer, childId: 'leave'});
      press(leave);
      await settleOverlay();
      assert.ok(registry.child({regionId: 'together-confirm', childId: 'cancel'}));
      await app.back();
      await settleOverlay();
      assert.equal(registry.capture()?.childId, 'leave', 'the Host Leave opening returns to Leave');
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
