/** Empty media-server writes still use JSON: a simple cross-origin form must
 * never be sufficient to invoke a write. Upload bodies and explicit media types
 * retain their own encoding (including browser-generated multipart boundaries). */
export function jsonEmptyWrites(fetcher: typeof fetch): typeof fetch {
  return (input, init) => {
    const request = typeof Request !== 'undefined' && input instanceof Request ? input : undefined;
    const method = (init?.method ?? request?.method ?? 'GET').toUpperCase();
    const body = init?.body === undefined ? request?.body : init.body;
    if (!['POST', 'PUT', 'PATCH', 'DELETE'].includes(method) || body != null && body !== '') return fetcher(input, init);
    const headers = new Headers(init?.headers ?? request?.headers);
    if (headers.has('Content-Type')) return fetcher(input, init);
    headers.set('Content-Type', 'application/json');
    return fetcher(input, {...init, headers, body: '{}'});
  };
}
