/** What the rebuilt server answers a reader opening a book: the book route (how to read it), the title
 * with the profile's state, and a saved place. Nothing is opened or closed on the server. */
export type FixtureBook = Readonly<{itemId?: string; version?: string; format?: string; size?: number; title?: string}>;

export const fixtureFingerprint = '0123456789abcdef';

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});

/** The answer for one request, or undefined when it is not one of the book's own routes. */
export function rebuiltBookResponse(url: string, method: string, books: readonly FixtureBook[] = [{}]): Response | undefined {
  const path = new URL(url, 'http://fixture.invalid').pathname;
  for (const book of books) {
    const itemId = book.itemId ?? 'item', version = book.version ?? 'source', format = book.format ?? 'pdf';
    if (method === 'GET' && path === `/v1/items/${itemId}/book`) return json({itemId, version, versions: [{id: version, format}], format,
      fingerprint: fixtureFingerprint, size: book.size ?? 100, mediaUrl: `/v1/media/grant-${itemId}`});
    if (method === 'GET' && path === `/v1/items/${itemId}`) return json({id: itemId, kind: 'book', title: book.title ?? 'Stored title', state: {}});
    if (method === 'PUT' && path === `/v1/items/${itemId}/state`) return json({});
  }
  return undefined;
}

/** A fetch that answers only the books' own routes; anything else is a 404 the test can see in `calls`. */
export function rebuiltBookFetch(calls: string[], books?: readonly FixtureBook[]) {
  return async (url: RequestInfo | URL, init: RequestInit = {}) => {
    const method = init.method ?? 'GET';
    calls.push(method + ' ' + new URL(String(url), 'http://fixture.invalid').pathname);
    return rebuiltBookResponse(String(url), method, books) ?? json({error: {code: 'not_found', message: 'Not found.'}}, 404);
  };
}
