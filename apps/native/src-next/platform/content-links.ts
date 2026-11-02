import {Linking} from 'react-native';

/** N5-1: `portico://item/{id}` (open the title) and `portico://play/{id}` (play it), the links the
 * Top Shelf feed, the Android TV home screen, Siri, Spotlight and Handoff hand the app. The same
 * grammar the native allowlists keep (PorticoUrlRoutes.kt / PorticoURLRoutes.swift
 * `isContentRoute`). They name no server: they are for the server this device has open now, and
 * are resolved against the signed-in viewer before anything opens or plays. Input only, like
 * share links: pending until a shell is ready, from a cold launch or a warm open, on every
 * platform including TV. */
export type NativeContentLink = Readonly<{id: number; action: 'item' | 'play'; itemId: string; warm: boolean}>;

export function parseContentLink(raw: unknown): Pick<NativeContentLink, 'action' | 'itemId'> | undefined {
  if (typeof raw !== 'string') return;
  const match = /^portico:\/\/(item|play)\/([A-Za-z0-9_-]{1,128})$/.exec(raw);
  return match ? Object.freeze({action: match[1] as 'item' | 'play', itemId: match[2]}) : undefined;
}

let pending: NativeContentLink | undefined, serial = 0, owners = 0, initialRead = false, warmSeen = false;
let offNative: (() => void) | undefined;
const listeners = new Set<() => void>();
const publish = () => {for (const listener of listeners) {try {listener();} catch { /* Input publication grants no authority. */ }}};
function receive(raw: unknown, warm: boolean) {
  const link = parseContentLink(raw);
  if (!link) return;
  if (warm) warmSeen = true;
  pending = Object.freeze({id: ++serial, ...link, warm});
  publish();
}

export const nativeContentLinks = Object.freeze({
  getSnapshot: () => pending,
  subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
  /** The consumer takes the link it is acting on; a newer link is never cleared by an older one. */
  take: (link: NativeContentLink) => {if (pending?.id !== link.id) return false; pending = undefined; publish(); return true;},
  acquire: () => {
    owners++;
    if (!offNative) {
      const subscription = Linking.addEventListener('url', event => receive(event.url, true));
      offNative = () => subscription.remove();
    }
    if (!initialRead) {
      initialRead = true;
      let initial: Promise<string | null>;
      try {initial = Linking.getInitialURL();} catch {initial = Promise.resolve(null);}
      // A warm link that overtook the cold-launch read wins.
      void initial.then(raw => {if (!warmSeen) receive(raw, false);}, () => {});
    }
    let released = false;
    return () => {if (released) return; released = true; if (--owners === 0) {offNative?.(); offNative = undefined;}};
  },
  /** Tests only. */
  resetForTest: () => {pending = undefined; serial = 0; owners = 0; initialRead = false; warmSeen = false; offNative?.(); offNative = undefined; listeners.clear();},
});
