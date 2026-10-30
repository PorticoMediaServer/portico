import {checkResponse, type Exposure} from './credential-boundary';

/** An independent, bounded containment request to the issuer, never to a Location address.
 * This uses the same SDK limitation: a malicious issuer can redirect revocation too.
 * No refresh is attempted: refresh within an exposed family cannot replace that family safely.
 */
export async function revokeExposedSessions(exposure: Exposure): Promise<boolean> {
  const results = await Promise.all(exposure.sessions.map(session => new Promise<boolean>(resolve => {
    const xhr = new XMLHttpRequest();
    const url = exposure.origin + (session.hosted && session.refreshToken ? '/v1/sessions/revoke' : '/v1/sessions/current');
    let done = false;
    const finish = (ok: boolean) => { if (done) return; done = true; clearTimeout(timer); try { xhr.abort(); } catch { /* completed */ } resolve(ok); };
    const timer = setTimeout(() => finish(false), 5000);
    try {
      xhr.open(session.hosted && session.refreshToken ? 'POST' : 'DELETE', url);
      xhr.timeout = 5000;
      xhr.withCredentials = false;
      xhr.responseType = 'text';
      xhr.setRequestHeader('Cache-Control', 'no-store');
      xhr.setRequestHeader('Content-Type', 'application/json');
      if (!session.hosted || !session.refreshToken) xhr.setRequestHeader('Authorization', 'Bearer ' + session.accessToken);
      xhr.onreadystatechange = () => {
        if (xhr.readyState < 2) return;
        // Kepler publishes HEADERS_RECEIVED before assigning responseURL; final validation stays mandatory.
        if (xhr.readyState === 2 && !xhr.responseURL) return;
        try { checkResponse(url, xhr.responseURL, xhr.status, false); } catch { finish(false); return; }
        if (Number(xhr.getResponseHeader('Content-Length')) > 65536 || (xhr.responseText?.length ?? 0) > 65536) finish(false);
      };
      xhr.onprogress = () => { if ((xhr.responseText?.length ?? 0) > 65536) finish(false); };
      xhr.onload = () => {
        try { checkResponse(url, xhr.responseURL, xhr.status, false); } catch { finish(false); return; }
        finish(xhr.status >= 200 && xhr.status < 300);
      };
      xhr.onerror = xhr.ontimeout = xhr.onabort = () => finish(false);
      xhr.send(session.hosted && session.refreshToken ? JSON.stringify({refreshToken: session.refreshToken}) : undefined);
    } catch { finish(false); }
  })));
  return results.length > 0 && results.every(Boolean);
}
