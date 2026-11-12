import type {Routes} from '../../../contracts/src/next/api.ts';

/** Anything that can send one request to the selected server (`HttpLocalApi` is one). */
export type ServerRequester = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

/** The address of a route on the new server, with its `{id}` and query filled in. */
export function serverPath(route: keyof Routes, options: Readonly<{id?: string; ids?: Readonly<Record<string, string>>; query?: Readonly<Record<string, string | number | boolean | undefined>>}> = {}): string {
  let path = route.slice(route.indexOf(' ') + 1);
  // The route's first placeholder ({id}, or another name such as {name}) takes `id`; any later one is named in `ids`
  // ({entryId}: `ids.entryId`).
  const placeholder = /\{[a-zA-Z]+\}/.exec(path)?.[0];
  if (placeholder) {
    if (!options.id) throw new Error('This route needs an id.');
    path = path.replace(placeholder, encodeURIComponent(options.id));
  }
  path = path.replace(/\{([a-zA-Z]+)\}/g, (_, name: string) => {
    const value = options.ids?.[name];
    if (!value) throw new Error(`This route needs ${name}.`);
    return encodeURIComponent(value);
  });
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(options.query ?? {})) if (value !== undefined && value !== '') query.set(name, String(value));
  return query.size ? path + '?' + query.toString() : path;
}

/** One request to the new server, typed by the route table generated from the server's own routes. */
export function callServer<K extends keyof Routes>(api: ServerRequester, route: K,
  options: Readonly<{id?: string; ids?: Readonly<Record<string, string>>; query?: Readonly<Record<string, string | number | boolean | undefined>>; body?: Routes[K]['request']; signal?: AbortSignal}> = {}): Promise<Routes[K]['response']> {
  return api.request<Routes[K]['response']>(serverPath(route, options), route.slice(0, route.indexOf(' ')), options.body, options.signal);
}

/** The server answered that it has no such address. While the server is being rebuilt, that is how
 * an area it does not serve yet looks: start-up treats that area as absent instead of failing. */
export function notOnThisServer(error: unknown): boolean {
  const e = error as {status?: unknown; code?: unknown} | null;
  return e?.status === 404 && e.code === 'not_found';
}
