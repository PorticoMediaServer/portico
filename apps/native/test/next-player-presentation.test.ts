import test from 'node:test';
import assert from 'node:assert/strict';
import {letterboxPlayerPicture, playerControlsRevealDeadline, resolvePlayerPresentation, type PlayerFold, type PlayerPresentationInput} from '../src-next/platform/layout/player-presentation.ts';
import {createPlatformPostureStore, type PlatformPostureSnapshot} from '../src-next/platform/platformPostureStore.ts';

// Geometry assertions do not establish device continuity or frame fidelity.
const input = (width: number, height: number, folds: readonly PlayerFold[] = []): PlayerPresentationInput => ({
  window: {width, height}, posture: {status: 'available', window: {width, height}, folds}, docked: false, panelOpen: false,
});
const vertical: PlayerFold = {bounds: {left: 445, right: 445, top: 0, bottom: 626}, orientation: 'vertical', state: 'flat'};

test('Q7 flat-wide Duo uses the whole window across the faint crease and letterboxes16:9', () => {
  const p = resolvePlayerPresentation(input(890, 626, [vertical]))!;
  assert.equal(p.mode, 'full-window');
  assert.deepEqual(p.surface, {x: 0, y: 0, width: 890, height: 626});
  assert.deepEqual(p.picture, {x: 0, y: 62.6875, width: 890, height: 500.625});
  assert.equal(p.hideStatusBar, true);
  assert.equal(p.hideNavigation, true);
  assert.equal(p.hingeBand, undefined);
});

test('Q7 flat-tall and closed return the full-width watch picture without splitting on a crease', () => {
  const fold: PlayerFold = {...vertical, bounds: {left: 420, right: 420, top: 0, bottom: 880}};
  for (const next of [input(840, 880, [fold]), input(466, 678)]) {
    const p = resolvePlayerPresentation(next)!;
    assert.equal(p.mode, 'watch');
    assert.equal(p.surface!.width, next.window.width);
    assert.equal(p.surface!.height, next.window.width * 9 / 16);
    assert.equal(p.hingeBand, undefined);
    assert.equal(p.content!.y, p.surface!.height);
  }
});

test('8123g keeps the picture on its physical page and touchable chrome outside the padded hinge band', () => {
  const fold: PlayerFold = {...vertical, state: 'half-opened', bounds: {...vertical.bounds, left: 437, right: 453}};
  const p = resolvePlayerPresentation(input(890, 626, [fold]))!;
  assert.equal(p.mode, 'book');
  assert.deepEqual(p.hingeBand, {x: 421, y: 0, width: 48, height: 626});
  assert.equal(p.surface!.x + p.surface!.width, fold.bounds.left);
  assert.deepEqual(p.content, {x: 469, y: 0, width: 421, height: 626});
  assert.equal(p.controls!.x + p.controls!.width, p.hingeBand!.x);
  assert.equal(p.details!.x + p.details!.width, p.hingeBand!.x);
  assert.equal(p.picture!.x + p.picture!.width, fold.bounds.left, 'the picture never crosses the physical hinge');
  const duo = resolvePlayerPresentation(input(890, 626, [{...vertical, state: 'half-opened'}]))!;
  assert.deepEqual(duo.picture, {x: 0, y: 0, width: 445, height: 250.3125});
  assert.equal(duo.controls!.width, 429, '3g top/bottom/seek chrome ends16pt short of the crease');
});

test('tabletop4/5 keep picture above the horizontal hinge and all controls below, always shown', () => {
  for (const [width, height, top, bottom] of [[626, 890, 437, 453], [840, 880, 432, 448]]) {
    const fold: PlayerFold = {orientation: 'horizontal', state: 'half-opened', bounds: {left: 0, right: width!, top: top!, bottom: bottom!}};
    const p = resolvePlayerPresentation(input(width!, height!, [fold]))!;
    assert.equal(p.mode, 'tabletop');
    assert.equal(p.surface!.height, top! - 16);
    assert.equal(p.controls!.y, bottom! + 16);
    assert.equal(p.controlsAutoHide, false);
    assert.equal(p.controls!.y + p.controls!.height, height);
  }
});

test('foldable5 splits the wide tabletop into the controls column and the episodes that follow', () => {
  const fold: PlayerFold = {orientation: 'horizontal', state: 'half-opened', bounds: {left: 0, right: 880, top: 420, bottom: 420}};
  const wide = resolvePlayerPresentation({...input(880, 840, [fold]), upNext: true})!;
  assert.equal(wide.mode, 'tabletop');
  assert.deepEqual(wide.controls, {x: 28, y: 436, width: 508, height: 370});
  assert.deepEqual(wide.episodes, {x: 576, y: 440, width: 276, height: 238});
  assert.equal(wide.hingeBand!.height, 32);
  assert.deepEqual(wide.picture, letterboxPlayerPicture({x: 0, y: 0, width: 880, height: 404}));
  const film = resolvePlayerPresentation({...input(880, 840, [fold]), upNext: false})!;
  assert.equal(film.episodes, undefined, 'a film draws no episodes it does not have');
  assert.deepEqual(film.controls, {x: 0, y: 436, width: 880, height: 404});
  const narrow = resolvePlayerPresentation({...input(626, 890, [{orientation: 'horizontal', state: 'half-opened', bounds: {left: 0, right: 626, top: 445, bottom: 445}}]), upNext: true})!;
  assert.deepEqual(narrow.controls, {x: 0, y: 461, width: 626, height: 255}, 'foldable4 stacks the controls over frame 5 rows');
  assert.deepEqual(narrow.episodes, {x: 0, y: 716, width: 626, height: 162}, 'two compact rows and their heading');
  const filmNarrow = resolvePlayerPresentation({...input(626, 890, [{orientation: 'horizontal', state: 'half-opened', bounds: {left: 0, right: 626, top: 445, bottom: 445}}]), upNext: false})!;
  assert.equal(filmNarrow.episodes, undefined);
  assert.deepEqual(filmNarrow.controls, {x: 0, y: 461, width: 626, height: 429}, 'a film keeps the one column');
  const short = resolvePlayerPresentation({...input(626, 860, [{orientation: 'horizontal', state: 'half-opened', bounds: {left: 0, right: 626, top: 428, bottom: 428}}]), upNext: true})!;
  assert.equal(short.episodes, undefined, 'a half that cannot hold the column and both rows drops to one column instead of clipping');
  assert.deepEqual(short.controls, {x: 0, y: 444, width: 626, height: 416});
});

test('full-window panels take the exact right page or tablet-play380pt beside the picture', () => {
  const duo = resolvePlayerPresentation({...input(890, 626, [vertical]), panelOpen: true})!;
  assert.deepEqual(duo.panel, {x: 445, y: 0, width: 445, height: 626});
  assert.equal(duo.surface!.width, 445);
  assert.equal(duo.controlsHidden, true);
  assert.equal(duo.controls, undefined);
  const tablet = resolvePlayerPresentation({...input(1180, 820), panelOpen: true})!;
  assert.deepEqual(tablet.panel, {x: 800, y: 0, width: 380, height: 820});
  assert.deepEqual(tablet.picture, {x: 0, y: 185, width: 800, height: 450});
});

test('phone-player3 floats its side panel over the whole picture instead of re-cropping it', () => {
  for (const [width, height] of [[844, 390], [890, 626], [626, 400]]) {
    const p = resolvePlayerPresentation({...input(width!, height!), panelOpen: true})!;
    assert.equal(p.mode, 'full-window');
    assert.deepEqual(p.surface, {x: 0, y: 0, width: width!, height: height!}, 'the picture keeps playing across the whole window');
    assert.deepEqual(p.picture, letterboxPlayerPicture({x: 0, y: 0, width: width!, height: height!}));
    assert.deepEqual(p.panel, {x: width! - 408, y: 12, width: 356, height: height! - 24}, '52 in from the right edge,12 top and bottom');
    assert.equal(p.controlsHidden, true);
    assert.equal(p.controls, undefined);
  }
});

test('3f keeps the docked state and measured-slot authority through every posture', () => {
  for (const next of [input(466, 678), input(890, 626, [vertical]), input(840, 880), input(890, 626, [{...vertical, state: 'half-opened'}])]) {
    assert.deepEqual(resolvePlayerPresentation({...next, docked: true, panelOpen: true}), {
      mode: 'docked', hideNavigation: false, hideStatusBar: false, controlsAutoHide: false, controlsHidden: false,
    });
  }
});

test('a stale or unsupported posture cannot reserve a hinge from the old window', () => {
  const next = input(890, 626, [{...vertical, state: 'half-opened'}]);
  for (const posture of [{...next.posture, window: {width: 626, height: 890}}, {...next.posture, status: 'pending' as const},
    {...next.posture, status: 'unavailable' as const, window: null}, {...next.posture, folds: [vertical, vertical]}]) {
    assert.equal(resolvePlayerPresentation({...next, posture})!.mode, 'full-window');
  }
  for (const width of [0, -1, Infinity, NaN]) assert.equal(resolvePlayerPresentation({...next, window: {width, height: 626}}), undefined);
});

test('notes/foldable9 reveals for exactly3s on fold/unfold/turn, except under the3d full-window panel', () => {
  const wide = input(890, 626, [vertical]), book = input(890, 626, [{...vertical, state: 'half-opened'}]), tall = input(626, 890);
  assert.equal(playerControlsRevealDeadline(wide, book, 100), 3100);
  assert.equal(playerControlsRevealDeadline(book, wide, 200), 3200);
  assert.equal(playerControlsRevealDeadline(wide, tall, 300), 3300);
  assert.equal(playerControlsRevealDeadline(wide, {...wide, panelOpen: true}, 400, 3300), 0);
  assert.equal(playerControlsRevealDeadline(undefined, wide, 500), 0);
  assert.equal(playerControlsRevealDeadline(wide, {...tall, docked: true}, 600, 3300), 0);
  assert.equal(playerControlsRevealDeadline(tall, {...wide, panelOpen: true}, 700, 3300), 0, 'unfolding with the panel open does not reveal controls');
  assert.equal(playerControlsRevealDeadline({...wide, panelOpen: true}, wide, 800, 0), 0, 'panel closure alone is not a fold reveal');
  const tabletop = input(626, 890, [{orientation: 'horizontal', state: 'half-opened', bounds: {left: 0, top: 445, right: 626, bottom: 445}}]);
  assert.equal(resolvePlayerPresentation({...tabletop, panelOpen: true})!.controlsHidden, false, 'tabletop keeps its bottom controls visible');
});

test('the checked posture publisher feeds Q7 page, panel, reveal and dock geometry directly', () => {
  let publish!: (value: unknown) => void;
  const store = createPlatformPostureStore({getSnapshot: () => new Promise(() => {}), subscribe: listener => {publish = listener; return () => {};}});
  const off = store.subscribe(() => {});
  const read = (window = {width: 890, height: 626}): PlayerPresentationInput => {
    const posture: PlatformPostureSnapshot = store.getSnapshot();
    return {window, posture, docked: false, panelOpen: false};
  };
  const report = (revision: number, state: 'flat' | 'half-opened') => ({revision, status: 'available', window: {width: 890, height: 626},
    folds: [{...vertical, state, isSeparating: state === 'half-opened', occlusion: 'none'}]});
  try {
    assert.equal(resolvePlayerPresentation(read())!.mode, 'full-window');
    publish(report(1, 'flat'));
    const flat = read();
    assert.deepEqual(resolvePlayerPresentation({...flat, panelOpen: true})!.panel, {x: 445, y: 0, width: 445, height: 626});
    assert.equal(resolvePlayerPresentation({...flat, panelOpen: true})!.controlsHidden, true);
    publish(report(2, 'half-opened'));
    const book = read(), split = resolvePlayerPresentation(book)!;
    assert.deepEqual(split.picture, {x: 0, y: 0, width: 445, height: 250.3125});
    assert.equal(split.controls!.width, 429);
    assert.equal(playerControlsRevealDeadline(flat, book, 100), 3100);
    assert.equal(resolvePlayerPresentation(read({width: 626, height: 890}))!.mode, 'watch', 'a new window cannot reuse the previous window fold');
    assert.equal(resolvePlayerPresentation({...book, docked: true})!.surface, undefined, 'posture never replaces the measured dock slot');
    publish({revision: 3, status: 'unavailable', window: null, folds: []});
    assert.equal(resolvePlayerPresentation(read())!.hingeBand, undefined, 'native suspension cannot leave a split reserved');
  } finally {off();}
});

test('a renewed posture observation cannot restore the previous attachment split', () => {
  const callbacks: ((value: unknown) => void)[] = [];
  const store = createPlatformPostureStore({getSnapshot: () => new Promise(() => {}), subscribe: listener => {callbacks.push(listener); return () => {};}});
  const current = () => resolvePlayerPresentation({...input(890, 626), posture: store.getSnapshot()})!;
  const report = (revision: number) => ({revision, status: 'available', window: {width: 890, height: 626},
    folds: [{...vertical, state: 'half-opened', isSeparating: true, occlusion: 'none'}]});
  const first = store.subscribe(() => {});
  callbacks[0]!(report(8));
  assert.equal(current().mode, 'book');
  first();
  const second = store.subscribe(() => {});
  try {
    assert.equal(current().mode, 'full-window', 'the new observation is pending, not the cached split');
    callbacks[0]!(report(100));
    assert.equal(current().mode, 'full-window', 'a retired native attachment cannot reserve a hinge');
    callbacks[1]!(report(1));
    assert.equal(current().mode, 'book');
  } finally {second();}
});
