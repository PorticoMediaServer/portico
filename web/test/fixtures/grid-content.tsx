import {useLayoutEffect} from 'react';
import {flushSync} from 'react-dom';
import {createRoot} from 'react-dom/client';
import {createWindowedCollection, type PageResult} from '@core/collections/windowed';
import {WindowedGrid, type GridItemContext, type WindowedGridHandle} from '../../src/kit/layout/WindowedGrid';
import {mountGridOutgoingHarness} from './grid-outgoing';
import '../../src/kit/tokens.css';

const CARD = 80.23, GAP = .37, ROW = 56.3, ROW_GAP = .27, STICKY = 7.13;
/** Mutable per-mount card geometry; changing it must not require a new renderItem. */
type GridGeometry = Readonly<{cardWidth: number; rowHeight: number; gap: number; rowGap: number; stickyOffset: number}>;
const DEFAULT_GEOMETRY: GridGeometry = {cardWidth: CARD, rowHeight: ROW, gap: GAP, rowGap: ROW_GAP, stickyOffset: STICKY};
type Item = Readonly<{id: string; revision: number}>;
const frame = () => new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
const settle = async () => {await frame();await frame();await frame();};
const frames = (job: Animation) => job.effect instanceof KeyframeEffect ? job.effect.getKeyframes() : [];
async function until(read: () => boolean, message: string) {
  const deadline = performance.now() + 4000;
  while (!read()) {if (performance.now() > deadline) throw new Error(message);await frame();}
}
function check(value: unknown, message: string): asserts value {if (!value) throw new Error(message);}
const equal = (a: unknown, b: unknown, message: string) => check(JSON.stringify(a) === JSON.stringify(b), `${message}: ${JSON.stringify(a)} != ${JSON.stringify(b)}`);

/** Real WindowedCollection + WindowedGrid; no list-sized allocation even at 5M. */
function mount(totalItems: number, initialIndex?: number, zoom = 1) {
  let total = totalItems, revision = 0, callback = 0, placeholderCallback = 0, focusCallback = 0;
  let calls = 0, children = 0, placeholders = 0, mounts = 0, unmounts = 0;
  const visible: number[][] = [], ensured: number[][] = [], focusEvents: number[] = [];
  let blocked = false;
  const pending: (() => void)[] = [];
  const fetchPage = async (offset: number, limit: number): Promise<PageResult<Item>> => {
    const answer = {total, items: Array.from({length: Math.max(0, Math.min(limit, total - offset))}, (_, index) => ({id: `item-${offset + index}`, revision}))};
    if (blocked) await new Promise<void>(resolve => pending.push(resolve));
    return answer;
  };
  const source = createWindowedCollection<Item>({fetchPage, keyOf: item => item.id, pageSize: 32, maxResidentPages: 6, prefetchPages: 0});
  const collection = {...source, ensureRange: (first: number, last: number) => {ensured.push([first, last]);source.ensureRange(first, last);}};
  const host = document.createElement('div');host.style.cssText = 'position:fixed;left:0;top:0;width:646px;background:#071018;z-index:10000';host.style.zoom = String(zoom);document.body.append(host);
  const root = createRoot(host), handle = {current: null as WindowedGridHandle | null};
  function Content({item, context, version}: {item: Item; context: GridItemContext; version: number}) {
    children++;
    useLayoutEffect(() => {mounts++;return () => {unmounts++;};}, []);
    return <button data-content={item.id} data-revision={item.revision} data-callback={version} data-focused={context.focused}>{item.id}</button>;
  }
  const makeRender = () => {const captured = callback;return (item: Item, context: GridItemContext) => {calls++;return <Content item={item} context={context} version={captured}/>;};};
  const makePlaceholder = () => {const captured = placeholderCallback;return (index: number) => {placeholders++;return <span data-placeholder={index} data-callback={captured}/>;};};
  const makeFocus = () => {const captured = focusCallback;return () => {focusEvents.push(captured);};};
  let renderItem = makeRender(), renderPlaceholder = makePlaceholder(), onItemFocus = makeFocus();
  const geom = {...DEFAULT_GEOMETRY}, pitch = () => geom.rowHeight + geom.rowGap;
  const onVisibleRange = (first: number, last: number) => visible.push([first, last]);
  const draw = () => flushSync(() => root.render(<WindowedGrid collection={collection} retainRemovals={zoom !== 1} paintScope={collection} ref={handle} label="Content fixture" height={360} cardWidth={geom.cardWidth} rowHeight={geom.rowHeight}
    gap={geom.gap} rowGap={geom.rowGap} stickyOffset={geom.stickyOffset} initialIndex={initialIndex} onVisibleRange={onVisibleRange} onItemFocus={onItemFocus}
    renderItem={renderItem} renderPlaceholder={renderPlaceholder}/>));
  draw();
  const grid = () => host.querySelector<HTMLElement>('[role="list"]')!;
  const stats = () => ({calls, children, placeholders, mounts, unmounts, visible: visible.length, ensured: ensured.length});
  const range = () => {
    const element = grid(), columns = Math.max(1, Math.floor((element.clientWidth + geom.gap) / (geom.cardWidth + geom.gap)));
    const height = Math.ceil(total / columns) * pitch() - geom.rowGap, usable = element.clientHeight - geom.stickyOffset;
    const logicalTop = element.scrollTop * Math.max(1, height - usable) / Math.max(1, Math.min(8_000_000, height) - usable);
    const first = Math.min(Math.ceil(total / columns) - 1, Math.floor(logicalTop / pitch()));
    const last = Math.min(Math.ceil(total / columns) - 1, Math.max(first, Math.ceil((logicalTop + usable) / pitch()) - 1));
    return {columns, logicalTop, first: first * columns, last: Math.min(total - 1, (last + 1) * columns - 1),
      renderFirst: Math.max(0, first - 1) * columns, renderLast: Math.min(total - 1, (last + 2) * columns - 1)};
  };
  const geometry = () => {
    const element = grid(), expected = range();
    // A separate DOM scroller lets the same browser round/paint the original
    // expression. Compare exact bounding boxes, not an assumed float epsilon.
    const reference = document.createElement('div');reference.className = element.className;
    reference.style.cssText = element.style.cssText;reference.style.width = `${element.offsetWidth}px`;
    reference.style.zoom = String(zoom);reference.style.position = 'fixed';reference.style.left = '700px';reference.style.top = '0';
    const extent = document.createElement('div');extent.className = element.firstElementChild!.className;
    extent.style.cssText = (element.firstElementChild as HTMLElement).style.cssText;reference.append(extent);document.body.append(reference);
    const pairs: [HTMLElement, HTMLElement][] = [];
    for (const node of element.querySelectorAll<HTMLElement>('[data-grid-index]')) {
      const index = Number(node.dataset.gridIndex), row = Math.floor(index / expected.columns), column = index % expected.columns;
      const old = document.createElement('div');old.className = node.className;
      // Deliberately spell the pre-fix formula in its original evaluation order.
      Object.assign(old.style, {width: `${geom.cardWidth}px`, height: `${geom.rowHeight}px`, left: `${column * (geom.cardWidth + geom.gap)}px`,
        top: `${row * pitch() - expected.logicalTop + element.scrollTop + geom.stickyOffset}px`});
      extent.append(old);pairs.push([node, old]);
    }
    reference.scrollTop = element.scrollTop;
    const actualRoot = element.getBoundingClientRect(), oldRoot = reference.getBoundingClientRect();
    for (const [node, old] of pairs) {
      const a = node.getBoundingClientRect(), b = old.getBoundingClientRect();
      equal([a.left - actualRoot.left, a.top - actualRoot.top, a.width, a.height], [b.left - oldRoot.left, b.top - oldRoot.top, b.width, b.height], `original bounding box ${node.dataset.gridIndex}`);
      equal(node.style.top, old.style.top, 'original float/CSS serialization');
    }
    reference.remove();return pairs.length;
  };
  const verifyRange = () => {const expected = range();equal(visible.at(-1), [expected.first, expected.last], 'actual visible range');equal(ensured.at(-1), [expected.renderFirst, expected.renderLast], 'actual ensureRange');};
  return {grid, stats, range, geometry, verifyRange, focusEvents, handle,
    ready: () => until(() => source.getSnapshot().status === 'ready' && !source.getSnapshot().refreshing && !host.querySelector('[data-placeholder]'), 'ready real sparse pages'),
    scroll: async (top: number) => {grid().scrollTop = top;grid().dispatchEvent(new Event('scroll'));await settle();},
    block: () => {blocked = true;}, release: () => {blocked = false;for (const resolve of pending.splice(0)) resolve();},
    publish: async () => {revision++;await source.revalidate({fetchPage});await settle();},
    callbacks: () => {callback++;renderItem = makeRender();draw();},
    placeholderCallback: () => {placeholderCallback++;renderPlaceholder = makePlaceholder();draw();},
    focusCallback: () => {focusCallback++;onItemFocus = makeFocus();draw();},
    total: async (value: number) => {total = value;await source.revalidate({fetchPage});await settle();},
    /** Change only card geometry and redraw with the very same renderItem identity. */
    setGeometry: (next: Partial<GridGeometry>) => {Object.assign(geom, next);draw();},
    geometryValues: () => ({...geom}), renderer: () => renderItem,
    draw, dispose: () => {flushSync(() => root.unmount());source.dispose();host.remove();},
  };
}

export async function checkGridContent() {
  const proof: unknown[] = [];
  for (const {total, zoom} of [1, 1.25].flatMap(zoom => [1000, 5_000_000].map(total => ({total, zoom})))) {
    const fixture = mount(total, undefined, zoom);
    try {
      await fixture.ready();await settle();
      const baseTop = total === 1000 ? 230 : 1_000_005;
      await fixture.scroll(baseTop);await fixture.ready();await settle();fixture.verifyRange();
      const before = fixture.stats(), originalRange = fixture.range();
      const original = new Map([...fixture.grid().querySelectorAll<HTMLElement>('[data-content]')].map(node => [node.dataset.content, node]));
      const firstSlot = fixture.grid().querySelector<HTMLElement>('[data-grid-index]')!, oldTop = firstSlot.style.top;
      let boxes = 0;
      // Values are actually read back from the scroller (fractional at high DPR).
      const tops: number[] = [];
      for (const offset of [.25, 1.25, 2.25, 3.25, 4.25]) {
        await fixture.scroll(baseTop + offset);tops.push(fixture.grid().scrollTop);
        equal(fixture.range(), {...originalRange, logicalTop: fixture.range().logicalTop}, 'unchanged render/visible window');
        boxes += fixture.geometry();fixture.verifyRange();
      }
      check(new Set(tops).size >= 4, 'multiple real scroll positions were exercised');
      const after = fixture.stats();
      equal(after.calls, before.calls, 'zero renderItem calls in unchanged window');
      equal(after.children, before.children, 'zero React child renders in unchanged window');
      equal(after.placeholders, before.placeholders, 'zero placeholder calls in unchanged window');
      equal([after.visible, after.ensured], [before.visible, before.ensured], 'range effect is not spuriously republished per pixel');
      for (const node of fixture.grid().querySelectorAll<HTMLElement>('[data-content]')) check(original.get(node.dataset.content) === node, 'child DOM identity survives');
      if (total > 1000) check(firstSlot.style.top !== oldTop, 'compressed wrappers really move while their content stays stable');
      const boundaryBefore = fixture.stats();await fixture.scroll(baseTop + 80);await fixture.ready();await settle();
      check(fixture.stats().calls > boundaryBefore.calls, 'real render-window boundary renders');check(fixture.stats().visible > boundaryBefore.visible, 'boundary publishes visible range');fixture.verifyRange();fixture.geometry();
      if (zoom !== 1) check(tops.some(top => !Number.isInteger(top)), 'actual fractional CSS scroll readback');
      proof.push({total, zoom, retainRemovals: zoom !== 1, before, after, boxes, tops, compressedWrappersMoved: total > 1000});
    } finally {fixture.dispose();}
  }
  const fixture = mount(1000);
  try {
    await fixture.ready();await settle();
    let before = fixture.stats();fixture.draw();await settle();equal(fixture.stats().calls, before.calls, 'unrelated parent publish is stable');
    fixture.callbacks();await settle();check(fixture.stats().children > before.children, 'renderItem callback change renders');
    equal(fixture.grid().querySelector<HTMLElement>('[data-content]')?.dataset.callback, '1', 'new callback original props');
    before = fixture.stats();fixture.focusCallback();await settle();check(fixture.stats().calls > before.calls, 'onItemFocus callback change preserves invalidation');
    const button = fixture.grid().querySelector<HTMLButtonElement>('[data-content]')!;button.focus({preventScroll: true});await settle();
    check(fixture.stats().children > before.children, 'focus context renders');equal(button.dataset.focused, 'true', 'new focused prop');equal(fixture.focusEvents.at(-1), 1, 'new original focus callback');
    before = fixture.stats();await fixture.publish();check(fixture.stats().calls > before.calls, 'generation publishes fresh content');
    equal(fixture.grid().querySelector<HTMLElement>('[data-content]')?.dataset.revision, '1', 'generation item revision is current');
    before = fixture.stats();await fixture.total(1040);check(fixture.stats().calls > before.calls, 'total update renders');equal(fixture.grid().querySelector('[data-grid-index]')?.getAttribute('aria-setsize'), '1040', 'total metadata updates');
    fixture.block();await fixture.scroll(4000);await until(() => !!fixture.grid().querySelector('[data-placeholder]'), 'unloaded real page placeholders');
    before = fixture.stats();fixture.placeholderCallback();await settle();check(fixture.stats().placeholders > before.placeholders, 'placeholder callback changes render');
    equal(fixture.grid().querySelector<HTMLElement>('[data-placeholder]')?.dataset.callback, '1', 'new placeholder original props');
    before = fixture.stats();fixture.release();await fixture.ready();await settle();check(fixture.stats().children > before.children, 'page version publication renders loaded content');fixture.verifyRange();
  } finally {fixture.dispose();}
  // Real browser, geometry-only invalidation: the same renderItem identity and the
  // same render/visible range, but a changed card width and sticky offset must still
  // rebuild content and reposition every wrapper with the original expression/order.
  const geometryOnly = mount(1000);
  try {
    await geometryOnly.ready();await settle();
    geometryOnly.grid().querySelector<HTMLButtonElement>('[data-content="item-8"]')!.focus({preventScroll: true});await settle();
    const beforeGeometry = geometryOnly.stats(), rangeBefore = geometryOnly.range(), renderer = geometryOnly.renderer();
    const slotCount = geometryOnly.grid().querySelectorAll('[data-grid-index]').length;
    const sample = geometryOnly.grid().querySelector<HTMLElement>('[data-grid-index="9"]')!, oldLeft = sample.style.left, oldTop = sample.style.top;
    check(geometryOnly.grid().querySelector<HTMLElement>('[data-content][data-focused="true"]')?.dataset.content === 'item-8', 'geometry case starts with a real focused slot');
    geometryOnly.setGeometry({cardWidth: 79.5, stickyOffset: STICKY + 1});await settle();
    const afterGeometry = geometryOnly.stats(), rangeAfter = geometryOnly.range();
    check(geometryOnly.renderer() === renderer, 'same renderItem identity across the geometry change');
    check(afterGeometry.calls > beforeGeometry.calls, 'geometry-only change reinvokes renderItem');
    equal([rangeAfter.first, rangeAfter.last, rangeAfter.renderFirst, rangeAfter.renderLast],
      [rangeBefore.first, rangeBefore.last, rangeBefore.renderFirst, rangeBefore.renderLast], 'geometry change keeps the same range');
    equal(geometryOnly.grid().querySelectorAll('[data-grid-index]').length, slotCount, 'geometry change keeps the same slot count');
    equal(geometryOnly.geometry(), slotCount, 'every wrapper matches the original expression at the new geometry');
    const index = Number(sample.dataset.gridIndex), row = Math.floor(index / rangeAfter.columns), column = index % rangeAfter.columns;
    check(sample.style.left !== oldLeft && sample.style.top !== oldTop, 'wrapper actually adopted the new geometry');
    equal(sample.style.left, `${column * (79.5 + GAP)}px`, 'wrapper left keeps original arithmetic order');
    equal(sample.style.top, `${row * (ROW + ROW_GAP) - rangeAfter.logicalTop + geometryOnly.grid().scrollTop + STICKY + 1}px`, 'wrapper top keeps original arithmetic order');
    for (const node of geometryOnly.grid().querySelectorAll<HTMLElement>('[data-grid-index]')) {
      const at = Number(node.dataset.gridIndex);
      equal(node.dataset.itemId, `item-${at}`, 'wrapper keeps its live item id');
      equal(node.querySelector<HTMLElement>('[data-content]')?.dataset.content, `item-${at}`, 'slot content matches its live index');
    }
    equal(geometryOnly.grid().querySelectorAll('[data-content][data-focused="true"]').length, 1, 'exactly one live focus context');
    equal(geometryOnly.grid().querySelector<HTMLElement>('[data-content][data-focused="true"]')?.dataset.content, 'item-8', 'focus stays on the same original item');
    for (const node of geometryOnly.grid().querySelectorAll<HTMLElement>('[data-content]')) {
      equal([node.dataset.callback, node.dataset.revision], ['0', '0'], 'no stale page version or item revision props');
    }
    equal(geometryOnly.grid().querySelectorAll('[data-grid-leaving]').length, 0, 'geometry change creates no outgoing nodes');
    equal(afterGeometry.visible, beforeGeometry.visible, 'geometry change does not republish the authority range');
  } finally {geometryOnly.dispose();}
  proof.push({invalidation: ['parent-stable', 'render-callback', 'focus-callback', 'focused', 'generation', 'total', 'placeholder-callback', 'page-version', 'geometry-only']});
  const initial = mount(1000, 400);
  try {
    await initial.ready();await until(() => initial.grid().scrollTop > 0, 'initial index scroll');await settle();
    check(initial.range().first <= 400 && initial.range().last >= 400, 'initial index in viewport');
    initial.handle.current!.scrollToIndex(410, {focus: true});await until(() => document.activeElement?.closest<HTMLElement>('[data-grid-index]')?.dataset.gridIndex === '410', 'imperative pending focus');
    document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', {key: 'ArrowRight', bubbles: true, cancelable: true}));
    await until(() => document.activeElement?.closest<HTMLElement>('[data-grid-index]')?.dataset.gridIndex === '411', 'keyboard focus');
    initial.handle.current!.scrollToIndex(440, {focus: true});await frame();initial.grid().dispatchEvent(new WheelEvent('wheel'));
    const stopped = initial.grid().scrollTop;await settle();equal(initial.grid().scrollTop, stopped, 'wheel cancels original animated scroll');
    proof.push({focus: ['initial-index', 'imperative', 'keyboard', 'scroll-cancel']});
  } finally {initial.dispose();}
  return {dpr: devicePixelRatio, proof};
}

/** Focused new cases reuse the old real PosterCard fixture, without invoking its full family. */
export async function checkGridContentRetention() {
  const dispose = mountGridOutgoingHarness(), beforeInert: boolean[] = [];
  // Native capture observes the real focus handoff during React's snapshot phase,
  // before commit event suppression can defer a synthetic React focus handler.
  const focusWitness = (event: FocusEvent) => {
    if ((event.target as HTMLElement).closest('[data-item-id="a-1"]')) {
      const original = document.querySelector<HTMLElement>('[data-item-id="a-0"]');if (original) beforeInert.push(original.inert);
    }
  };
  document.addEventListener('focusin', focusWitness, true);
  const slot = (key: string) => document.querySelector<HTMLElement>(`[data-item-id="${key}"]`)!;
  const ready = () => until(() => document.querySelector('[data-grid-ready]')?.getAttribute('data-grid-ready') === 'true' && !!slot('a-0'), 'retention fixture ready');
  const replace = async (ids: string[]) => {
    const generation = window.gridOutgoingHarness!.generation();window.gridOutgoingHarness!.replace(ids);
    await until(() => window.gridOutgoingHarness!.generation() > generation && document.querySelector('[data-grid-ready]')?.getAttribute('data-grid-ready') === 'true', 'accepted replacement');
  };
  const ids = (prefix: string) => Array.from({length: 100}, (_, i) => `${prefix}-${i}`);
  try {
    await ready();await settle();
    slot('a-0').querySelector<HTMLButtonElement>('button')!.focus();await settle();document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', {key: 'Shift', bubbles: true}));
    await replace(Array.from({length: 99}, (_, i) => `a-${i + 1}`));
    await until(() => !!document.querySelector('[data-grid-leaving]'), 'first removal cohort appears');
    check(slot('a-1').contains(document.activeElement), 'focus transferred to next original item');equal(beforeInert.at(-1), false, 'native focus witness before inert');
    await until(() => !document.querySelector('[data-grid-leaving]'), 'first cohort retires');
    await replace(Array.from({length: 96}, (_, i) => `a-${i + 4}`));
    await until(() => !!document.querySelector('[data-grid-leaving]'), 'move cohort appears');
    await settle();
    window.gridOutgoingHarness!.publish();await settle();
    equal(slot('a-4').querySelector<HTMLElement>('[data-original-card]')?.dataset.publication, '1', 'live props update during retention');
    await until(() => !document.querySelector('[data-grid-leaving]'), 'move cohort retires');
    const original = slot('a-4'), originalChild = original.querySelector('[data-original-card]');
    await replace(ids('b'));await settle();
    check(document.querySelectorAll('[data-grid-leaving]').length > 0 && document.querySelectorAll('[data-grid-leaving]').length <= 40, 'visible outgoing40 bound');
    check(document.querySelectorAll('[data-item-id^="b-"]').length > 40, 'live slots are not capped');
    check(slot('a-4') === original && original.querySelector('[data-original-card]') === originalChild, 'exact retained nodes');
    check(original.inert && original.getAttribute('aria-hidden') === 'true', 'retained controls retired');
    equal(window.gridOutgoingHarness!.mounted('a-4'), 1, 'no original remount');equal(window.gridOutgoingHarness!.retired('a-4'), 0, 'original not unmounted during exit');
    window.gridOutgoingHarness!.publish();await settle();equal(original.querySelector<HTMLElement>('[data-original-card]')?.dataset.publication, '1', 'old content never borrows latest callbacks/props');
    original.querySelector<HTMLButtonElement>('button')!.click();equal(window.gridOutgoingHarness!.navigations(), 0, 'old callbacks cannot navigate');
    const incoming = slot('b-0'), arrival = incoming.getAnimations().find(job => frames(job).some(key => key.opacity !== undefined));
    check(arrival, 'real incoming arrival');equal(arrival.effect!.getTiming().duration, 180, '180ms arrival preserved');arrival.pause();arrival.currentTime = 60;
    const oldFinish = arrival.onfinish, opacity = Number(getComputedStyle(incoming).opacity), inline = incoming.style.opacity;
    await until(() => !document.querySelector('[data-grid-leaving]'), 'outgoing cohort retires');
    await replace(ids('c'));await settle();
    const exit = incoming.getAnimations().find(job => String(frames(job).at(-1)?.opacity) === '0');check(exit, 'real outgoing opacity');exit.pause();exit.currentTime = 40;
    check(Math.abs(Number(frames(exit)[0].opacity) - opacity) < .02, 'incoming opacity lent to original exit');
    oldFinish?.call(arrival, new AnimationPlaybackEvent('finish'));equal(incoming.style.opacity, inline, 'retired completion cannot write');
    const exitOpacity = Number(getComputedStyle(incoming).opacity);await replace(ids('b'));await settle();
    check(slot('b-0') === incoming, 'return keeps original DOM');
    const returning = incoming.getAnimations().find(job => String(frames(job).at(-1)?.opacity) === '1');check(returning, 'return arrival');
    check(Math.abs(Number(frames(returning)[0].opacity) - exitOpacity) < .02, 'return borrows original exit opacity');
    // Raw scroll still replaces paintWindow and cancels outgoing cohorts immediately.
    const grid = document.querySelector<HTMLElement>('[aria-label="Sparse fixture"]')!;grid.scrollTop = 10;grid.dispatchEvent(new Event('scroll'));await settle();
    equal(document.querySelectorAll('[data-grid-leaving]').length, 0, 'scroll cancels retained paint');
    await replace(ids('d'));await settle();window.gridOutgoingHarness!.replaceSource();await ready();await settle();
    equal(document.querySelectorAll('[data-grid-leaving]').length, 0, 'source replacement retires original scope');
    return ['before-inert-focus', 'live-props', '40-outgoing-cap', 'original-nodes-props', 'retired-actions', '180ms-arrival', 'exit-return-opacity', 'stale-completion', 'scroll-cancel', 'source-retirement'];
  } finally {document.removeEventListener('focusin', focusWitness, true);dispose();}
}

/** Strict original 320ms check, separate so a pre-existing failure cannot hide
 * the content/geometry and original retained-paint results. */
export async function checkGridContentMove() {
  const dispose = mountGridOutgoingHarness();
  try {
    await until(() => document.querySelector('[data-grid-ready]')?.getAttribute('data-grid-ready') === 'true', 'move fixture ready');await settle();
    const generation = window.gridOutgoingHarness!.generation();
    window.gridOutgoingHarness!.replace(Array.from({length: 96}, (_, i) => `a-${i + 4}`));
    await until(() => window.gridOutgoingHarness!.generation() > generation && document.querySelector('[data-grid-ready]')?.getAttribute('data-grid-ready') === 'true', 'move fixture accepted');await settle();
    const node = document.querySelector<HTMLElement>('[data-item-id="a-4"]')!;
    const move = node.getAnimations().find(job => job.effect?.getTiming().duration === 320);
    check(move, 'real 320ms FLIP exists');move.pause();window.gridOutgoingHarness!.publish();await settle();
    check(node.getAnimations().includes(move), 'original 320ms move survives new props');
    equal(node.querySelector<HTMLElement>('[data-original-card]')?.dataset.publication, '1', 'new live props during original move');
    return 'original-320ms-move-preserved';
  } finally {dispose();}
}
