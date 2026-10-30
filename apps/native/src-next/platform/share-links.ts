import {Linking, Platform} from 'react-native';
import {sharedRouteToWebUrl} from '../screens/share/share-link';

/** O-162: the one pending shared link, from a cold launch or a warm open. Input only: it grants
 * no server, identity or content authority, and it survives a server switch or sign-in within
 * this process (never a restart: a cold start reads the launch URL again). TV never opens links. */
export type NativeShareLink = Readonly<{id: number; webUrl: string; warm: boolean}>;
let pending: NativeShareLink | undefined, serial = 0, owners = 0, initialRead = false, warmSeen = false;
let offNative: (() => void) | undefined;
const listeners = new Set<() => void>();
const publish = () => {for (const listener of listeners) {try {listener();} catch { /* Input publication grants no authority. */ }}};
function receive(raw: unknown, warm: boolean) {
  const webUrl = sharedRouteToWebUrl(raw);
  if (!webUrl) return;
  if (warm) warmSeen = true;
  pending = Object.freeze({id: ++serial, webUrl, warm});
  publish();
}

export const nativeShareLinks = Object.freeze({
  getSnapshot: () => pending,
  subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
  /** The consumer takes the link it is acting on; a newer link is never cleared by an older one. */
  take: (link: NativeShareLink) => {if (pending?.id !== link.id) return false; pending = undefined; publish(); return true;},
  acquire: () => {
    if (Platform.isTV) return () => {};
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
});
