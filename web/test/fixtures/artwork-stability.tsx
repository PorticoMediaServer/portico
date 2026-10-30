import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {flushSync} from 'react-dom';
import {createRoot} from 'react-dom/client';
import '../../src/kit/tokens.css';
import {Artwork} from '../../src/kit/artwork/Artwork';
import {Shelf} from '../../src/kit/layout/Row';
import {createArtworkRuntime} from '../../src/kit/artwork/source';
import type {ArtworkSource} from '../../src/kit/artwork/types';

/** One real 1×1 PNG: the mounted node proves it owns an actual decode, not a fabricated readiness. */
const PIXEL = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

type Counters = {decodes: number; transitions: number; paintRemovals: number; acquisitions: number; releases: number; statuses: {status: string; key?: string}[]};
type Snapshot = Counters & {tick: number; status?: string; images: number; containers: number; admitted: boolean; transitionDuration: string};
type Harness = {
  tick(): number; rerender(): void; key(value: string): void; visible(value: boolean): void; order(value: number | undefined): void;
  authority(): void; customRetain(value: boolean): void;
  capture(): void; sameImage(): boolean; capturedConnected(): boolean;
  snapshot(): Snapshot; statusesSince(index: number): {status: string; key?: string}[];
  shelf(): {childRenders: number; rangeCalls: number; lastRange: [number, number] | undefined; leftDisabled: boolean; rightDisabled: boolean; left: number; maxLeft: number};
  scroll(value: number | 'end'): void; mark(): number;
};
declare global {interface Window {artworkStability?: Harness; disposeArtworkStability?: () => void;}}

export function mountArtworkStability() {
  const counters: Counters = {decodes: 0, transitions: 0, paintRemovals: 0, acquisitions: 0, releases: 0, statuses: []};
  // A real HTMLImageElement.decode() call is the unit under test; never replace
  // the result, only count the original native work.
  const nativeDecode = HTMLImageElement.prototype.decode;
  HTMLImageElement.prototype.decode = function (this: HTMLImageElement) {counters.decodes++; return nativeDecode.call(this);};
  const onTransition = (event: Event) => {if ((event.target as Element | null)?.tagName === 'IMG') counters.transitions++;};
  document.addEventListener('transitionrun', onTransition, true);
  const observer = new MutationObserver(records => {
    for (const record of records) if (record.attributeName === 'data-artwork-admitted' && record.oldValue !== null
      && !(record.target as Element).hasAttribute('data-artwork-admitted')) counters.paintRemovals++;
  });
  observer.observe(document.body, {subtree: true, attributes: true, attributeFilter: ['data-artwork-admitted'], attributeOldValue: true});
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  try {flushSync(() => root.render(<Fixture counters={counters} />));}
  catch (error) {observer.disconnect(); document.removeEventListener('transitionrun', onTransition, true); HTMLImageElement.prototype.decode = nativeDecode; host.remove(); throw error;}
  return () => {
    flushSync(() => root.unmount());
    observer.disconnect(); document.removeEventListener('transitionrun', onTransition, true);
    HTMLImageElement.prototype.decode = nativeDecode;
    host.remove(); delete window.artworkStability; delete window.disposeArtworkStability;
  };
}

function Fixture({counters}: {counters: Counters}) {
  const [tick, setTick] = useState(0);
  const [key, setKey] = useState('alpha');
  const [visible, setVisible] = useState(true);
  const [order, setOrder] = useState<number | undefined>(undefined);
  const [custom, setCustom] = useState(false);
  const [authority, setAuthority] = useState(0);
  const runtimes = useRef<ReturnType<typeof createArtworkRuntime>[]>([]);
  // A changed authority is a real new runtime; the old one only retires with this mount.
  const runtime = useMemo(() => {
    const next = createArtworkRuntime({
      scopeKey: `dev-only-stability-authority-${authority}`,
      load: async () => ({src: PIXEL, width: 1, height: 1}),
      classify: () => ({retryable: false}),
    });
    runtimes.current.push(next);
    return next;
  }, [authority]);
  useLayoutEffect(() => () => {for (const item of runtimes.current) item.dispose();}, []);
  const base = runtime.source({url: `/dev-only-art/${key}`, cacheKey: key}, order === undefined ? undefined : {order});
  const retain = () => {counters.acquisitions++; const release = base.retain!(); return () => {counters.releases++; release();};};
  // Spreading the frozen runtime wrapper keeps its factory retention identity.
  // The custom negative carries only a fresh retain closure, exactly like an
  // external producer that owns no factory token.
  const source: ArtworkSource = custom
    ? {scheduler: base.scheduler, key: base.key, retain}
    : Object.freeze({...base, retain});
  const stage = useRef<HTMLDivElement>(null);
  const shelf = useRef<{childRenders: number; rangeCalls: number; lastRange?: [number, number]}>({childRenders: 0, rangeCalls: 0});
  const captured = useRef<HTMLImageElement | null>(null);
  const images = () => [...(stage.current?.querySelectorAll('img') ?? [])] as HTMLImageElement[];
  useLayoutEffect(() => {
    window.artworkStability = {
      tick: () => tick,
      rerender: () => flushSync(() => setTick(value => value + 1)),
      key: value => flushSync(() => setKey(value)),
      visible: value => flushSync(() => setVisible(value)),
      order: value => flushSync(() => setOrder(value)),
      authority: () => flushSync(() => setAuthority(value => value + 1)),
      customRetain: value => flushSync(() => setCustom(value)),
      capture: () => {captured.current = images()[0] ?? null;},
      sameImage: () => captured.current !== null && captured.current === images()[0],
      capturedConnected: () => captured.current?.isConnected === true,
      mark: () => counters.statuses.length,
      statusesSince: index => counters.statuses.slice(index),
      snapshot: () => {
        const image = images()[0];
        return {...counters, statuses: [...counters.statuses], tick, status: stage.current?.querySelector<HTMLElement>('[data-artwork-status]')?.dataset.artworkStatus,
          images: images().length, containers: stage.current?.querySelectorAll('[data-artwork-status]').length ?? 0, admitted: !!image?.hasAttribute('data-artwork-admitted'),
          transitionDuration: image ? getComputedStyle(image).transitionDuration : ''};
      },
      shelf: () => {
        const track = stage.current?.querySelector<HTMLElement>('[data-shelf-index]')?.parentElement;
        const left = stage.current?.querySelector<HTMLButtonElement>('button[aria-label="Previous items"]'), right = stage.current?.querySelector<HTMLButtonElement>('button[aria-label="Next items"]');
        return {...shelf.current, lastRange: shelf.current.lastRange, leftDisabled: left?.disabled === true, rightDisabled: right?.disabled === true,
          left: track?.scrollLeft ?? 0, maxLeft: track ? Math.max(0, track.scrollWidth - track.clientWidth) : 0};
      },
      scroll: value => {
        const track = stage.current?.querySelector<HTMLElement>('[data-shelf-index]')?.parentElement;
        if (!track) return;
        track.style.scrollSnapType = 'none';
        track.scrollLeft = value === 'end' ? track.scrollWidth : value;
        track.dispatchEvent(new Event('scroll'));
      },
    };
  }, [tick]);
  return <div>
    <div ref={stage} data-tick={tick} style={{width: 200, height: 200}}>
      <div data-artwork-stage style={{width: 200, height: 200}}>
        <Artwork source={source} alt="" visible={visible} width="100%" height="100%" onStatusChange={status => counters.statuses.push({status, key: source.key})} />
      </div>
      <div data-shelf-stage style={{width: 420}}>
        <Shelf items={SHELF_ITEMS} getKey={item => item} itemWidth={200} heading="Shelf"
          onVisibleRange={(first, last) => {shelf.current.rangeCalls++; shelf.current.lastRange = [first, last];}}
          renderItem={item => {shelf.current.childRenders++; return <div style={{height: 40}}>{item}</div>;}} />
      </div>
    </div>
  </div>;
}
const SHELF_ITEMS = Array.from({length: 40}, (_, index) => `item-${String(index).padStart(2, '0')}`);
