import {Linking} from 'react-native';

/** The app links the native allowlists forward besides share links, server invitations and the
 * sign-in return (PorticoUrlRoutes.kt / PorticoURLRoutes.swift, the same grammar route for route):
 * - N5-1: `portico://item/{id}` (open the title) and `portico://play/{id}` (play it), from the Top
 *   Shelf feed, the Android TV home screen, Siri, Spotlight and Handoff. They name no server: they
 *   are for the server this device has open now, resolved against the signed-in viewer before
 *   anything opens or plays.
 * - N2-1: `portico://account[?tab=…]` (web.getportico.tv/account: this server's Settings › Profile,
 *   as web's own /account alias), `portico://together/join/{code}` (a Watch Together code),
 *   `portico://invite#invite=…` (an old Portico Account invitation, which says to ask for a new
 *   one) and `portico://downloads`.
 * Input only, like share links: pending until a shell is ready, from a cold launch or a warm open,
 * on every platform including TV. */
export type NativeContentLinkTarget =
  | Readonly<{action: 'item'; itemId: string}>
  | Readonly<{action: 'play'; itemId: string}>
  | Readonly<{action: 'account'}>
  | Readonly<{action: 'together'; code: string}>
  | Readonly<{action: 'old-invite'}>
  | Readonly<{action: 'downloads'}>;
export type NativeContentLink = NativeContentLinkTarget & Readonly<{id: number; warm: boolean}>;

const fragmentPairs = (fragment: string) => fragment.split('&').filter(Boolean);
export function parseContentLink(raw: unknown): NativeContentLinkTarget | undefined {
  if (typeof raw !== 'string') return;
  const content = /^portico:\/\/(item|play)\/([A-Za-z0-9_-]{1,128})$/.exec(raw);
  if (content) return Object.freeze({action: content[1] === 'play' ? 'play' : 'item', itemId: content[2]});
  if (/^portico:\/\/account\/?(\?tab=(profiles|sessions|security))?$/.test(raw)) return Object.freeze({action: 'account'});
  if (/^portico:\/\/downloads\/?$/.test(raw)) return Object.freeze({action: 'downloads'});
  const together = /^portico:\/\/together\/join\/([A-Za-z0-9-]{4,16})$/.exec(raw);
  if (together) return Object.freeze({action: 'together', code: together[1].toUpperCase()});
  const invite = /^portico:\/\/invite\/?#(.*)$/.exec(raw);
  if (invite && fragmentPairs(invite[1]).length === 1 && /^invite=[A-Za-z0-9_.~-]{1,512}$/.test(invite[1])) return Object.freeze({action: 'old-invite'});
  return undefined;
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
