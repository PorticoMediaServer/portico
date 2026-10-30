/**
 * Forward-compatibility filter for content projections (moved from the two
 * clients' `app/compat.ts`, X-05). The server can advertise library
 * navigation views the core's strict projection validator doesn't know yet;
 * those entries are dropped so the page still loads. Nothing else is altered.
 *
 * The clients' old `artwork`-object folding is deliberately gone: no server
 * code emits `artwork` objects any more (every poster/backdrop URL is flat),
 * so it was dead weight.
 */
export const knownContentViews: ReadonlySet<string> = new Set(['home', 'discover', 'browse', 'collections', 'categories', 'show', 'season', 'artist', 'album', 'book', 'collection', 'releases', 'songs', 'authors', 'series', 'author', 'book_series', 'disc']);

export function sanitizeContentProjection(value: unknown): unknown {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  const record = value as Record<string, unknown>;
  let out: Record<string, unknown> | undefined;
  if (Array.isArray(record.navigation)) {
    const filtered = record.navigation.filter(n => n && typeof n === 'object' && knownContentViews.has(String((n as {view?: unknown}).view)));
    if (filtered.length !== record.navigation.length) out = {...record, navigation: filtered};
  }
  if (record.episodes && typeof record.episodes === 'object') {
    const inner = sanitizeContentProjection(record.episodes);
    if (inner !== record.episodes) out = {...(out ?? record), episodes: inner};
  }
  return out ?? value;
}

export type ContentRequestApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

/** Wraps an API so content, show-workspace and search reads pass through the filter. */
export function compatContentApi(api: ContentRequestApi): ContentRequestApi {
  return {
    async request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
      const result = await api.request<T>(path, method, body, signal);
      if (method === 'GET' && (path.includes('/content?') || path.includes('/show-workspace?') || path.includes('/search?'))) return sanitizeContentProjection(result) as T;
      return result;
    },
  };
}
