/** The avatar and channel-logo envelopes are internal to HttpLocalApi's JSON path. Select their
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
  const channelLogo = /^\/v1\/admin\/library-channels\/[A-Za-z0-9_-]{1,128}\/logo$/.test(path);
  if (method !== 'PUT' || !channelLogo && !/^\/v1\/profiles\/[A-Za-z0-9_-]{1,128}\/avatar$/.test(path)) return body;
  const key = channelLogo ? 'nativeChannelLogoBase64' : 'nativeAvatarBase64';
  if (body === null || body.length > 5592448) return null;
  try {
    const envelope: unknown = JSON.parse(body);
    if (!envelope || typeof envelope !== 'object' || Array.isArray(envelope) || Object.keys(envelope).length !== 1 || !Object.hasOwn(envelope, key)
      || typeof (envelope as Record<string, unknown>)[key] !== 'string') return null;
    return (envelope as Record<string, string>)[key]!;
  } catch {return null;}
}
