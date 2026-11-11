import type {Routes} from '../../../contracts/src/next/api.ts';

/**
 * The address of a playback route on the server, from the route table generated from the
 * server's own routes, with `{id}` and `{entryId}` filled in. Only the path: the method is the
 * caller's, and must be the one the key names.
 */
export function playbackPath(route: keyof Routes, ids: Readonly<{id?: string; entryId?: string}> = {}, query?: URLSearchParams): string {
  let path = route.slice(route.indexOf(' ') + 1);
  for (const name of ['id', 'entryId'] as const) {
    if (!path.includes(`{${name}}`)) continue;
    const value = ids[name];
    if (!value) throw new Error(`This route needs ${name}.`);
    path = path.replace(`{${name}}`, encodeURIComponent(value));
  }
  const q = query?.toString();
  return q ? path + '?' + q : path;
}
