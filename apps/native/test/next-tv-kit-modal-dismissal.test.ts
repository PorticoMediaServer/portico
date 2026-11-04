import test from 'node:test';
import assert from 'node:assert/strict';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';
import {tvKitOverlays} from './helpers/tv-kit-overlays.mjs';

// DS-427, 4 Nov amendment (DECISIONS, "4 Nov 2026: TV focus, undrawn parity and notices"): on Apple TV, focus returns
// once a closing overlay has fully gone, native dismissal included. #835 did that for the refused-link page; this holds
// it for every other kit overlay drawn as a native Modal on TV: dialogs, sheets (and the TV Menu and Play on, which are
// sheets), modal popover menus, the player's panels (a modal TVPanelTransition) and the live mini guide. Runs the real
// overlays, overlay lifecycle and useNativeModalPresence on the fake engine's window model (D12-D16).
type Kind = 'dialog' | 'sheet' | 'menu' | 'popover' | 'panel' | 'guide';
const KINDS: readonly Kind[] = ['dialog', 'sheet', 'menu', 'popover', 'panel', 'guide'];
const pause = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

async function scene(platform: 'android' | 'tvos', options?: {wait: number}) {
  const kit = await tvFocusKit({platform}), {engine, React, e, focus, Stop} = kit, n = engine.native as any;
  const o: any = await tvKitOverlays(kit, options), registry = focus.createFocusRegistry();
  const exits: string[] = [];
  /** A player panel as WatchOverlays draws one: its own Modal inside a modal TVPanelTransition. */
  const Panel = ({close}: {close: () => void}) => {
    const modal = o.useTVPanelModal();
    o.useOverlayLifecycle(true, true, close, undefined, undefined, 0, 'panel');
    return e(n.Modal, {transparent: true, visible: modal.visible, onDismiss: modal.onDismiss, onRequestClose: close},
      e(o.FocusBoundary, {modal: true, testID: 'panel'}, e(Stop, {id: 'panel-ok', onPress: close})));
  };
  const overlay = (kind: Kind, open: boolean, close: () => void, anchor: any): any => {
    const change = (next: boolean) => {if (!next) close();};
    if (kind === 'dialog') return e(o.Dialog, {open, onOpenChange: change, title: 'Dialog', testID: 'dialog', scrollable: false}, e(Stop, {id: 'dialog-ok', onPress: close}));
    if (kind === 'sheet') return e(o.BottomSheet, {open, onOpenChange: change, title: 'Sheet', testID: 'sheet', showClose: false, scrollable: false,
      onExited: () => exits.push('sheet')}, e(Stop, {id: 'sheet-ok', onPress: close}));
    if (kind === 'menu') return e(o.Menu, {open, onOpenChange: change, title: 'Menu', testID: 'menu', items: [{id: 'ok', label: 'OK', onSelect: close}]});
    if (kind === 'popover') return e(o.Popover, {open, onOpenChange: change, anchorRef: anchor, modal: true, title: null, testID: 'popover'}, e(Stop, {id: 'popover-ok', onPress: close}));
    if (kind === 'panel') return e(PanelHost, {open, close});
    return e(o.NativeMiniGuide, {open, onExited: () => exits.push('guide'), onClose: close, onSelect: close, onFullGuide() {}, now: 0, timezone: 'UTC',
      rows: [{channel: {kind: 'live', id: '1', number: '1', name: 'One', tuneAvailable: true}, watching: true}]});
  };
  /** WatchOverlays' retention: the panel stays mounted until its transition reports it exited. */
  const PanelHost = ({open, close}: {open: boolean; close: () => void}) => {
    const presence = o.useTVPanelPresence(open ? {} : undefined, 'scope');
    return presence.value ? e(o.TVPanelTransition, {modal: true, open: presence.open, onExited: () => {presence.exited(); exits.push('panel');}}, e(Panel, {close})) : null;
  };
  let setOpen: (value: boolean) => void = () => {};
  const Shell = ({kind, second}: {kind: Kind; second?: Kind}) => {
    const [open, set] = React.useState(false), [next, setNext] = React.useState(false), anchor = React.useRef(null);
    setOpen = set;
    // `second`: closing the first opens another over it at once (the stacked sheet → dialog case).
    const close = () => {set(false); if (second) setNext(true);};
    return e(focus.FocusProvider, {registry}, e(n.View, {style: {flex: 1}},
      e(focus.FocusRegion, {id: 'home.row', orientation: 'horizontal', style: {flexDirection: 'row', gap: 20}}, e(Stop, {id: 'film'}), e(Stop, {id: 'show', ref: anchor})),
      overlay(kind, open, close, anchor),
      second ? overlay(second, next, () => setNext(false), anchor) : null));
  };
  const ok = (kind: Kind) => kind === 'guide' ? 'tv-mini-guide-live//1' : kind === 'menu' ? 'menu-item-ok' : `${kind}-ok`;
  const opened = async (kind: Kind, second?: Kind) => {
    await engine.render(e(Shell, {kind, second}));
    await engine.focus('show');
    setOpen(true);
    await engine.settle(); await pause(200); await engine.settle();
    assert.equal(engine.focused, ok(kind), `${kind}: focus in the overlay`);
    assert.equal(engine.windows().length, 1, `${kind}: its own window`);
  };
  /** The overlay's exit (reduced motion) and any timers after it. */
  const exit = async () => {for (let i = 0; i < 4; i++) {await pause(60); await engine.settle();}};
  return {engine, opened, exit, exits, ok, open: (value: boolean) => setOpen(value)};
}

test('on Android TV and Fire TV every kit Modal overlay goes with its exit, and focus returns where it was', async () => {
  for (const kind of KINDS) for (const key of ['select', 'back'] as const) {
    const {engine, opened, exit, exits} = await scene('android'), label = `${kind}, ${key}`;
    await opened(kind);
    await engine.press(key);
    await exit();
    assert.equal(engine.windows().length, 0, `${label}: gone`);
    if (kind !== 'guide') assert.equal(engine.focused, 'show', `${label}: focus back where it was`);
    if (kind === 'sheet' || kind === 'panel' || kind === 'guide') assert.deepEqual(exits, [kind], label);
    await engine.unmount();
  }
});

test('on Apple TV every kit Modal overlay stays until tvOS reports it dismissed; focus returns, and it has exited, only then', async () => {
  for (const kind of KINDS) {
    const {engine, opened, exit, exits, ok} = await scene('tvos', {wait: 60_000}), label = kind;
    await opened(kind);
    await engine.press('select');
    await exit();
    assert.deepEqual(engine.windows(), [{testID: undefined, dismissing: true}], `${label}: its Modal is still dismissing`);
    // The guide disables its rows as it closes (nothing left to focus); every other overlay keeps its control.
    if (kind === 'guide') assert.notEqual(engine.focused, 'show', `${label}: focus waits for the dismissal`);
    else assert.equal(engine.focused, ok(kind), `${label}: focus waits for the dismissal`);
    assert.deepEqual(exits, [], `${label}: not exited while it is still on screen`);
    await engine.finishDismissals();
    await exit();
    assert.equal(engine.windows().length, 0, label);
    if (kind !== 'guide') assert.equal(engine.focused, 'show', `${label}: focus back where it was`);
    if (kind === 'sheet' || kind === 'panel' || kind === 'guide') assert.deepEqual(exits, [kind], label);
    await engine.unmount();
  }
});

test('tvOS Menu (D16): UIKit dismisses the overlay itself and reports onDismiss first; the overlay goes as soon as its exit ends, not on the fallback', async () => {
  for (const kind of KINDS) {
    const {engine, opened, exit, exits, ok} = await scene('tvos', {wait: 60_000}), label = kind;
    await opened(kind);
    await engine.press('menu');
    assert.equal(engine.focused, ok(kind), `${label}: focus waits for the dismissal`);
    await engine.finishDismissals();
    await exit();
    assert.equal(engine.windows().length, 0, label);
    if (kind !== 'guide') assert.equal(engine.focused, 'show', `${label}: focus back where it was`);
    if (kind === 'sheet' || kind === 'panel' || kind === 'guide') assert.deepEqual(exits, [kind], `${label}: exited with no wait for the fallback`);
    await engine.unmount();
  }
});

test('Apple TV: a dismissal tvOS never reports (D15) ends on the bounded fallback', async () => {
  for (const kind of KINDS) {
    const {engine, opened, exit, exits} = await scene('tvos', {wait: 60}), label = kind;
    await opened(kind);
    await engine.press('select');
    await exit(); await exit();
    assert.equal(engine.windows().length, 0, `${label}: gone after the fallback`);
    if (kind === 'sheet' || kind === 'panel' || kind === 'guide') assert.deepEqual(exits, [kind], label);
    await engine.unmount();
  }
});

test('only the top layer takes focus: a dialog opened over a closing sheet keeps focus when the sheet has gone', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const {engine, opened, exit} = await scene(platform, {wait: 60_000});
    await opened('sheet', 'dialog');
    await engine.press('select');
    await exit();
    if (platform === 'tvos') {await engine.finishDismissals(); await exit();}
    assert.deepEqual(engine.windows().map(window => window.dismissing), [false], `${platform}: the dialog is the one window left`);
    assert.equal(engine.focused, 'dialog-ok', `${platform}: the closed sheet does not pull focus under the dialog`);
    await engine.unmount();
  }
});

// Sol 8's P2 on #844 (comment 6638349337): each dismissal report belongs to its own presentation. A modal popover hides
// its Modal as soon as it closes, while its exit keeps it present; reopened before tvOS reports that dismissal, it must
// not ask UIKit to show a view controller that is still being dismissed, and that old report must not end a later close.
test('Apple TV: a popover reopened while its early hide is still dismissing is shown again only once that dismissal ends', async () => {
  const {engine, opened, exit, open} = await scene('tvos', {wait: 60_000});
  await opened('popover');
  open(false);
  await engine.settle();
  assert.deepEqual(engine.windows().map(window => window.dismissing), [true], 'hidden at once: its dismissal has started');
  open(true);
  await engine.settle(); await exit();
  assert.deepEqual(engine.windows().map(window => window.dismissing), [true], 'not shown again while UIKit is still dismissing it');
  await engine.finishDismissals(); await exit();
  assert.deepEqual(engine.windows().map(window => window.dismissing), [false], 'shown again once the old dismissal has ended');
  open(false);
  await engine.settle(); await exit();
  assert.deepEqual(engine.windows().map(window => window.dismissing), [true], 'the second close waits for its own dismissal');
  await engine.finishDismissals(); await exit();
  assert.equal(engine.windows().length, 0);
  assert.equal(engine.focused, 'show', 'focus back where it was');
  await engine.unmount();
});

async function probe(platform: 'android' | 'tvos', wait: number) {
  const kit = await tvFocusKit({platform}), o: any = await tvKitOverlays(kit, {wait});
  let seen: {mounted: boolean; visible: boolean; onDismiss: () => void} = undefined as any;
  const Probe = ({present, shown}: {present: boolean; shown: boolean}) => {seen = o.useNativeModalPresence(present, shown); return null;};
  const at = async (present: boolean, shown: boolean) => {await kit.engine.render(kit.e(Probe, {present, shown})); return seen;};
  const report = async () => {seen.onDismiss(); await kit.engine.settle(); return seen;};
  return {at, report, engine: kit.engine};
}

test('a dismissal report from an earlier presentation never ends a later close (Sol 8\'s second-close repro)', async () => {
  const {at, report, engine} = await probe('tvos', 60_000);
  let now = await at(true, true);
  assert.deepEqual([now.mounted, now.visible], [true, true]);
  now = await at(true, false); // the popover hides at once; its exit keeps it present
  assert.equal(now.visible, false);
  await at(true, true); // reopened before tvOS reports the first dismissal
  now = await report(); // that first dismissal is reported
  assert.equal(now.visible, true, 'shown again once the first dismissal has ended');
  now = await at(false, false); // the reopened popover closes and its exit ends
  assert.equal(now.mounted, true, 'the old report did not end this close: it waits for its own dismissal');
  now = await report();
  assert.equal(now.mounted, false, 'gone once its own dismissal is reported');
  await engine.unmount();
});

test('a dismissal the fallback gave up on (D15) does not end a later close when its report comes late', async () => {
  const {at, report, engine} = await probe('tvos', 60);
  await at(true, true);
  let now = await at(false, false);
  assert.equal(now.mounted, true, 'waiting for the dismissal');
  await pause(120); await engine.settle();
  now = await at(false, false);
  assert.equal(now.mounted, false, 'gone on the fallback');
  now = await at(true, true);
  assert.equal(now.visible, true, 'opened again');
  now = await report(); // the lost dismissal reports late
  now = await at(false, false);
  assert.equal(now.mounted, true, 'the late report belongs to the lost dismissal: this close still waits');
  now = await report();
  assert.equal(now.mounted, false);
  await engine.unmount();
});

test('Android TV: the Modal follows presence and its hide with no wait, whatever is reported', async () => {
  const {at, report, engine} = await probe('android', 60_000);
  assert.equal((await at(true, true)).visible, true);
  let now = await at(true, false);
  assert.deepEqual([now.mounted, now.visible], [true, false]);
  now = await at(true, true);
  assert.equal(now.visible, true, 'shown again at once');
  await report();
  now = await at(false, false);
  assert.equal(now.mounted, false, 'gone with its presence');
  await engine.unmount();
});
