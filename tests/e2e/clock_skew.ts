// "No client-clock requirements" end to end (planning/DECISIONS.md). Run under the disposable
// fixture, against a server built from the same commit:
//
//   python3 tests/e2e/server.py --binary <devtrust-server> --media-cache <dir> -- \
//     node --experimental-strip-types tests/e2e/clock_skew.ts
//
// This client's whole clock (`Date.now()` and `new Date()`) is moved a year ahead, then a year
// behind, while the server keeps real time. Each run must connect (nonce identity proof), sign in
// with a password, renew its sign-in through the stored-session renewal, stay signed in, start a
// playback session and read its media.
import {readFileSync} from 'node:fs';
import {routePath} from '../../packages/contracts/src/route-paths.ts';
import {randomBytes} from 'node:crypto';
import {HttpLocalApi, type LocalSession} from '../../packages/client-core/src/index.ts';
import {probeRoute} from '../../packages/client-core/src/route-identity.ts';
import {RouteConnection} from '../../packages/client-core/src/route-connection.ts';
import {rememberNativeSession, refreshStoredSession, type RememberedServer, type ServerConnectionStorage} from '../../packages/client-core/src/server-connections.ts';
import {setInstallationIdentity} from '../../packages/client-core/src/installation.ts';
import {directSignIn, selectDirectProfile} from '../../packages/client-core/src/profile-management.ts';
import {SessionsClient} from '../../packages/client-core/src/playback-v1/sessions.ts';
import {androidDeclaredProfile} from '../../packages/client-core/src/client-profile.ts';
import {resetServerClocks, serverClockOffset} from '../../packages/client-core/src/server-clock.ts';

const base = process.env.PORTICO_E2E_SERVER_URL!, credentialsFile = process.env.PORTICO_E2E_CREDENTIALS_FILE!;
if (!base || !credentialsFile) throw new Error('Run under tests/e2e/server.py.');
const member = JSON.parse(readFileSync(credentialsFile, 'utf8')).member as {username: string; password: string};
const YEAR = 365 * 86400000;
const RealDate = Date, realNow = Date.now.bind(Date);

/** Move this whole process's wall clock by `offset`; the server is untouched. */
function shiftClock(offset: number) {
  class ShiftedDate extends RealDate {
    constructor(...args: unknown[]) {
      if (args.length === 0) super(realNow() + offset);
      else super(...(args as [string | number | Date]));
    }
    static override now() { return realNow() + offset; }
  }
  globalThis.Date = ShiftedDate as DateConstructor;
}

function memoryStorage(): ServerConnectionStorage {
  let value: RememberedServer | undefined;
  return {read: async () => structuredClone(value), change: async update => {value = structuredClone(update(structuredClone(value)));}};
}

async function firstPlayableItem(api: HttpLocalApi): Promise<string> {
  const libraries = await api.request<{items: {id: string; kind: string}[]}>('/v1/libraries');
  for (const library of libraries.items.filter(l => l.kind === 'movie' || l.kind === 'tv')) {
    const found = await api.request<{groups?: {items?: {id: string; kind: string}[]}[]}>(`/v1/search?q=${encodeURIComponent('e')}`).catch(() => undefined);
    const item = found?.groups?.flatMap(g => g.items ?? []).find(i => i.kind === 'movie' || i.kind === 'episode');
    if (item) return item.id;
    void library;
  }
  throw new Error('No playable movie or episode in the fixture library.');
}

async function run(offset: number) {
  resetServerClocks();
  shiftClock(offset);
  const label = offset === 0 ? 'no shift' : `${offset > 0 ? '+' : '-'}${Math.round(Math.abs(offset) / YEAR)} year`;
  try {
    // 1. Connect: the identity proof carries no times, only this client's fresh nonce.
    const pin = await probeRoute(base);
    const connection = new RouteConnection({pin, logicalOrigin: base, memory: {paired: [base]}});
    await connection.resolve();
    const api = new HttpLocalApi(base, '');
    api.useRoutes(connection);

    // 2. Sign in with a password, as an installed app (so the server issues a refresh credential).
    const installationId = randomBytes(32).toString('base64url');
    setInstallationIdentity({installationId, name: 'Clock skew e2e', platform: 'test', app: 'portico-e2e', appVersion: '1'});
    const signedIn = await directSignIn(api, member.username, member.password);
    if (signedIn.kind !== 'signed-in') throw new Error(`sign-in did not finish (${signedIn.kind})`);
    let session: LocalSession = signedIn.session;
    api.setAccessToken(session.accessToken);
    if (session.viewer.role === 'account') {
      // An account sign-in opens a profile next, as the apps do.
      const profile = signedIn.snapshot.profiles.find(p => !p.pinRequired) ?? signedIn.snapshot.profiles[0]!;
      session = (await selectDirectProfile(api, {authority: 'local', accountId: session.viewer.accountId, serverId: session.viewer.serverId}, profile.id, {installationId: installationId})).session;
      api.setAccessToken(session.accessToken);
    }
    if (process.env.CLOCK_DEBUG) console.log('viewer', JSON.stringify(session.viewer), 'me before renewal:', await api.request('/v1/me').then(() => 'ok', (e: {status?: number; code?: string}) => `${e.status} ${e.code}`));
    const storage = memoryStorage();
    await rememberNativeSession(api, session, {storage});

    // 3. Renew through the stored-session renewal, as if the access token had just been refused.
    const renewed = await refreshStoredSession({env: {storage}, api, family: {sessionFamilyId: session.sessionFamilyId, serverId: pin.serverId}, rejected: session.accessToken});
    if (BigInt(renewed.tokenGeneration) <= BigInt(session.tokenGeneration)) throw new Error('renewal did not advance the token generation');
    api.setAccessToken(renewed.accessToken);

    // 4. Still signed in on the renewed credential.
    await api.request('/v1/me');

    // 5. Play: start a playback session and read the first bytes of its media.
    const itemId = await firstPlayableItem(api);
    const http = {send: (r: {method: string; path: string; headers?: Record<string, string>; body?: unknown; signal?: AbortSignal}) =>
      api.requestRaw(r.path, r.method, {headers: r.headers, body: r.body, signal: r.signal})};
    // A declared Android TV profile, published the way the apps publish theirs.
    const published = await api.requestRaw(routePath('put_device_capabilities'), 'PUT', {body: androidDeclaredProfile({television: true})});
    if (published.status >= 300) throw new Error(`capabilities answered ${published.status} ${JSON.stringify(published.body).slice(0, 200)}`);
    const client = new SessionsClient(http as never);
    const started = await client.start({itemId}, {startFrom: 'beginning'}, randomBytes(16).toString('hex')) as unknown as {id: string; presentation?: {url?: string; mode?: string}};
    const url = started.presentation?.url;
    if (!url) throw new Error('the playback session has no media URL');
    const media = await connection.fetch(new URL(url, base).toString(), {headers: {Authorization: `Bearer ${renewed.accessToken}`, Range: 'bytes=0-1023'}});
    if (!media.ok) throw new Error(`media answered ${media.status}`);
    const bytes = (await media.arrayBuffer()).byteLength;
    if (!bytes) throw new Error('media was empty');
    await client.stop(started.id).catch(() => {});
    connection.dispose();
    console.log(`PASS ${label}: connected, signed in, renewed (generation ${session.tokenGeneration} -> ${renewed.tokenGeneration}), played ${bytes} bytes (${started.presentation?.mode}); learned offset ${Math.round(serverClockOffset() / 86400000)} days`);
  } finally {
    globalThis.Date = RealDate;
    setInstallationIdentity(undefined);
  }
}

let failed = false;
for (const offset of (process.env.CLOCK_OFFSETS ? process.env.CLOCK_OFFSETS.split(",").map(Number) : [YEAR, -YEAR])) {
  try { await run(offset); } catch (error) { failed = true; console.error(`FAIL ${offset === 0 ? 'no shift' : (offset > 0 ? '+' : '-') + '1 year'}:`, error); }
}
process.exit(failed ? 1 : 0);
