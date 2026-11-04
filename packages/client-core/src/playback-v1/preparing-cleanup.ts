import {requestDeadlineMs} from '../index.ts';
import {encodedRoutePath} from '../../../contracts/src/routes.ts';
import {SessionsClient, type PreparingSessionView} from './sessions.ts';
import {readBoundedJson} from '../bounded-json.ts';

/** Cleanup uses only the captured pin-bound executor and exact original token. */
export async function stopOriginalPreparingSession(fetcher: typeof fetch, origin: string, accessToken: string, current: () => boolean, offered: PreparingSessionView, signal: AbortSignal, uninstalled: () => boolean, claim: () => boolean): Promise<void> {
  const allowed = () => !signal.aborted && current() && uninstalled();
  const readPath = encodedRoutePath('get_playback_session', {id: encodeURIComponent(offered.id)});
  const stopPath = encodedRoutePath('stop_playback_session', {id: encodeURIComponent(offered.id)});
  const client = new SessionsClient({send: async request => {
    if (!allowed() || request.body !== undefined || !(request.method === 'GET' && request.path === readPath
      || request.method === 'DELETE' && request.path === stopPath)) throw new Error('Original preparing owner retired');
    const round = new AbortController(), abort = () => round.abort();
    signal.addEventListener('abort', abort, {once: true}); if (signal.aborted) abort();
    const timer = setTimeout(abort, requestDeadlineMs(request.path, request.method));
    try {
      const response = await fetcher(origin + request.path, {method: request.method, signal: round.signal,
        headers: {Accept: 'application/json', Authorization: 'Bearer ' + accessToken},
        redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', cache: 'no-store'});
      if (round.signal.aborted || !allowed()) {void response.body?.cancel().catch(() => {}); throw new Error('Original preparing owner retired');}
      const headers: Record<string, string> = {}; response.headers.forEach((value, name) => {headers[name] = value;});
      let body: unknown;
      if (request.method === 'GET' && response.status === 200) {
        if (response.body) body = await readBoundedJson(response, 1_048_576, round.signal);
        else {
          // RN's native networkRequest already bounds the buffered body;
          // its Response shim need not expose a ReadableStream. Keep the
          // same explicit JSON/byte limit before interpreting that buffer.
          const length = response.headers.get('Content-Length');
          if (response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'application/json'
            || length !== null && (!/^\d+$/.test(length) || Number(length) > 1_048_576)) throw new Error('Unreadable preparing session');
          const text = await response.text();
          if (new TextEncoder().encode(text).length > 1_048_576) throw new Error('Unreadable preparing session');
          body = JSON.parse(text);
        }
      } else {void response.body?.cancel().catch(() => {});}
      if (round.signal.aborted || !allowed()) throw new Error('Original preparing owner retired');
      return {status: response.status, headers, body};
    } finally {clearTimeout(timer); signal.removeEventListener('abort', abort);}
  }});
  const observed = await client.get(offered.id, signal);
  if (observed.id !== offered.id || observed.kind !== offered.kind || observed.presentation.generation !== offered.presentation.generation || observed.role !== 'local'
    || observed.state === 'ended' || observed.itemId !== offered.itemId || observed.versionId !== offered.versionId
    || observed.queue?.queueId !== offered.queue?.queueId || observed.queue?.entryId !== offered.queue?.entryId
    || observed.discTitle?.id !== offered.discTitle?.id || observed.discTitle?.revision !== offered.discTitle?.revision || !claim()) return;
  await client.stop(offered.id, undefined, signal);
}
