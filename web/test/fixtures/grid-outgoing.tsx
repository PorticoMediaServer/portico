import {useCallback, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {createRoot} from 'react-dom/client';
import {createWindowedCollection, type WindowedCollection} from '@core/collections/windowed';
import {WindowedGrid} from '../../src/kit/layout/WindowedGrid';
import {PosterCard} from '../../src/kit/cards';
import type {CardSelection} from '../../src/kit/cards/types';
import {createCardInteractionOwner} from '../../src/kit/cards/interaction-admission';

type Item = Readonly<{id: string}>;
type Harness = Readonly<{
  replace: (ids: readonly string[]) => void; replaceSource: () => void; publish: () => void;
  generation: () => number; mounted: (id: string) => number; retired: (id: string) => number;
  navigations: () => number; toggles: () => number; previews: () => number; focusBeforeInert: () => readonly boolean[];
  selecting: (enabled: boolean) => void; clearBeforeInert: () => readonly boolean[];
}>;
declare global {interface Window {gridOutgoingHarness?: Harness; disposeGridOutgoing?: () => void;}}

/** Real React + real WindowedCollection, static public items. Not a server/Artwork/Session fixture. */
export function mountGridOutgoingHarness() {
  const host = document.createElement('div');host.style.cssText = 'position:fixed;inset:0;z-index:10000;background:#071018;';document.body.append(host);
  const root = createRoot(host);root.render(<HarnessView/>);
  return () => {root.unmount();host.remove();window.gridOutgoingHarness = undefined;};
}
function HarnessView() {
  const [source, setSource] = useState(0), [publication, setPublication] = useState(0);
  const [selecting, setSelecting] = useState(false), clearedBeforeInert = useRef<boolean[]>([]);
  const mounted = useRef(new Map<string, number>()), retired = useRef(new Map<string, number>()), beforeInert = useRef<boolean[]>([]);
  const navigations = useRef(0), toggles = useRef(0), previews = useRef(0), activeCollection = useRef<WindowedCollection<Item> | null>(null);
  const collection = useMemo(() => createWindowedCollection<Item>({pageSize: 200, maxResidentPages: 2, prefetchPages: 0,
    keyOf: item => item.id, fetchPage: async (offset, limit) => ({items: Array.from({length: 100}, (_, i) => ({id: `a-${i}`})).slice(offset, offset + limit), total: 100})}), [source]);
  const answer = useSyncExternalStore(collection.subscribe, collection.getSnapshot, collection.getSnapshot);
  const interactions = useMemo(() => createCardInteractionOwner(), [collection]);
  useLayoutEffect(() => {activeCollection.current = collection;return () => {interactions.dispose();if (activeCollection.current === collection) activeCollection.current = null;collection.dispose();};}, [collection, interactions]);
  useLayoutEffect(() => {window.gridOutgoingHarness = {
    replace: ids => {const rows = ids.map(id => ({id}));collection.invalidate(async (offset, limit) => ({items: rows.slice(offset, offset + limit), total: rows.length}));},
    replaceSource: () => setSource(value => value + 1), publish: () => setPublication(value => value + 1), generation: () => collection.getSnapshot().generation,
    mounted: id => mounted.current.get(id) ?? 0, retired: id => retired.current.get(id) ?? 0,
    navigations: () => navigations.current, toggles: () => toggles.current, previews: () => previews.current, focusBeforeInert: () => beforeInert.current,
    selecting: setSelecting, clearBeforeInert: () => clearedBeforeInert.current,
  };});
  const render = useCallback((item: Item) => <Card item={item} publication={publication} mount={() => mounted.current.set(item.id, (mounted.current.get(item.id) ?? 0) + 1)}
    retire={() => retired.current.set(item.id, (retired.current.get(item.id) ?? 0) + 1)} navigate={() => {navigations.current++;}}
    toggle={() => {toggles.current++;}} preview={enabled => {if (enabled) previews.current++;}} clear={inert => clearedBeforeInert.current.push(inert)}
    interactions={interactions} current={() => activeCollection.current === collection && collection.residentItems().includes(item)} selecting={selecting}/>, [publication, interactions, collection, selecting]);
  return <div style={{width: 640}} onFocusCapture={event => {
    if ((event.target as HTMLElement).closest('[data-item-id="a-1"]')) {
      const original = event.currentTarget.querySelector<HTMLElement>('[data-item-id="a-0"]');if (original) beforeInert.current.push(original.inert);
    }
  }}><button type="button" data-grid-before>Before grid</button><output data-grid-source={source} data-grid-generation={answer.generation} data-grid-ready={answer.status === 'ready' && !answer.refreshing}/>
    <WindowedGrid collection={collection} retainRemovals paintScope={collection} label="Sparse fixture" height={360} cardWidth={80} rowHeight={56} gap={0} rowGap={0}
      empty={<p data-grid-empty>Empty answer</p>} renderPlaceholder={() => <span data-grid-placeholder/>} renderItem={render}/>
  </div>;
}
function Card({item, publication, mount, retire, navigate, toggle, preview, clear, interactions, current, selecting}: Readonly<{
  item: Item; publication: number; mount: () => void; retire: () => void; navigate: () => void; toggle: () => void;
  preview: (enabled: boolean) => void; clear: (inert: boolean) => void; interactions: ReturnType<typeof createCardInteractionOwner>; current: () => boolean; selecting: boolean;
}>) {
  // The original component's DOM/ref/effect lifetime, not a cloned or re-mounted paint tree.
  useLayoutEffect(() => {mount();return retire;}, []);
  const wrapper = useRef<HTMLDivElement>(null);
  const interaction = interactions.source(item, {current, preview: enabled => {
    preview(enabled);if (!enabled) clear(wrapper.current?.closest<HTMLElement>('[data-item-id]')?.inert ?? true);
  }});
  const selection: CardSelection = {interaction, active: selecting, checked: false, onToggle: toggle, onExit: () => {}, onPreview: preview};
  return <div ref={wrapper} data-original-card={item.id} data-publication={publication}><PosterCard model={{title: item.id, caption: []}} onNavigate={navigate} selection={selection} fallback={<span/>}/></div>;
}
