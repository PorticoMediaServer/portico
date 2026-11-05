/** The legacy avatar envelope is internal to HttpLocalApi's JSON path. Select its
 * wire payload in core; the native executor receives only encoded image bytes.
 * Keep native URL, base64 and byte validation at the execution boundary. */
export function nativeAvatarWireBody(url: string, method: string, body: string | null, applePath: boolean, base64Wire = true): string | null {
  // Vega and older native providers retain their existing bridge representation.
  if (!base64Wire) return body;
  let path: string;
  try {
    path = new URL(url).pathname;
    if (applePath) path = decodeURIComponent(path);
  } catch {return body;}
  if (method !== 'POST' || !/^\/v1\/(direct\/profiles\/[A-Za-z0-9_-]{1,128}|admin\/access\/members\/[A-Za-z0-9_-]{1,128}\/profiles\/[A-Za-z0-9_-]{1,128})\/avatar$/.test(path)) return body;
  if (body === null || body.length > 5592448) return null;
  try {
    const envelope: unknown = JSON.parse(body);
    if (!envelope || typeof envelope !== 'object' || Array.isArray(envelope) || Object.keys(envelope).length !== 1 || !Object.hasOwn(envelope, 'nativeAvatarBase64')
      || typeof (envelope as {nativeAvatarBase64?: unknown}).nativeAvatarBase64 !== 'string') return null;
    return (envelope as {nativeAvatarBase64: string}).nativeAvatarBase64;
  } catch {return null;}
}
