import {useEffect, useLayoutEffect, useRef, useState, type CSSProperties} from 'react';
import {flushSync} from 'react-dom';
import {createRoot} from 'react-dom/client';
import '../../src/kit/tokens.css';
import styles from '../../src/kit/cards/cards.module.css';
import {CardCaption, CardFrame} from '../../src/kit/cards/CardFrame';
import {RetainedLayoutPaint} from '../../src/kit/layout/retained-paint';
import type {CardSelection} from '../../src/kit/cards/types';

/** One card's live ResizeObserver, as the page actually constructed it. */
type ObserverRecord = {id: number; calls: number; reads: number; targets: Element[]; disconnects: number; callback: ResizeObserverCallback};
type Box = Readonly<{left: string; top: string; width: string; height: string}>;
type CardView = Readonly<{
  kind: string; hasAux: boolean; liveObservers: number;
  art: Box; aux: Box | null; shadow: Box;
  auxExact: boolean | null; shadowExact: boolean;
  auxAligned: boolean | null; shadowAligned: boolean;
  observed: boolean; oneReadPerCallback: boolean; deviceWidth: number; deviceTop: number; primaryHeight: number;
}>;
type Calls = {navigate: number; more: (Element | null)[]; toggle: unknown[]; exit: number};

const layout = (element: HTMLElement): Box => {
  const bounds = getComputedStyle(element);
  return {left: `${element.offsetLeft}px`, top: `${element.offsetTop}px`, width: bounds.width, height: bounds.height};
};
const inline = (element: HTMLElement): Box => ({left: element.style.left, top: element.style.top, width: element.style.width, height: element.style.height});
const same = (left: Box, right: Box) => left.left === right.left && left.top === right.top && left.width === right.width && left.height === right.height;
const painted = (left: HTMLElement, right: HTMLElement) => {
  const a = left.getBoundingClientRect(), b = right.getBoundingClientRect();
  return Math.abs(a.left - b.left) < 0.75 && Math.abs(a.top - b.top) < 0.75 && Math.abs(a.width - b.width) < 0.75 && Math.abs(a.height - b.height) < 0.75;
};
/** RO delivery happens in the rendering lifecycle; never assume one frame arrives. */
const frame = () => new Promise<void>(resolve => {
  let settled = false;
  const timer = setTimeout(() => {if (!settled) {settled = true; resolve();}}, 250);
  requestAnimationFrame(() => {if (!settled) {settled = true; clearTimeout(timer); resolve();}});
});
const settle = async () => {for (let index = 0; index < 3; index++) await frame();};

/** The real card as the browser paints it: product CSS, product CardFrame, one live observer pair under test. */
export function mountCardGeometry() {
  const records: ObserverRecord[] = [];
  const calls: Calls = {navigate: 0, more: [], toggle: [], exit: 0};
  const nativeStyle = window.getComputedStyle.bind(window), nativeObserver = window.ResizeObserver;
  let reads = 0;
  window.getComputedStyle = ((...args: Parameters<typeof nativeStyle>) => {reads++; return nativeStyle(...args);}) as typeof window.getComputedStyle;
  class TrackedResizeObserver extends nativeObserver {
    readonly record: ObserverRecord;
    constructor(callback: ResizeObserverCallback) {
      const record: ObserverRecord = {id: records.length, calls: 0, reads: 0, targets: [], disconnects: 0, callback};
      super((entries, observer) => {const before = reads; record.calls++; callback(entries, observer); record.reads += reads - before;});
      this.record = record;
      records.push(record);
    }
    observe(target: Element, options?: ResizeObserverOptions) {this.record.targets.push(target); super.observe(target, options);}
    disconnect() {this.record.disconnects++; super.disconnect();}
  }
  window.ResizeObserver = TrackedResizeObserver as unknown as typeof ResizeObserver;
  const host = document.createElement('div');
  host.setAttribute('data-geometry-host', '');
  host.style.cssText = 'display:flex;align-items:flex-start;gap:24px;padding:16px;width:900px';
  document.body.append(host);
  const root = createRoot(host);
  const dispose = () => {
    flushSync(() => root.unmount());
    document.querySelectorAll('[data-geometry-host]').forEach(node => node.remove());
    window.getComputedStyle = nativeStyle;
    window.ResizeObserver = nativeObserver;
    window.cardGeometryClosed = records.map(record => ({id: record.id, disconnects: record.disconnects, observed: record.targets.length}));
    delete window.cardGeometry; delete window.disposeCardGeometry;
  };
  try {flushSync(() => root.render(<Fixture records={records} calls={calls} />));}
  catch (error) {dispose(); throw error;}
  return dispose;
}

function Fixture({records, calls}: {records: ObserverRecord[]; calls: Calls}) {
  const [tick, setTick] = useState(0), [aux, setAux] = useState(true), [caption, setCaption] = useState(13);
  const [probe, setProbe] = useState(16), [retained, setRetained] = useState(false), [generation, setGeneration] = useState(0), [widths, setWidths] = useState<[number, number]>([190, 190]);
  const firstRef = useRef<HTMLSpanElement>(null), secondRef = useRef<HTMLSpanElement>(null), host = useRef<HTMLDivElement>(null);
  const selection: CardSelection = {active: false, checked: false, onToggle: intent => calls.toggle.push(intent), onExit: () => {calls.exit++;}};
  const model = {title: 'Geometry card', caption: []};
  useLayoutEffect(() => {
    const article = (index: number) => host.current?.querySelectorAll<HTMLElement>('article[data-card-kind]')[index] ?? null;
    const primary = (index: number) => article(index)?.querySelector<HTMLElement>(':scope > button') ?? null;
    const shadow = (index: number) => primary(index)?.querySelector<HTMLElement>(':scope > span[aria-hidden="true"]') ?? null;
    const frame = (index: number) => (article(index)?.querySelector<HTMLElement>(':scope > span')?.firstElementChild as HTMLElement | null) ?? null;
    const live = (index: number) => {
      const button = primary(index);
      return records.filter(record => record.disconnects === 0 && !!button && record.targets.some(target => target === button || button.contains(target)));
    };
    const cards = () => [0, 1].flatMap(index => {
      const card = article(index), button = primary(index), art = card?.querySelector<HTMLElement>('[data-geometry-art]'), layer = shadow(index), auxLayer = frame(index);
      if (!card || !button || !art || !layer) return [];
      const liveObservers = live(index), expected = 1 + button.children.length, artBox = layout(art), shadowBox = inline(layer), auxBox = auxLayer ? inline(auxLayer) : null;
      return [{
        kind: card.dataset.cardKind ?? '', hasAux: !!auxLayer, liveObservers: liveObservers.length,
        art: artBox, aux: auxBox, shadow: shadowBox,
        auxExact: auxLayer ? same(auxBox!, artBox) : null, shadowExact: same(shadowBox, artBox),
        auxAligned: auxLayer ? painted(auxLayer, art) : null, shadowAligned: painted(layer, art),
        observed: liveObservers.every(record => record.targets.length === expected && record.targets[0] === button && [...button.children].every(child => record.targets.includes(child))),
        oneReadPerCallback: liveObservers.every(record => record.reads === record.calls),
        deviceWidth: Math.round(art.getBoundingClientRect().width), deviceTop: Math.round(art.getBoundingClientRect().top),
        primaryHeight: Math.round(button.getBoundingClientRect().height),
      }];
    });
    window.cardGeometry = {
      ready: () => cards().length === 2 && cards().every(card => card.shadowExact && card.shadowAligned && (card.hasAux ? card.auxExact === true && card.auxAligned === true : card.aux === null)),
      cards: () => cards(),
      records: () => records.map(record => ({id: record.id, calls: record.calls, reads: record.reads, targets: record.targets.length, disconnects: record.disconnects})),
      recordIds: index => live(index).map(record => record.id),
      invokeRecord: id => {const record = records.find(item => item.id === id); if (record) record.callback([], {} as ResizeObserver);},
      settle,
      rerender: () => flushSync(() => setTick(value => value + 1)),
      width: next => flushSync(() => setWidths([next, next])),
      aux: value => flushSync(() => setAux(value)),
      caption: value => flushSync(() => setCaption(value)),
      probe: value => flushSync(() => setProbe(value)),
      retained: value => flushSync(() => setRetained(value)),
      scale: value => {for (const card of host.current?.querySelectorAll<HTMLElement>('article[data-card-kind]') ?? []) card.style.setProperty('--cards-art-scale', value);},
      replace: next => flushSync(() => {setGeneration(value => value + 1); setWidths([190, next]);}),
      focus: () => {primary(0)?.focus();},
      activeIsPrimary: () => primary(0) === document.activeElement,
      clickPrimary: () => {primary(0)?.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true}));},
      clickMore: () => {
        const button = primary(0), more = article(0)?.querySelector<HTMLElement>('button[aria-haspopup="menu"]') ?? null;
        more?.click();
        return {inside: !!button?.contains(more), expanded: more?.getAttribute('aria-expanded') ?? null};
      },
      clickSelection: () => {
        const button = primary(0), target = [...(article(0)?.querySelectorAll<HTMLElement>(':scope > span button') ?? [])].at(-1) ?? null;
        target?.click();
        return {inside: !!button?.contains(target), selected: target?.getAttribute('aria-pressed') ?? null};
      },
      calls: () => ({navigate: calls.navigate, more: calls.more.length, toggle: calls.toggle.length, lastToggle: calls.toggle.at(-1), lastMore: (calls.more.at(-1) ?? null) instanceof Element, exit: calls.exit}),
      secondArt: () => secondRef.current,
      secondConnected: () => secondRef.current?.isConnected === true,
      secondStyles: () => {
        const art = secondRef.current!, button = art.parentElement!, layer = button.querySelector<HTMLElement>(':scope > span[aria-hidden="true"]')!;
        return {aux: null, shadow: inline(layer), art: layout(art)};
      },
    };
  });
  useEffect(() => () => {delete window.cardGeometry;}, []);
  const card = (key: string, ref: typeof firstRef, withAux: boolean, index: number) => (
    <CardFrame key={key} model={model} onNavigate={() => {calls.navigate++;}} kind="poster" artworkRef={ref}
      onMore={withAux ? anchor => {calls.more.push(anchor);} : undefined} selection={withAux ? selection : undefined}
      style={{width: `${widths[index]}px`} as CSSProperties}>
      <span data-geometry-probe style={{display: 'block', fontSize: `${probe}px`, lineHeight: 1.2}}>Font probe</span>
      <span ref={ref} data-geometry-art className={styles.art} />
      <CardCaption model={model} />
    </CardFrame>
  );
  return <div ref={host} style={{'--type-card-title-size': `${caption}px`, display: 'flex', gap: 24} as CSSProperties}>
    <RetainedLayoutPaint.Provider value={retained}>
      {card('first', firstRef, aux, 0)}
      {card(`second-${generation}`, secondRef, false, 1)}
    </RetainedLayoutPaint.Provider>
    <span data-tick={tick} style={{display: 'none'}} />
  </div>;
}

declare global {
  interface Window {
    cardGeometry?: {
      ready(): boolean; cards(): CardView[]; records(): {id: number; calls: number; reads: number; targets: number; disconnects: number}[];
      recordIds(index: number): number[]; invokeRecord(id: number): void; settle(): Promise<void>;
      rerender(): void; width(next: number): void; aux(value: boolean): void; caption(value: number): void; probe(value: number): void;
      retained(value: boolean): void; scale(value: string): void; replace(next: number): void; focus(): void; activeIsPrimary(): boolean;
      clickPrimary(): void; clickMore(): {inside: boolean; expanded: string | null}; clickSelection(): {inside: boolean; selected: string | null};
      calls(): {navigate: number; more: number; toggle: number; lastToggle: unknown; lastMore: boolean; exit: number};
      secondArt(): HTMLElement | null; secondConnected(): boolean;
      secondStyles(): {aux: null; shadow: Box; art: Box};
    };
    disposeCardGeometry?: () => void;
    cardGeometryClosed?: {id: number; disconnects: number; observed: number}[];
  }
}
