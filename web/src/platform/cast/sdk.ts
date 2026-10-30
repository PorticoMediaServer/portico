/** The existing Cast sender URL, covered by the server's current script-src policy. */
const SDK_URL = 'https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1';

export type CastMessageListener = (namespace: string, message: string) => void;
export type CastSession = Readonly<{
  getCastDevice(): {friendlyName: string; label?: string};
  sendMessage(namespace: string, message: unknown): Promise<unknown>;
  addMessageListener(namespace: string, listener: CastMessageListener): void;
  removeMessageListener(namespace: string, listener: CastMessageListener): void;
  endSession(stopReceiver: boolean): void | Promise<void>;
}>;
export type CastSessionEvent = Readonly<{sessionState: string}>;
export type CastContext = Readonly<{
  setOptions(options: {receiverApplicationId: string; autoJoinPolicy: string}): void;
  requestSession(): Promise<unknown>;
  getCurrentSession(): CastSession | null;
  addEventListener(type: string, listener: (event: CastSessionEvent) => void): void;
  removeEventListener(type: string, listener: (event: CastSessionEvent) => void): void;
}>;
export type CastFramework = Readonly<{
  CastContext: {getInstance(): CastContext};
  CastContextEventType: {SESSION_STATE_CHANGED: string};
  SessionState: Record<string, string>;
}>;

/** A structural SDK boundary, also usable by a controlled SDK transport after verification resumes. */
export type CastBrowser = {
  document: Document;
  navigator: Pick<Navigator, 'userAgent'>;
  chrome?: {cast?: {AutoJoinPolicy: {ORIGIN_SCOPED: string}}};
  cast?: {framework: CastFramework};
  __onGCastApiAvailable?: (available: boolean) => void;
  setTimeout(callback: () => void, milliseconds: number): number;
  clearTimeout(timer: number): void;
  requestIdleCallback?(callback: IdleRequestCallback): number;
  cancelIdleCallback?(handle: number): void;
};

export function currentCastBrowser(): CastBrowser | undefined {
  return typeof window === 'undefined' ? undefined : window as unknown as CastBrowser;
}

/** Browser support is separate from a configured receiver and from an available SDK. */
export function isCastBrowser(browser: CastBrowser | undefined): browser is CastBrowser {
  return !!browser?.chrome && /(?:Chrome|Chromium|Edg)\//.test(browser.navigator.userAgent)
    && !/(?:OPR|SamsungBrowser)\//.test(browser.navigator.userAgent);
}

/** A browser description, never an account name or an inferred receiver model. */
export function castDisplayName(userAgent = typeof navigator === 'undefined' ? '' : navigator.userAgent): string {
  const browser = /Edg\//.test(userAgent) ? 'Edge' : /(?:Chrome|Chromium)\//.test(userAgent) ? 'Chrome' : '';
  if (!browser) return '';
  const platform = /CrOS/.test(userAgent) ? 'Chromebook' : /Mac OS X|Macintosh/.test(userAgent) ? 'Mac'
    : /Windows/.test(userAgent) ? 'Windows' : /Android/.test(userAgent) ? 'Android' : /Linux/.test(userAgent) ? 'Linux' : '';
  return platform ? `${browser} on ${platform}` : browser;
}

const pending = new WeakMap<CastBrowser, Promise<CastFramework | undefined>>();

/** No module/constructor side effect. Called only from the idle or player-open entry points. */
export function loadCastSdk(browser: CastBrowser): Promise<CastFramework | undefined> {
  if (!isCastBrowser(browser)) return Promise.resolve(undefined);
  if (browser.cast?.framework) return Promise.resolve(browser.cast.framework);
  const existing = pending.get(browser);
  if (existing) return existing;
  const loading = new Promise<CastFramework | undefined>(resolve => {
    const previous = browser.__onGCastApiAvailable;
    let settled = false, created = false, script: HTMLScriptElement | undefined;
    const finish = (framework?: CastFramework) => {
      if (settled) return;
      settled = true;
      browser.clearTimeout(timer);
      script?.removeEventListener('error', failed);
      if (browser.__onGCastApiAvailable === available) {
        if (previous) browser.__onGCastApiAvailable = previous;
        else delete browser.__onGCastApiAvailable;
      }
      if (!framework && created) script?.remove();
      resolve(framework);
    };
    const available = (ready: boolean) => {
      try { previous?.(ready); } catch { /* Another SDK consumer does not own this completion. */ }
      finish(ready ? browser.cast?.framework : undefined);
    };
    const failed = () => finish();
    const timer = browser.setTimeout(failed, 8000);
    browser.__onGCastApiAvailable = available;
    try {
      script = browser.document.querySelector<HTMLScriptElement>(`script[src="${SDK_URL}"]`) ?? undefined;
      if (!script) {
        script = browser.document.createElement('script');
        script.src = SDK_URL;
        script.async = true;
        created = true;
      }
      script.addEventListener('error', failed);
      if (created) browser.document.head.appendChild(script);
    } catch { finish(); }
  });
  pending.set(browser, loading);
  void loading.then(framework => { if (!framework && pending.get(browser) === loading) pending.delete(browser); });
  return loading;
}
