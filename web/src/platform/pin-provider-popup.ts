import {CodedError} from '@core/server-messages.ts';

/** Forgot PIN's Google or Apple step on the web. web.getportico.tv opens the provider in a pop-up, so
 * the recovery dialog and its PIN proof stay open in this page. Services sends the pop-up back to
 * /app/account-return, which hands the return to this page and closes. Everything stays on this
 * origin: the hand-over is a same-origin message from the pop-up this page opened, for this flow only. */
const flowKey = 'portico.pinProviderFlow';
const kind = 'portico.pin-provider-return';

/** Opens the provider and answers the exact return address for `flowId`, or refuses: a blocked
 * pop-up (`popup_blocked`), or one closed before it returned (`provider_return_incomplete`). */
export function openProviderPopup(url: string, flowId: string, page: Window = window, signal?: AbortSignal): Promise<string> {
  return new Promise((resolve, reject) => {
    // A pop-up starts with a copy of this tab's session storage: that is how its return page knows the flow.
    try {page.sessionStorage.setItem(flowKey, flowId);} catch { /* the return page then offers nothing to hand back */ }
    const popup = page.open(url, 'portico-provider', 'popup,width=480,height=720');
    if (!popup) {
      try {page.sessionStorage.removeItem(flowKey);} catch { /* nothing kept */ }
      reject(new CodedError('popup_blocked', 'The browser blocked the sign-in window.')); return;
    }
    let timer: ReturnType<typeof setInterval> | undefined;
    const finish = () => {
      page.removeEventListener('message', onMessage); if (timer) clearInterval(timer); signal?.removeEventListener('abort', onAbort);
      try {page.sessionStorage.removeItem(flowKey);} catch { /* nothing kept */ }
    };
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== page.location.origin || event.source !== popup) return;
      const data = event.data as {kind?: unknown; href?: unknown} | null;
      if (!data || data.kind !== kind || typeof data.href !== 'string') return;
      let tx: string | null = null;
      try {
        const returned = new URL(data.href);
        if (returned.origin !== page.location.origin || returned.pathname !== '/app/account-return') return;
        tx = new URLSearchParams(returned.hash.slice(1)).get('tx');
      } catch {return;}
      if (tx !== flowId) return;
      finish(); resolve(data.href);
    };
    const onAbort = () => {finish(); try {popup.close();} catch { /* already gone */ } reject(signal!.reason ?? new CodedError('cancelled', 'Cancelled.'));};
    page.addEventListener('message', onMessage);
    signal?.addEventListener('abort', onAbort, {once: true});
    timer = setInterval(() => {if (popup.closed) {finish(); reject(new CodedError('provider_return_incomplete', 'The sign-in window closed before it finished.'));}}, 500);
  });
}

/** In the pop-up's return page: hands the return to the page that opened it for this flow, and says
 * whether it did (the caller then closes the window). Never completes a sign-in here. */
export function returnToOpener(id: string, code: string, page: Window = window): boolean {
  let flow: string | null = null;
  try {flow = page.sessionStorage.getItem(flowKey);} catch {return false;}
  const opener = page.opener as Window | null;
  if (!flow || flow !== id || !opener) return false;
  try {if (opener.location.origin !== page.location.origin) return false;} catch {return false;}
  const href = `${page.location.origin}/app/account-return#${new URLSearchParams({tx: id, code}).toString()}`;
  opener.postMessage({kind, href}, page.location.origin);
  try {page.sessionStorage.removeItem(flowKey);} catch { /* nothing kept */ }
  return true;
}
