import {useLayoutEffect, useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {useRemovalFocus} from '../../src/kit/layout/focus-context';

export type RemovalFocusHarness = Readonly<{
  remove: (id: string) => void;
  removeNow: (id: string) => void;
  removeBoth: (first: string, second: string) => void;
  retainBoth: (first: string, second: string) => void;
  add: (id: string) => void;
  detachRoot: () => void;
  replaceRoot: () => void;
  replaceSection: () => void;
  detachSection: () => void;
  hideFirst: (id: string) => void;
  committed: () => number;
}>;

type Entry = Readonly<{id: string; retained: boolean}>;

declare global {
  interface Window { removalFocusHarness?: RemovalFocusHarness; disposeRemovalFocus?: () => void; }
}

/**
 * A real-DOM harness for the shared removal-focus helper. Entries keep their key when they become
 * retained, so an outgoing row is the *same* connected node drawn inert — the presence case — and
 * `removeNow` drops it in the same commit. The published order is the caller's own committed DOM
 * order, so a node added or replaced under the same ref is only published once its ref has run.
 */
export function mountRemovalFocusHarness() {
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  root.render(<Harness/>);
  return () => {root.unmount(); host.remove(); window.removalFocusHarness = undefined;};
}

function Harness() {
  const [entries, setEntries] = useState<Entry[]>([{id: 'alpha', retained: false}, {id: 'bravo', retained: false}, {id: 'charlie', retained: false}, {id: 'delta', retained: false}]);
  const [hidden, setHidden] = useState<string[]>([]);
  const [rootKey, setRootKey] = useState(0), [sectionKey, setSectionKey] = useState(0);
  const [, bump] = useState(0);
  const list = useRef<HTMLDivElement>(null), nodes = useRef(new Map<string, HTMLElement>()), start = useRef<HTMLButtonElement>(null), commits = useRef(0);
  const [order, setOrder] = useState<HTMLElement[]>([]);
  const live = entries.filter(entry => !entry.retained).map(entry => entry.id);
  const ready = live.length === order.length && live.every((id, index) => nodes.current.get(id) === order[index]);
  const handlers = useRemovalFocus(list, order, start.current ? [start.current] : []);
  // Publish the caller's actual committed DOM order: a node that only exists after the refs run is
  // included on the next pass, and the order is never assumed from the rendered entry list.
  useLayoutEffect(() => {
    const next = live.map(id => nodes.current.get(id)).filter((node): node is HTMLElement => node !== undefined);
    if (next.length === order.length && next.every((node, index) => node === order[index])) return;
    setOrder(next);
  }, [entries, order, rootKey]);
  useLayoutEffect(() => { document.body.dataset.removalReady = ready ? 'true' : 'false'; document.body.dataset.removalCommits = String(++commits.current); });
  useLayoutEffect(() => { if (!ready) bump(value => value + 1); });
  useLayoutEffect(() => {
    window.removalFocusHarness = {
      remove: id => setEntries(current => current.map(entry => entry.id === id ? {...entry, retained: true} : entry)),
      removeNow: id => setEntries(current => current.filter(entry => entry.id !== id)),
      removeBoth: (first, second) => setEntries(current => current.filter(entry => entry.id !== first && entry.id !== second)),
      retainBoth: (first, second) => setEntries(current => current.map(entry => entry.id === first || entry.id === second ? {...entry, retained: true} : entry)),
      add: id => setEntries(current => [...current.filter(entry => entry.id !== id), {id, retained: false}]),
      detachRoot: () => list.current?.remove(),
      replaceRoot: () => setRootKey(key => key + 1),
      replaceSection: () => setSectionKey(key => key + 1),
      detachSection: () => start.current?.remove(),
      hideFirst: id => setHidden(current => [...current, id]),
      committed: () => commits.current,
    };
  });
  return <div>
    <button key={`section-${sectionKey}`} ref={start} type="button" data-section-start>Section start</button>
    <div key={`root-${rootKey}`} ref={list} tabIndex={-1} data-removal-list {...handlers}>
      {entries.map(entry => <div key={entry.id} data-removal-item={entry.id} data-retained={entry.retained || undefined}
        inert={entry.retained || undefined} aria-hidden={entry.retained || undefined}
        ref={node => { if (node) nodes.current.set(entry.id, node); else nodes.current.delete(entry.id); }}>
        <button type="button" data-primary style={hidden.includes(entry.id) ? {visibility: 'hidden'} : undefined}>{entry.id}</button>
        <button type="button" data-secondary>open {entry.id}</button>
      </div>)}
    </div>
  </div>;
}
