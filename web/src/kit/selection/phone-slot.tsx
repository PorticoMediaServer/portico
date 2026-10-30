import {createContext, useContext, useLayoutEffect, useRef, useSyncExternalStore, type ReactNode} from 'react';
import type {SelectionActions, SelectionCount} from './types';

/** A live screen selection's phone action bar. The publishing screen owns the
 * count and actions; this store only hands them to the shell that wrapped it, so
 * two shells never share one selection (select.html 16 and 17). Membership,
 * grants and commands stay with the screen's own selection model. */
export type PhoneSelectionContent = Readonly<{selection: SelectionCount; actions: SelectionActions}>;

type Lease = Readonly<{set(content: PhoneSelectionContent): void; retire(): void}>;
type Store = Readonly<{
  subscribePresence(listener: () => void): () => void;
  presence(): boolean;
  subscribeContent(listener: () => void): () => void;
  content(): PhoneSelectionContent | undefined;
  /** The last publisher holds the lease. A retained publisher's later calls and
   * cleanup are no-ops once a successor holds it. */
  acquire(content: PhoneSelectionContent): Lease;
}>;

function createStore(): Store {
  const presenceListeners = new Set<() => void>(), contentListeners = new Set<() => void>();
  let active: Readonly<{token: object; content: PhoneSelectionContent}> | undefined;
  const notify = (listeners: Set<() => void>) => {for (const listener of listeners) listener();};
  const subscribe = (listeners: Set<() => void>) => (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};};
  return {
    subscribePresence: subscribe(presenceListeners), presence: () => active !== undefined,
    subscribeContent: subscribe(contentListeners), content: () => active?.content,
    acquire(content) {
      const token = {}, wasPresent = active !== undefined;
      active = {token, content}; notify(contentListeners);
      if (!wasPresent) notify(presenceListeners);
      return {
        set(next) {if (active?.token !== token) return; active = {token, content: next}; notify(contentListeners);},
        retire() {
          if (active?.token !== token) return;
          active = undefined; notify(contentListeners); notify(presenceListeners);
        },
      };
    },
  };
}

const PhoneSelectionContext = createContext<Store | null>(null);

/** One store per shell. Screens inside this shell publish to it; another shell has
 * its own, so a replaced shell can never show the other's selection. */
export function PhoneSelectionProvider({children}: {children: ReactNode}) {
  const held = useRef<Store | undefined>(undefined);
  if (held.current === undefined) held.current = createStore();
  const store = held.current;
  return <PhoneSelectionContext.Provider value={store}>{children}</PhoneSelectionContext.Provider>;
}

function useStore(): Store {
  const store = useContext(PhoneSelectionContext);
  if (!store) throw new Error('phone-selection-provider-required');
  return store;
}

/** The shell draws a bar while a screen holds one. */
export function usePhoneSelectionPresent(): boolean {
  const store = useStore();
  return useSyncExternalStore(store.subscribePresence, store.presence, store.presence);
}
/** The bar itself, which follows the published count and actions. */
export function usePhoneSelectionContent(): PhoneSelectionContent | undefined {
  const store = useStore();
  return useSyncExternalStore(store.subscribeContent, store.content, store.content);
}

/** Publish this screen's live selection (undefined publishes nothing). The lease is
 * taken on the first publication and retired when the publisher unmounts; a later
 * publisher replaces the content and keeps its own lease. */
export function usePublishPhoneSelection(value: PhoneSelectionContent | undefined): void {
  const store = useStore(), held = useRef<Readonly<{store: Store; lease: Lease}> | undefined>(undefined);
  useLayoutEffect(() => {
    if (!value) {
      held.current?.lease.retire(); held.current = undefined;
      return;
    }
    if (held.current?.store === store) held.current.lease.set(value);
    else held.current = {store, lease: store.acquire(value)};
  });
  useLayoutEffect(() => () => {
    held.current?.lease.retire();
    held.current = undefined;
  }, [store]);
}
