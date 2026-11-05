import {NativeModules, Platform} from 'react-native';
import {issueTopShelfToken} from '../../../../packages/client-core/src/query/connection-operations';
import {parseTopShelfToken} from '../../../../packages/client-core/src/identity-devices';
import {serverNow} from '../../../../packages/client-core/src/server-clock';

/** The native writer (Native/PorticoTopShelf.m) the shelf extension reads from. */
export type NativeTopShelfPort = Readonly<{
  configure: (baseUrl: string, token: string, expiresAt: string, owner: string) => Promise<void>;
  clear: () => Promise<void>;
  changed: () => void;
}>;
type Viewer = Readonly<{serverId: string; accountId: string; profileId: string; authority: string}>;
export type NativeTopShelfOwner = Readonly<{
  current: () => boolean;
  /** The signed-in viewer and the installation its session is bound to. */
  viewer: () => (Viewer & {installationId?: string}) | undefined;
  /** The server's origin as this app reaches it. */
  baseUrl: string;
  tokenApi: {request: (path: string, method: 'POST', body: unknown, signal?: AbortSignal) => Promise<unknown>};
  /** Fires when something stops or changes on the player, so Continue Watching is fresh. */
  subscribePlayback: (listener: () => void) => () => void;
  playbackKey: () => string | undefined;
}>;
type Timers = Readonly<{set: (run: () => void, ms: number) => unknown; clear: (handle: unknown) => void}>;

const day = 24 * 60 * 60 * 1000;
// setTimeout's longest delay; a thirty-day token is renewed in steps.
const longestTimer = 2 ** 31 - 1;

/** Only an origin the extension accepts (ContentProvider.swift: http(s), a host, no
 * credentials, query, fragment or path). Anything else leaves the shelf cleared. */
export function topShelfOrigin(baseUrl: string): string | undefined {
  try {
    const url = new URL(baseUrl);
    if (url.protocol !== 'https:' && url.protocol !== 'http:' || !url.hostname || url.username || url.password || url.search || url.hash) return;
    if (url.pathname !== '' && url.pathname !== '/') return;
    return url.origin;
  } catch {return;}
}

/** Whose shelf this is. The extension keys its poster cache by origin and owner, and the
 * native module clears the cache when the owner changes. */
export const topShelfOwnerKey = (viewer: Viewer): string => JSON.stringify([viewer.serverId, viewer.accountId, viewer.profileId]);

/** N3-1: the signed-in TV app gives its home-screen shelf the server's origin and a
 * feed token (POST /v1/topshelf/token), renews it a day before it lapses, tells the shelf
 * when playback stops or moves on, and clears it when this viewer's runtime retires (sign-out,
 * profile switch). The token goes only to the native module, never to a log or a snapshot.
 * The server issues feed tokens to local accounts only, so any other viewer clears the shelf. */
export function bindTopShelf(owner: NativeTopShelfOwner, port: NativeTopShelfPort, reportError: (error: unknown) => void, timers: Timers = {set: setTimeout, clear: handle => clearTimeout(handle as never)}): () => void {
  let live = true, timer: unknown, abort: AbortController | undefined;
  const current = () => live && owner.current();
  const fail = (error: unknown) => {if (current()) reportError(error);};
  const viewer = owner.viewer(), origin = topShelfOrigin(owner.baseUrl);
  const schedule = (expiresAt: string) => {
    const renewAt = Date.parse(expiresAt) - day - serverNow();
    timer = timers.set(() => {timer = undefined; if (current()) void issue();}, Math.max(60_000, Math.min(renewAt, longestTimer)));
  };
  const issue = async () => {
    if (!viewer?.installationId || !origin) return;
    abort?.abort(); abort = new AbortController();
    const grant = parseTopShelfToken(await issueTopShelfToken(owner.tokenApi, viewer.installationId, abort.signal));
    if (!current()) return;
    await port.configure(origin, grant.token, grant.expiresAt, topShelfOwnerKey(viewer));
    if (current()) schedule(grant.expiresAt);
  };
  if (viewer?.authority === 'local' && viewer.installationId && origin) void issue().catch(fail);
  else void port.clear().catch(fail);
  let playing = owner.playbackKey();
  const unsubscribe = owner.subscribePlayback(() => {
    if (!current()) return;
    const next = owner.playbackKey();
    if (playing !== undefined && next !== playing) {try {port.changed();} catch (error) {fail(error);}}
    playing = next;
  });
  return () => {
    if (!live) return;
    live = false; unsubscribe(); abort?.abort();
    if (timer !== undefined) timers.clear(timer);
    // Another viewer's shelf must never be drawn: the next runtime configures its own.
    void port.clear().catch(reportError);
  };
}

/** Televisions only: the Apple TV Top Shelf extension, and on Android TV and Fire TV the
 * home screen's Watch Next row and Portico channel (PorticoTopShelf.kt, the same four methods
 * and the same feed). Phones and tablets have no shelf. */
export function bindNativeTopShelf(owner: NativeTopShelfOwner, reportError: (error: unknown) => void): (() => void) | undefined {
  if (!Platform.isTV || Platform.OS !== 'ios' && Platform.OS !== 'android' || !owner.current()) return;
  const native = NativeModules.PorticoTopShelf as Partial<NativeTopShelfPort> | undefined;
  if (typeof native?.configure !== 'function' || typeof native.clear !== 'function' || typeof native.changed !== 'function') return;
  return bindTopShelf(owner, {configure: native.configure, clear: native.clear, changed: native.changed}, reportError);
}
