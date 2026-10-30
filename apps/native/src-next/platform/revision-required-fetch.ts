import {encodedRoutePath, routePattern, type RouteId} from '../../../../packages/contracts/src/routes.ts';
import {ALREADY_DONE, retryWhenStale} from '../../../../packages/client-core/src/freshness-actions.ts';
import {readConsoleJson, consoleUTF8Size} from '../../../../packages/client-core/src/console-transport.ts';

const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const revision = (v: unknown): v is number | string => typeof v === 'number' ? Number.isSafeInteger(v) && v >= 0 : typeof v === 'string' && /^\d{1,20}$/.test(v);
type Rule = {method: string; operation: RouteId; read: RouteId; header?: boolean; item?: boolean};
// Local adapter until client-core handles revision_required in every freshness consumer.
// Only an explicit 428 means the server refused to apply the request. Ordinary conflicts,
// interrupted writes, and requests without a revision-bearing resource keep their own policy.
const rules: readonly Rule[] = [
  {method: 'PATCH', operation: 'change_connectivity_policy', read: 'get_connectivity_policy'},
  {method: 'PATCH', operation: 'change_console_settings', read: 'get_console_settings'},
  {method: 'PUT', operation: 'put_admin_maintenance_settings', read: 'get_admin_maintenance_settings'},
  {method: 'PUT', operation: 'put_direct_profile_restrictions', read: 'get_direct_profile_restrictions'},
  {method: 'PUT', operation: 'put_download_settings', read: 'get_download_settings'},
  {method: 'PUT', operation: 'put_library_settings', read: 'get_library_settings'},
  {method: 'PUT', operation: 'put_metadata_settings', read: 'get_metadata_settings'},
  {method: 'PUT', operation: 'reorder_playlist_window', read: 'get_playlist_order'},
  {method: 'PUT', operation: 'set_container_personal_state', read: 'get_container_personal_state'},
  {method: 'PUT', operation: 'set_live_settings', read: 'get_live_settings'},
  {method: 'PUT', operation: 'set_live_source_configuration', read: 'get_live_source_configuration'},
  {method: 'DELETE', operation: 'delete_direct_member', read: 'get_direct_member_deletion_preview'},
  {method: 'DELETE', operation: 'remove_library', read: 'get_admin_libraries_id_inventory_config'},
  {method: 'POST', operation: 'control_admin_session', read: 'get_admin_session_controls'},
  {method: 'PATCH', operation: 'change_playback_session', read: 'get_playback_session', header: true},
  ...(['add_queue_segment', 'advance_queue', 'commit_queue_next', 'move_queue_entry', 'prepare_queue_next', 'save_queue_as_playlist'] as const).map(operation => ({method: 'POST', operation, read: 'get_queue' as const, header: true})),
  {method: 'DELETE', operation: 'remove_queue_entry', read: 'get_queue', header: true},
  {method: 'PATCH', operation: 'update_queue', read: 'get_queue', header: true},
];
const compiled = rules.map(rule => {
  const keys: string[] = [];
  const pattern = routePattern(rule.operation).split(/(\{[^}]+\})/).map(part => {
    if (part.startsWith('{')) { keys.push(part.slice(1, -1)); return '([^/:]+)'; }
    return part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }).join('');
  return {...rule, keys, pattern: new RegExp('^' + pattern + '$')};
});

/** Same transport, authority, deadline and operation ID; a refused revision is read and retried. */
export function revisionRequiredFetch(fetcher: typeof fetch): typeof fetch {
  return async (input, options = {}) => {
    if (typeof input !== 'string') return fetcher(input, options);
    const url = new URL(input), method = (options.method ?? 'GET').toUpperCase();
    const rule = compiled.find(candidate => candidate.method === method && candidate.pattern.test(url.pathname));
    if (!rule) return fetcher(input, options);
    const match = rule.pattern.exec(url.pathname)!;
    const parameters = Object.fromEntries(rule.keys.map((key, i) => [key, match[i + 1]!]));
    const readParameters = Object.fromEntries([...routePattern(rule.read).matchAll(/\{([^}]+)\}/g)].map(m => [m[1]!, parameters[m[1]!]!]));
    // Captures are already escaped. Do not decode IDs and then accidentally turn them into paths.
    const readPath = encodedRoutePath(rule.read, readParameters as never);
    let next = options;
    const signal = options.signal ?? new AbortController().signal;
    try {
      const result = await retryWhenStale(async () => {
        const response = await fetcher(input, next);
        if (response.status !== 428) return response;
        const text = await response.text();
        if (consoleUTF8Size(text) > 8192) throw new Error('Revision response exceeded its bound.');
        const saved = new Response(text, {status: response.status, headers: response.headers});
        let raw: unknown; try { raw = JSON.parse(text); } catch { return saved; }
        if (!object(raw) || !object(raw.error) || raw.error.code !== 'revision_required') return saved;
        throw {code: 'revision_required', status: 428, response: saved};
      }, {
        isStale: e => object(e) && e.status === 428 && e.code === 'revision_required',
        reread: async () => {
          const headers = new Headers(options.headers);
          for (const key of ['If-Match', 'Content-Type', 'Content-Length', 'Idempotency-Key']) headers.delete(key);
          let current: unknown, cursor = '';
          const seen = new Set<string>();
          // The device endpoint is paged. Keep the read bounded by both the caller's deadline
          // and 32 pages; a missing device or an unusable cursor must never replay the write.
          for (let page = 0; page < (rule.item ? 32 : 1); page++) {
            const query = rule.item ? '?' + new URLSearchParams({limit: '100', ...(cursor ? {cursor} : {})}) : '';
            const raw = await readConsoleJson<unknown>(await fetcher(url.origin + readPath + query, {...options, method: 'GET', headers, body: undefined}), signal);
            const data = object(raw) && object(raw.data) ? raw.data : object(raw) && object(raw.result) ? raw.result : raw;
            current = rule.item && object(data) && Array.isArray(data.items) ? data.items.find(v => object(v) && v.id === decodeURIComponent(parameters.id!)) : data;
            if (current !== undefined || !rule.item || !object(data)) break;
            if (typeof data.nextCursor !== 'string' || !data.nextCursor || data.nextCursor.length > 4096 || seen.has(data.nextCursor)) break;
            cursor = data.nextCursor; seen.add(cursor);
          }
          if (!object(current) || !revision(current.revision)) throw new Error('The current revision could not be read.');
          if (signal.aborted) throw new Error('Revision read cancelled.');
          if (rule.header) {
            const updated = new Headers(options.headers);
            updated.delete('Content-Length');
            updated.set('If-Match', String(current.revision));
            next = {...options, headers: updated};
          } else {
            const body: unknown = typeof options.body === 'string' ? JSON.parse(options.body) : {};
            if (!object(body)) throw new Error('A revision-bearing object is required.');
            const updated = new Headers(options.headers);
            updated.delete('Content-Length');
            next = {...options, headers: updated, body: JSON.stringify({...body, expectedRevision: current.revision})};
          }
        },
      });
      if (result === ALREADY_DONE) throw new Error('Revision retry ended without a response.');
      return result;
    } catch (e) {
      if (object(e) && e.response instanceof Response) return e.response;
      throw e;
    }
  };
}
