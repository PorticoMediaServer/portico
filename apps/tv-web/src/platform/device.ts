import {setExitApp} from '../shims/keys';

/**
 * Which set this is. One build runs on both: LG's web engine has `PalmServiceBridge` and says
 * "Web0S" in its user agent; Samsung's has `tizen`. Anything else is a desktop browser (a
 * developer or a test), which plays the LG part unless the page asks for `?platform=tizen`.
 */
export type TVPlatform = 'webos' | 'tizen';

type Tizen = {application?: {getCurrentApplication(): {exit(): void}}; tvinputdevice?: {registerKeyBatch(keys: string[], ok?: () => void, failed?: (error: unknown) => void): void}};
const tizen = (): Tizen | undefined => (globalThis as {tizen?: Tizen}).tizen;
const userAgent = () => globalThis.navigator?.userAgent ?? '';

export const tvPlatform: TVPlatform = (() => {
  if (tizen() || /Tizen/i.test(userAgent())) return 'tizen';
  if ('PalmServiceBridge' in globalThis || /Web0S|webOS/i.test(userAgent())) return 'webos';
  return /[?&]platform=tizen\b/.test(globalThis.location?.search ?? '') ? 'tizen' : 'webos';
})();

/** Whether this is a real set rather than a desktop browser standing in for one. */
export const onSet: boolean = !!tizen() || 'PalmServiceBridge' in globalThis || /Tizen|Web0S|webOS/i.test(userAgent());

/** The browser engine's version, from its user agent ("Chrome/87.0.4280.88"). */
export function engineVersion(): number | undefined {
  const match = /Chrome\/(\d+)/.exec(userAgent());
  return match ? Number(match[1]) : undefined;
}

export type DeviceFacts = Readonly<{model?: string; osVersion?: string; deviceName?: string}>;
let facts: Promise<DeviceFacts> | undefined;
/** What the set says about itself; filled in by the capability work. */
export function deviceFacts(): Promise<DeviceFacts> {
  facts ??= Promise.resolve({});
  return facts;
}

/** The keys a Samsung set sends to an app only when asked for (arrows, OK and Back always arrive). */
const SAMSUNG_KEYS = ['MediaPlayPause', 'MediaPlay', 'MediaPause', 'MediaStop', 'MediaFastForward', 'MediaRewind', 'ChannelUp', 'ChannelDown'];

export function startDevice(): void {
  if (tvPlatform === 'tizen') {
    try { tizen()?.tvinputdevice?.registerKeyBatch(SAMSUNG_KEYS, undefined, () => {}); } catch { /* not on a set */ }
    setExitApp(() => { try { tizen()?.application?.getCurrentApplication().exit(); } catch { window.close(); } });
  } else {
    setExitApp(() => window.close());
  }
}
