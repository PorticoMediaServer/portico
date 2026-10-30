/** O-162: the native half of shared links. The link format, its builder and its parser are
 * client-core's (P2-61, O-161); this module never reads a destination itself. It only turns the
 * app route the native layer forwarded back into the one web URL it came from, and decides what
 * opening it means on this device. Pure: no React Native, network or storage. */

/** The only host a shared link opens the app from (local-account servers open in the browser). */
export const SHARE_ORIGIN = 'https://web.getportico.tv';

const SEGMENT = /^[A-Za-z0-9._~%-]{1,512}$/, QUERY = /^[A-Za-z0-9._~%&=+-]{1,1024}$/;
const FORBIDDEN = /%(2[fF]|3[fF]|23|[01][0-9a-fA-F]|7[fF])/;

/** `portico://shared/{serverId}/{segment}…[?query]` (what Android and Apple forward) back to
 * `https://web.getportico.tv/s/{serverId}/{segment}…[?query]`, or undefined. The same grammar the
 * native allow-lists accept: 1–6 encoded segments, no dot segment, no encoded slash, `?`, `#` or
 * control character, and no fragment. client-core's parser then decides what it names. */
export function sharedRouteToWebUrl(raw: unknown): string | undefined {
  if (typeof raw !== 'string' || !raw.startsWith('portico://shared/') || raw.includes('#')) return undefined;
  const rest = raw.slice('portico://shared/'.length), at = rest.indexOf('?');
  const path = at < 0 ? rest : rest.slice(0, at), query = at < 0 ? undefined : rest.slice(at + 1);
  const segments = path.split('/');
  if (segments.length < 1 || segments.length > 6 || !segments.every(segment => SEGMENT.test(segment) && !FORBIDDEN.test(segment) && segment !== '.' && segment !== '..')) return undefined;
  if (query !== undefined && (!QUERY.test(query) || FORBIDDEN.test(query))) return undefined;
  return `${SHARE_ORIGIN}/s/${segments.join('/')}${query ? `?${query}` : ''}`;
}

/** What client-core's parser returns for a valid share (P2-61 owns the destination union). */
export type ParsedShare<D> = Readonly<{serverId: string; destination: D}>;
export type ShareParser<D> = (url: string) => ParsedShare<D> | undefined;

/** The facts this device has when a link arrives. */
export type ShareContext = Readonly<{
  /** The server whose shell is open and ready (signed in, profile chosen), if any. */
  currentServerId?: string;
  /** Servers this device holds a sign-in for. */
  savedServerIds: readonly string[];
  /** Whether the device can reach the network at all. */
  online: boolean;
}>;

export type ShareDecision<D> =
  /** The shell for that server is open: push the destination over that server's Home. */
  | Readonly<{kind: 'open'; serverId: string; destination: D}>
  /** Another server this device is signed in to: switch, then open once its shell is ready. */
  | Readonly<{kind: 'switch'; serverId: string; destination: D}>
  /** Nothing is signed in yet: sign in and choose a profile, then decide again. */
  | Readonly<{kind: 'sign-in'; serverId: string; destination: D}>
  /** A short message on the standard backdrop, with a way Home. */
  | Readonly<{kind: 'unavailable'; reason: 'not-a-share' | 'no-access' | 'offline'}>;

/** One decision for cold starts, warm opens and signed-out resumes alike. */
export function decideShareLink<D>(webUrl: string | undefined, parse: ShareParser<D>, context: ShareContext): ShareDecision<D> {
  const parsed = webUrl === undefined ? undefined : parse(webUrl);
  if (!parsed) return {kind: 'unavailable', reason: 'not-a-share'};
  const {serverId, destination} = parsed;
  if (context.currentServerId === serverId) return {kind: 'open', serverId, destination};
  if (context.savedServerIds.includes(serverId)) return context.online ? {kind: 'switch', serverId, destination} : {kind: 'unavailable', reason: 'offline'};
  // Signed out entirely: the sign-in decides which servers this person can reach, then we decide again.
  if (context.currentServerId === undefined && context.savedServerIds.length === 0) return {kind: 'sign-in', serverId, destination};
  return {kind: 'unavailable', reason: 'no-access'};
}
