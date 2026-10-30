/**
 * Parser probe for every client-core read (APL-SYS-12, MU10).
 *
 * Calls every client-core read function the apps use, against a real server,
 * through the same parsers/services, and reports every parse failure with the
 * request and a response excerpt. O4 runs it against the runner e2e server;
 * this tool never contacts a server on its own.
 *
 *   node --experimental-strip-types packages/client-core/tools/parser-probe.ts
 *
 * Env:
 *   PROBE_ORIGIN        e.g. http://127.0.0.1:19731 (required)
 *   PROBE_SESSION_FILE  JSON file with an `accessToken` (and `viewer.serverId`) (required)
 *   PROBE_OUT           JSON report path (optional; default stdout summary only)
 *   PROBE_LIMIT         entries sampled per list (optional; default 5)
 *
 * Read-only: only GET, plus POST to the browse-query read route. Exit code 0
 * always once the walk completes (it is a report, not a gate). The access
 * token is read from the session file, sent as a bearer, and never printed
 * or written anywhere.
 */
import {readFileSync, writeFileSync} from 'node:fs';
import {pathToFileURL} from 'node:url';
import {HttpLocalApi} from '../src/index.ts';
import {compatContentApi} from '../src/presentation/content-compat.ts';
import {viewerScope} from '../src/presentation/content.ts';
import {fetchHome, fetchHomeLayout, fetchHomeRow} from '../src/home.ts';
import {fetchItemRecommendations, fetchSuggestions} from '../src/recommendations.ts';
import {LibraryContentService, type ContentProjection, type ContentRoute, type ContentView} from '../src/library-content.ts';
import {BrowseQueryBuilder, parseBrowseCapabilities, parseBrowseFacets, parseBrowseResult, type BrowseCapabilities} from '../src/browse.ts';
import {fetchInventoryStatus} from '../src/library-inventory.ts';
import {DetailService} from '../src/detail.ts';
import {ShowWorkspaceService} from '../src/show-workspace.ts';
import {readPerson} from '../src/people.ts';
import {SavedService, type SavedProjection} from '../src/saved.ts';
import {PersonalSavedService} from '../src/personal-saved.ts';
import {readLibraryChannels, readLibraryTemplates} from '../src/library-channels.ts';
import {ChannelGuideService} from '../src/channel-guide.ts';
import {DVRClient} from '../src/dvr.ts';
import {InboxService} from '../src/inbox.ts';
import {parseFeedbackCapabilities} from '../src/notifications.ts';
import {DownloadsService} from '../src/downloads-service.ts';
import {parseDownloadUsage} from '../src/downloads.ts';
import {GroupSessionService} from '../src/group-session.ts';
import {ConsoleClient} from '../src/console.ts';
import {parseAccessDevice, parseAccessMember, parseInvitation, parseLogPage, parseLogSettings} from '../src/server-administration.ts';
import {parseApiCredential} from '../src/api-access.ts';
import {parseBackupsDocument, parseDVRDocument, parseEnvelope, parseLibraryDocument, parseLiveDefaultsDocument, parseStorageReport, parseTunerView, parseUpdateReport} from '../src/administration.ts';
import {PreferencesReader} from '../src/preferences-reader.ts';
import {IdentityClient} from '../src/identity-client.ts';
import {PlaybackOptionsClient} from '../src/playback-v1/options.ts';
import {localV1Http} from '../src/playback-v1/local-http.ts';
import {AdminSessionsClient} from '../src/playback-v1/admin-sessions.ts';
import {parseListeningPreferences, readListeningContext, readListeningItem} from '../src/listening.ts';
import {SearchService} from '../src/search.ts';
import {getContainerState} from '../src/container-state.ts';
import {loadRemoteAccess} from '../src/remote-access.ts';

export type ProbeCall = {
  area: string;
  name: string;
  method: string;
  path: string;
  status: number;
  ok: boolean;
  error?: {code: string; message: string};
  /** First 600 chars of the response body, only when the HTTP status was 2xx but parsing failed. */
  excerpt?: string;
};
export type ProbeReport = {
  origin: string;
  serverId: string;
  role: string;
  limit: number;
  startedAt: string;
  finishedAt: string;
  counts: Record<string, {ok: number; failed: number}>;
  failures: ProbeCall[];
  calls: ProbeCall[];
  notProbed: {name: string; reason: string}[];
};

export type ProbeInput = {
  origin: string;
  serverId: string;
  viewerId: string;
  authority: string;
  accountId: string;
  profileId: string;
  role: string;
  limit: number;
  /** Last response status + body per `METHOD path`, filled by the recording fetch. */
  seen: Map<string, {status: number; body: string}>;
  /** The bearer, kept only to scrub it out of anything recorded. Never reported. */
  token: string;
};

const BODY_KEEP = 64 * 1024;
const EXCERPT_KEEP = 600;
const MAX_LIBRARIES = 8;

/** A `fetch` wrapper that remembers the last response body per request (bounded), so a parse failure can print its excerpt. */
export function makeRecordingFetch(inner: typeof fetch, seen: Map<string, {status: number; body: string}>): typeof fetch {
  return (async (input: unknown, init?: unknown): Promise<Response> => {
    const response = await (inner as (i: unknown, n?: unknown) => Promise<Response>)(input, init);
    try {
      const request = input as {url?: unknown; method?: unknown};
      const url = typeof input === 'string' ? input : typeof request?.url === 'string' ? request.url : '';
      const initMethod = (init as {method?: unknown} | undefined)?.method;
      const method = typeof initMethod === 'string' ? initMethod : typeof request?.method === 'string' ? request.method : 'GET';
      const text = await response.clone().text();
      const key = `${method.toUpperCase()} ${url.split('?')[0].replace(/^https?:\/\/[^/]+/, '')}${url.includes('?') ? '?' + url.split('?').slice(1).join('?') : ''}`;
      seen.set(key, {status: response.status, body: text.slice(0, BODY_KEEP)});
      while (seen.size > 400) {
        const oldest = seen.keys().next();
        if (oldest.done) break;
        seen.delete(oldest.value);
      }
    } catch {
      /* A body that cannot be reread is not probe evidence. */
    }
    return response;
  }) as typeof fetch;
}

function seenKey(method: string, path: string): string {
  return `${method.toUpperCase()} ${path}`;
}

class Recorder {
  calls: ProbeCall[] = [];
  notProbed: {name: string; reason: string}[] = [];
  private input: ProbeInput;
  constructor(input: ProbeInput) {
    this.input = input;
  }
  private scrub(value: string): string {
    const token = this.input.token;
    if (!token || value.length < token.length) return value;
    return value.split(token).join('[redacted]');
  }
  private lookup(method: string, path: string): {status: number; body: string} | undefined {
    return this.input.seen.get(seenKey(method, path));
  }
  pass(area: string, name: string, method: string, path: string): void {
    const status = this.lookup(method, path)?.status ?? 0;
    this.calls.push({area, name, method, path, status, ok: true});
  }
  fail(area: string, name: string, method: string, path: string, error: unknown): void {
    const seen = this.lookup(method, path);
    const holder = error as {code?: unknown; status?: unknown; message?: unknown} | null;
    const code = typeof holder?.code === 'string' && holder.code ? holder.code : 'request_failed';
    const status = typeof holder?.status === 'number' ? holder.status : seen?.status ?? 0;
    const message = this.scrub(String((error as Error | null)?.message ?? error ?? 'request failed')).slice(0, 300);
    const call: ProbeCall = {area, name, method, path, status, ok: false, error: {code, message}};
    // An excerpt is evidence of a parse failure: the server answered 2xx but
    // the client-core parser refused the body.
    if (seen && seen.status >= 200 && seen.status < 300 && seen.body) {
      call.excerpt = this.scrub(seen.body).slice(0, EXCERPT_KEEP);
    }
    this.calls.push(call);
  }
  /** Run one read through its client-core parser/service; catch every error per call and continue. */
  async check(area: string, name: string, method: string, path: string, run: () => Promise<unknown>): Promise<unknown> {
    try {
      const value = await run();
      this.pass(area, name, method, path);
      return value;
    } catch (error) {
      this.fail(area, name, method, path, error);
      return undefined;
    }
  }
  skip(name: string, reason: string): void {
    this.notProbed.push({name, reason});
  }
}

const requestId = async (): Promise<string> => globalThis.crypto.randomUUID();

function contentPath(libraryId: string, route: ContentRoute, limit: number): string {
  const query = new URLSearchParams({view: route.view, limit: String(limit)});
  for (const field of ['entityId', 'sort', 'direction', 'category', 'q'] as const) {
    const value = (route as Record<string, string | undefined>)[field];
    if (value !== undefined) query.set(field, value);
  }
  return `/v1/libraries/${encodeURIComponent(libraryId)}/content?${query}`;
}

const ENTITY_VIEWS = new Set(['show', 'season', 'artist', 'album', 'book', 'collection', 'author', 'book_series', 'disc']);

type CollectedEntry = {libraryId: string; view: string; id: string; kind: string; navigationView?: string; navigationEntity?: string; itemId?: string};

export async function walkProbe(api: HttpLocalApi, input: ProbeInput): Promise<ProbeReport> {
  const startedAt = new Date().toISOString();
  const rec = new Recorder(input);
  const limit = input.limit;
  const contentApi = compatContentApi(api);
  let serverId = input.serverId;
  let viewerId = input.viewerId;
  let authority = input.authority;
  let accountId = input.accountId;
  let profileId = input.profileId;
  let role = input.role;
  const scopeOf = () => ({serverId, viewerId});

  // 1. System and identity.
  const system = await rec.check('system', 'system', 'GET', '/v1/system', () => api.system()) as {id?: unknown} | undefined;
  if (system && typeof system.id === 'string' && system.id) serverId = system.id;
  const me = await rec.check('system', 'me', 'GET', '/v1/me', () => api.me()) as {viewer?: {authority?: unknown; accountId?: unknown; profileId?: unknown; serverId?: unknown; role?: unknown}} | undefined;
  if (me?.viewer) {
    const viewer = me.viewer;
    if (typeof viewer.authority === 'string') authority = viewer.authority;
    if (typeof viewer.accountId === 'string') accountId = viewer.accountId;
    if (typeof viewer.profileId === 'string') profileId = viewer.profileId;
    if (typeof viewer.role === 'string') role = viewer.role;
    if (typeof viewer.serverId === 'string' && viewer.serverId) serverId = viewer.serverId;
    viewerId = viewerScope({viewer: {authority, accountId, profileId, serverId}}, {baseUrl: input.origin}).viewerId;
  }
  await rec.check('system', 'capabilities', 'GET', '/v1/capabilities', () => api.request<unknown>('/v1/capabilities'));
  rec.skip('capabilities body', 'no client-core parser: features.playback_v1 is read inline in playback-v1/v1-queue-player.ts');
  await rec.check('system', 'server preferences', 'GET', '/v1/preferences?deviceClass=web', () =>
    new PreferencesReader(api, scopeOf()).preferences('web'));
  const identity = new IdentityClient(api);
  await rec.check('system', 'rating systems', 'GET', '/v1/rating-systems', () => identity.ratingSystems());
  if (profileId) {
    await rec.check('system', 'profile restrictions', 'GET', `/v1/direct/profiles/${encodeURIComponent(profileId)}/restrictions`, () =>
      identity.restrictions(profileId));
  }
  await rec.check('system', 'profile avatars', 'GET', '/v1/direct/profiles/avatars', () => identity.avatars());
  await rec.check('system', 'two-factor state', 'GET', '/v1/direct/two-factor', () => identity.twoFactor());
  await rec.check('system', 'PIN recovery methods', 'GET', '/v1/direct/pin-recovery', () => identity.pinRecovery());
  rec.skip('devices / remembered accounts', 'need a device installationId the probe does not have (identity-client.ts)');
  const isOwner = role === 'owner';

  // 2. Home.
  const home = await rec.check('home', 'home document', 'GET', '/v1/home?limit=12', () => fetchHome(api, 12)) as
    {rows?: {id: string}[]} | undefined;
  await rec.check('home', 'home layout', 'GET', '/v1/home/layout', () => fetchHomeLayout(api));
  if (home && Array.isArray(home.rows)) {
    for (const row of home.rows) {
      await rec.check('home', `home row ${row.id}`, 'GET', `/v1/home/rows/${encodeURIComponent(row.id)}`, () =>
        fetchHomeRow(api, row.id, {}));
    }
  }
  await rec.check('home', 'suggestions', 'GET', '/v1/suggestions?limit=12', () => fetchSuggestions(api, 12));

  // 3. Libraries.
  const libraries = await rec.check('libraries', 'library list', 'GET', '/v1/libraries', () => api.libraries()) as
    {items?: {id: string; name?: string; kind?: string}[]} | undefined;
  const libs = Array.isArray(libraries?.items) ? libraries.items.slice(0, MAX_LIBRARIES) : [];
  const collected: CollectedEntry[] = [];
  const contentSvc = new LibraryContentService({api: contentApi, scope: scopeOf(), pageSize: 40});
  async function selectContent(area: string, name: string, libraryId: string, route: ContentRoute): Promise<ContentProjection | undefined> {
    const path = route.view === 'home' ? `/v1/content?view=home&limit=12` : contentPath(libraryId, route, 40);
    try {
      await contentSvc.select(route);
    } catch (error) {
      // A route the service refuses before any request (e.g. an entity view
      // without an entity) is a probe bug, not server evidence; skip it.
      rec.skip(`${name} (${path})`, `invalid content route: ${error instanceof Error ? error.message : String(error)}`);
      return undefined;
    }
    const snapshot = contentSvc.getSnapshot();
    if (snapshot.phase === 'error') {
      rec.fail(area, name, 'GET', path, Object.assign(new Error(snapshot.error?.message ?? 'content failed'), {code: snapshot.error?.code ?? 'request_failed'}));
      return undefined;
    }
    rec.pass(area, name, 'GET', path);
    return snapshot.projection ?? undefined;
  }
  for (const lib of libs) {
    const discover = await selectContent('libraries', `content discover (${lib.id})`, lib.id, {libraryId: lib.id, view: 'discover'});
    const projections: ContentProjection[] = [];
    if (discover) projections.push(discover);
    if (discover) {
      for (const nav of discover.navigation) {
        if (ENTITY_VIEWS.has(nav.view)) continue; // Entity views need an entityId; probed per entry below.
        if (nav.view === 'discover') continue;
        const projection = await selectContent('libraries', `content ${nav.view} (${lib.id})`, lib.id, {libraryId: lib.id, view: nav.view});
        if (projection) projections.push(projection);
      }
    }
    for (const projection of projections) {
      let taken = 0;
      for (const section of projection.sections) {
        for (const entry of section.entries) {
          if (taken >= limit) break;
          taken++;
          collected.push({
            libraryId: lib.id,
            view: projection.scope.view,
            id: entry.id,
            kind: entry.kind,
            navigationView: entry.navigation?.view,
            navigationEntity: entry.navigation?.entityId,
            itemId: entry.playback?.itemId ?? (entry.navigation?.view === 'item' ? entry.navigation.entityId ?? entry.id : undefined),
          });
        }
        if (taken >= limit) break;
      }
    }
    // Browse: the capability document, then one page through the same builder the apps use.
    const capabilitiesPath = `/v1/libraries/${encodeURIComponent(lib.id)}/browse-capabilities`;
    const capabilitiesRaw = await rec.check('libraries', `browse capabilities (${lib.id})`, 'GET', capabilitiesPath, () =>
      api.request<unknown>(capabilitiesPath).then(parseBrowseCapabilities));
    if (capabilitiesRaw && typeof capabilitiesRaw === 'object') {
      const caps = capabilitiesRaw as {pivots?: {id: string; browsable?: boolean}[]; fields?: {id: string}[]; queryLimits?: {maximumLimit?: number}};
      const pivot = caps.pivots?.find(p => p.browsable);
      if (pivot) {
        const browsePath = `/v1/libraries/${encodeURIComponent(lib.id)}/browse`;
        await rec.check('libraries', `browse page (${lib.id})`, 'POST', browsePath, async () => {
          const builder = new BrowseQueryBuilder(capabilitiesRaw as BrowseCapabilities);
          const maxLimit = caps.queryLimits?.maximumLimit ?? 60;
          const body = builder.limit(Math.max(1, Math.min(limit, maxLimit))).build();
          return api.request<unknown>(browsePath, 'POST', body).then(parseBrowseResult);
        });
      } else {
        rec.skip(`browse page (${lib.id})`, 'capabilities publish no browsable pivot (browse.ts)');
      }
      // The filter panels never ask facets for title or entityKind (not counted facets); probe the first field they do.
      const field = caps.fields?.find(f => f.id !== 'title' && f.id !== 'entityKind')?.id;
      if (field) {
        const facetsPath = `/v1/libraries/${encodeURIComponent(lib.id)}/facets?${new URLSearchParams({field, limit: '20'})}`;
        await rec.check('libraries', `browse facets (${lib.id})`, 'GET', facetsPath, () =>
          api.request<unknown>(facetsPath).then(raw => parseBrowseFacets(raw, field)));
      }
    }
    await rec.check('libraries', `inventory status (${lib.id})`, 'GET', `/v1/libraries/${encodeURIComponent(lib.id)}/inventory-status`, () =>
      fetchInventoryStatus(contentApi, serverId, lib.id, new AbortController().signal));
  }
  // Search uses the same envelope parsers through its own service.
  try {
    const searchSvc = new SearchService({api: contentApi, scope: scopeOf(), pageSize: 20});
    const searchPath = '/v1/search?q=star&limit=20';
    await searchSvc.select('star');
    const snapshot = searchSvc.getSnapshot();
    if (snapshot.phase === 'error') {
      rec.fail('libraries', 'search', 'GET', searchPath, Object.assign(new Error(snapshot.error?.message ?? 'search failed'), {code: snapshot.error?.code ?? 'request_failed'}));
    } else {
      rec.pass('libraries', 'search', 'GET', searchPath);
    }
    searchSvc.dispose();
  } catch (error) {
    rec.fail('libraries', 'search', 'GET', '/v1/search?q=star&limit=20', error);
  }

  // 4. Entities.
  const detailSvc = new DetailService({api, scope: scopeOf(), requestId});
  const personIds = new Set<string>();
  const showTargets: {libraryId: string; showId: string}[] = [];
  const playable: {libraryId: string; itemId: string}[] = [];
  for (const entry of collected) {
    if (!entry.itemId) {
      if (entry.navigationView && ENTITY_VIEWS.has(entry.navigationView)) {
        await selectContent('entities', `entity page ${entry.navigationView} (${entry.navigationEntity ?? entry.id})`, entry.libraryId, {
          libraryId: entry.libraryId, view: entry.navigationView as ContentView, entityId: entry.navigationEntity ?? entry.id,
        });
      }
      if (entry.kind === 'show') showTargets.push({libraryId: entry.libraryId, showId: entry.id});
      continue;
    }
    const itemId: string = entry.itemId;
    const detailPath = `/v1/items/${encodeURIComponent(itemId)}/detail?related=all`;
    try {
      await detailSvc.select({libraryId: entry.libraryId, itemId});
    } catch (error) {
      rec.fail('entities', `item detail (${itemId})`, 'GET', detailPath, error);
      continue;
    }
    const snapshot = detailSvc.getSnapshot();
    if (snapshot.phase === 'error') {
      rec.fail('entities', `item detail (${itemId})`, 'GET', detailPath,
        Object.assign(new Error(snapshot.error?.message ?? 'detail failed'), {code: snapshot.error?.code ?? 'request_failed'}));
      continue;
    }
    rec.pass('entities', `item detail (${itemId})`, 'GET', detailPath);
    const data = snapshot.data;
    if (data) {
      for (const credit of data.metadata.credits) {
        if (credit.personId) personIds.add(credit.personId);
      }
    }
    playable.push({libraryId: entry.libraryId, itemId});
    await rec.check('entities', `recommendations (${itemId})`, 'GET', `/v1/items/${encodeURIComponent(itemId)}/recommendations?limit=${limit}`, () =>
      fetchItemRecommendations(api, itemId, limit));
    if (entry.kind === 'show') showTargets.push({libraryId: entry.libraryId, showId: itemId});
  }
  const showSvc = new ShowWorkspaceService({api: contentApi, scope: scopeOf()});
  for (const target of showTargets.slice(0, limit)) {
    const path = `/v1/libraries/${encodeURIComponent(target.libraryId)}/show-workspace?showId=${encodeURIComponent(target.showId)}`;
    try {
      await showSvc.select({libraryId: target.libraryId, showId: target.showId});
    } catch (error) {
      rec.fail('entities', `show workspace (${target.showId})`, 'GET', path, error);
      continue;
    }
    const snapshot = showSvc.getSnapshot();
    if (snapshot.phase === 'error') {
      rec.fail('entities', `show workspace (${target.showId})`, 'GET', path,
        Object.assign(new Error(snapshot.error?.message ?? 'show workspace failed'), {code: snapshot.error?.code ?? 'request_failed'}));
    } else {
      rec.pass('entities', `show workspace (${target.showId})`, 'GET', path);
    }
  }
  showSvc.dispose();
  for (const personId of [...personIds].slice(0, 10)) {
    await rec.check('entities', `person (${personId})`, 'GET', `/v1/people/${encodeURIComponent(personId)}?limit=10`, () =>
      readPerson(contentApi, scopeOf(), personId, {limit: 10}));
  }
  // Listening: one item context plus the preferences document through its parser.
  const queueScope = {serverId, authority: authority as 'local' | 'hosted', accountId, profileId, controllerId: 'parser-probe', controllerEpoch: String(Date.now()), commandLaneId: 'parser-probe'};
  const listenReq = (path: string, body?: unknown, method: 'GET' | 'POST' | 'PUT' = 'GET', signal?: AbortSignal): Promise<unknown> =>
    api.request<unknown>(path, method, body, signal);
  const audioItem = playable[0];
  if (audioItem && (authority === 'local' || authority === 'hosted')) {
    await rec.check('entities', `listening item (${audioItem.itemId})`, 'GET', `/v1/items/${encodeURIComponent(audioItem.itemId)}/listening`, () =>
      readListeningItem(listenReq, queueScope, audioItem.itemId));
    await rec.check('entities', `listening context (${audioItem.itemId})`, 'GET', `/v1/items/${encodeURIComponent(audioItem.itemId)}/listening`, () =>
      readListeningContext(listenReq, scopeOf(), audioItem.itemId));
  } else {
    rec.skip('listening item/context', 'no playable entry sampled, or the viewer authority is neither local nor hosted');
  }
  await rec.check('entities', 'listening preferences', 'GET', '/v1/listening/preferences', () =>
    // The apps read `data` after checking the viewer scope (listeningData); the probe unwraps it.
    api.request<unknown>('/v1/listening/preferences').then(raw => parseListeningPreferences((raw as {data?: unknown}).data)));
  rec.skip('listening selection pages', 'loadListeningSelection pages a whole selection (listening.ts); unbounded for large libraries');
  // Playback options for the first playable item of each library.
  const optionsClient = new PlaybackOptionsClient(localV1Http(api));
  const seenLibs = new Set<string>();
  for (const item of playable) {
    if (seenLibs.has(item.libraryId)) continue;
    seenLibs.add(item.libraryId);
    await rec.check('entities', `playback options (${item.itemId})`, 'GET', `/v1/items/${encodeURIComponent(item.itemId)}/playback-options`, () =>
      optionsClient.options(item.itemId));
    if (seenLibs.size >= MAX_LIBRARIES) break;
  }
  rec.skip('chapters', 'PlayerChaptersService needs a playback session id, and creating one is a write (player-chapters.ts)');
  const firstShow = showTargets[0];
  if (firstShow) {
    await rec.check('entities', `container state (${firstShow.showId})`, 'GET', `/v1/containers/show/${encodeURIComponent(firstShow.showId)}/personal-state`, () =>
      getContainerState(api, 'show', firstShow.showId));
  }

  // 5. Saved.
  const savedSvc = new SavedService({api, scope: scopeOf(), requestId});
  async function selectSaved(name: string, route: Parameters<SavedService['select']>[0]): Promise<SavedProjection | undefined> {
    const path = route.view === 'playlist'
      ? `/v1/playlists/${encodeURIComponent((route as {playlistId: string}).playlistId)}/content?limit=40`
      : `/v1/content?limit=40&view=${route.view}`;
    try {
      await savedSvc.select(route);
    } catch (error) {
      rec.fail('saved', name, 'GET', path, error);
      return undefined;
    }
    const snapshot = savedSvc.getSnapshot();
    if (snapshot.phase === 'error') {
      rec.fail('saved', name, 'GET', path,
        Object.assign(new Error(snapshot.error?.message ?? 'saved failed'), {code: snapshot.error?.code ?? 'request_failed'}));
      return undefined;
    }
    rec.pass('saved', name, 'GET', path);
    return snapshot.projection ?? undefined;
  }
  await selectSaved('watchlist', {view: 'watchlist'});
  await selectSaved('favorites', {view: 'favorites'});
  const playlists = await selectSaved('playlists', {view: 'playlists'});
  if (playlists) {
    const card = playlists.sections.flatMap(section => section.entries)
      .find(entry => entry.kind === 'playlist');
    const playlistId = card && card.kind === 'playlist' ? card.navigation.entityId : undefined;
    if (playlistId) {
      await selectSaved(`playlist (${playlistId})`, {view: 'playlist', playlistId});
    } else {
      rec.skip('one playlist page', 'the playlists view returned no playlist card');
    }
  }
  savedSvc.dispose();
  const personalSvc = new PersonalSavedService(api, scopeOf(), requestId);
  for (const view of ['collections', 'views', 'history'] as const) {
    const path = view === 'history' ? '/v1/personal-history?limit=40' : `/v1/saved-resources?limit=40&kind=${view === 'collections' ? 'collection' : 'view'}`;
    try {
      await personalSvc.select({view});
    } catch (error) {
      rec.fail('saved', `personal ${view}`, 'GET', path, error);
      continue;
    }
    const snapshot = personalSvc.getSnapshot();
    if (snapshot.error) {
      const info = snapshot.error as {code?: unknown; message?: unknown};
      rec.fail('saved', `personal ${view}`, 'GET', path,
        Object.assign(new Error(typeof info.message === 'string' ? info.message : 'saved failed'), {code: typeof info.code === 'string' ? info.code : 'request_failed'}));
    } else {
      rec.pass('saved', `personal ${view}`, 'GET', path);
    }
  }
  await rec.check('saved', 'saved libraries', 'GET', '/v1/libraries', () => personalSvc.loadLibraries());
  personalSvc.dispose();

  // 6. Live.
  await rec.check('live', 'live sources', 'GET', '/v1/admin/live-sources', () => api.request<unknown>('/v1/admin/live-sources'));
  rec.skip('live source documents', 'no client-core parser: SourceDocument is read inline in web LiveSourceExtras.tsx');
  const windowStart = new Date(Math.floor(Date.now() / 3600000) * 3600000);
  const windowEnd = new Date(windowStart.getTime() + 3 * 3600000);
  for (const kind of ['live-source', 'library-channel'] as const) {
    // One service per guide kind, as the apps bind one per Channels view: each kind has its own viewer fence.
    const guideSvc = new ChannelGuideService(api, serverId);
    // The apps send whole-second instants (guideWindow); the server echoes them and the parser compares.
    const iso = (d: Date) => d.toISOString().replace('.000Z', 'Z');
    const route = {kind, start: iso(windowStart), end: iso(windowEnd), timezone: 'UTC', search: '', sourceId: ''};
    const path = `/v1/guide?${new URLSearchParams({kind, start: route.start, end: route.end, timezone: 'UTC', search: '', sourceId: '', limit: '30', cursor: ''})}`;
    try {
      await guideSvc.load(route);
    } catch (error) {
      rec.fail('live', `guide (${kind})`, 'GET', path, error);
      guideSvc.dispose();
      continue;
    }
    const snapshot = guideSvc.getSnapshot();
    if (snapshot.phase === 'error') {
      rec.fail('live', `guide (${kind})`, 'GET', path, Object.assign(new Error(snapshot.error ?? 'guide failed'), {code: 'guide_error'}));
    } else {
      rec.pass('live', `guide (${kind})`, 'GET', path);
    }
    guideSvc.dispose();
  }
  await rec.check('live', 'library channels', 'GET', '/v1/admin/library-channels', () => readLibraryChannels(api, serverId));
  await rec.check('live', 'library channel templates', 'GET', '/v1/admin/library-channels/templates', () => readLibraryTemplates(api, serverId));
  rec.skip('library channel preview', 'POST /v1/admin/library-channels/preview: a write-shaped call; skipped when unsure (library-channels.ts)');
  if (isOwner) {
    const dvr = new DVRClient(api, serverId, requestId);
    let firstRecording = '';
    for (const view of ['upcoming', 'recorded', 'rules'] as const) {
      const page = await rec.check('live', `dvr ${view}`, 'GET', `/v1/dvr?state=${view}&limit=50`, () => dvr.list(view)) as
        {recordings?: {id: string}[]} | undefined;
      const recordings = Array.isArray(page?.recordings) ? page.recordings : [];
      if (!firstRecording && recordings.length > 0 && typeof recordings[0].id === 'string') firstRecording = recordings[0].id;
    }
    await rec.check('live', 'dvr storage', 'GET', '/v1/dvr/storage', () => dvr.storage());
    const recordingId = firstRecording;
    if (recordingId) {
      await rec.check('live', 'dvr recording detail', 'GET', `/v1/dvr/recordings/${recordingId}`, () => dvr.detail(recordingId));
    } else {
      rec.skip('dvr recording detail', 'no recording sampled from the DVR lists');
    }
    dvr.dispose();
  }

  // 7. Notifications, downloads, groups.
  const inboxSvc = new InboxService(api);
  const inboxPath = '/v1/notifications/inbox?audience=profile&state=unread&limit=50';
  try {
    await inboxSvc.load();
  } catch (error) {
    rec.fail('notify', 'notification inbox', 'GET', inboxPath, error);
  }
  const inboxSnapshot = inboxSvc.getSnapshot();
  if (inboxSnapshot.phase === 'error') {
    rec.fail('notify', 'notification inbox', 'GET', inboxPath, Object.assign(new Error(inboxSnapshot.error ?? 'inbox failed'), {code: 'inbox_error'}));
  } else if (inboxSnapshot.phase === 'ready') {
    rec.pass('notify', 'notification inbox', 'GET', inboxPath);
  }
  if (inboxSnapshot.audiences.includes('account-admin')) {
    const adminPath = '/v1/notifications/inbox?audience=account-admin&state=unread&limit=50';
    try {
      await inboxSvc.load('account-admin');
      const adminSnapshot = inboxSvc.getSnapshot();
      if (adminSnapshot.phase === 'error') {
        rec.fail('notify', 'notification inbox (account-admin)', 'GET', adminPath, Object.assign(new Error(adminSnapshot.error ?? 'inbox failed'), {code: 'inbox_error'}));
      } else {
        rec.pass('notify', 'notification inbox (account-admin)', 'GET', adminPath);
      }
    } catch (error) {
      rec.fail('notify', 'notification inbox (account-admin)', 'GET', adminPath, error);
    }
  }
  await rec.check('notify', 'unread count', 'GET', '/v1/notifications/unread-count?audience=profile', () => inboxSvc.refreshUnread());
  inboxSvc.dispose();
  await rec.check('notify', 'feedback capabilities', 'GET', '/v1/feedback/capabilities', () =>
    // The inbox reads `data` (inbox.ts); the probe unwraps it the same way.
    api.request<unknown>('/v1/feedback/capabilities').then(raw => parseFeedbackCapabilities((raw as {data?: unknown}).data)));
  const downloadsSvc = new DownloadsService(api);
  await rec.check('notify', 'downloads preparations', 'GET', '/v1/downloads/preparations?limit=100', () => downloadsSvc.refresh());
  await rec.check('notify', 'downloads usage', 'GET', '/v1/downloads/usage', () =>
    api.request<unknown>('/v1/downloads/usage').then(parseDownloadUsage));
  if (playable[0]) {
    await rec.check('notify', `download options (${playable[0].itemId})`, 'GET', `/v1/items/${encodeURIComponent(playable[0].itemId)}/download-options`, () =>
      downloadsSvc.options(playable[0].itemId));
  }
  downloadsSvc.dispose();
  rec.skip('download request by id', 'needs a request id that only a POST create returns (download-requests.ts)');
  const groupSvc = new GroupSessionService({
    api: {baseUrl: input.origin, request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => api.request<T>(path, method, body, signal)},
    stream: () => Promise.reject(new Error('the probe never opens event streams')),
  });
  const groupsPath = '/v1/groups';
  try {
    await groupSvc.refreshDirectory();
    const directory = groupSvc.getSnapshot();
    if (directory.directoryPhase === 'error') {
      rec.fail('notify', 'group directory', 'GET', groupsPath,
        Object.assign(new Error(directory.directoryError ?? 'group directory failed'), {code: 'group_error'}));
    } else {
      rec.pass('notify', 'group directory', 'GET', groupsPath);
    }
  } catch (error) {
    rec.fail('notify', 'group directory', 'GET', groupsPath, error);
  }
  groupSvc.dispose();
  rec.skip('group snapshot/queue/events', 'open() starts a live event stream; the probe only reads the directory (group-session.ts)');
  rec.skip('hosted account notifications', 'a different authority/transport, not the selected server (account-notifications.ts)');

  // 8. Owner console reads.
  if (isOwner) {
    const ownerScope = scopeOf();
    const consoleClient = new ConsoleClient(api, ownerScope);
    const consoleReads: [string, string, () => Promise<unknown>][] = [
      ['console registry', '/v1/console/registry', () => consoleClient.registry()],
      ['console settings', '/v1/admin/console/settings', () => consoleClient.settings()],
      ['console preferences', '/v1/preferences?deviceClass=web', () => consoleClient.preferences('web')],
      ['console now playing', '/v1/admin/sessions?limit=50', () => new AdminSessionsClient(localV1Http(api)).list()],
      ['console stream options', '/v1/admin/console/stream-options', () => consoleClient.streamOptions()],
      ['console jobs', '/v1/admin/console/jobs?cursor=', () => consoleClient.jobs()],
      ['console job kinds', '/v1/admin/console/job-kinds', () => consoleClient.jobKinds()],
      ['console schedules', '/v1/admin/console/schedules', () => consoleClient.schedules()],
      ['console alerts', '/v1/admin/console/alerts', () => consoleClient.alerts()],
      ['console runtime diagnostics', '/v1/admin/console/diagnostics/runtime?cursor=', () => consoleClient.records('runtime')],
      ['console transcode capacity', '/v1/admin/transcode/capacity', () => consoleClient.transcodeCapacity()],
      ['console telemetry', '/v1/admin/telemetry?window=10m', () => consoleClient.telemetry()],
      ['console telemetry now', '/v1/admin/telemetry/now', () => consoleClient.telemetryNow()],
      ['console attention', '/v1/admin/attention', () => consoleClient.attention()],
      ['console playback history', '/v1/admin/playback/history?period=24h&cursor=&limit=50', () => consoleClient.playbackHistory()],
      ['console inbox', '/v1/notifications?cursor=', () => consoleClient.inbox()],
      ['console feedback reports', '/v1/admin/feedback?cursor=', () => consoleClient.reports(true)],
    ];
    for (const [name, path, run] of consoleReads) {
      await rec.check('owner', name, 'GET', path, run);
    }
    consoleClient.dispose();
    // The web console's administration documents: app-composed GET paths, client-core parsers.
    async function envelopeRead<T>(name: string, path: string, parse: (raw: unknown) => T): Promise<void> {
      await rec.check('owner', name, 'GET', path, async () => parseEnvelope(await api.request<unknown>(path), serverId, parse).result);
    }
    if (libs[0]) {
      await envelopeRead('library admin document', `/v1/admin/libraries/${encodeURIComponent(libs[0].id)}/settings`, parseLibraryDocument);
    }
    await envelopeRead('dvr settings document', '/v1/admin/dvr/settings', parseDVRDocument);
    await envelopeRead('dvr tuners', '/v1/admin/dvr/tuners', parseTunerView);
    await envelopeRead('live defaults', '/v1/admin/live/settings', parseLiveDefaultsDocument);
    await envelopeRead('backups document', '/v1/admin/backups', parseBackupsDocument);
    // Web MaintenanceWindows.tsx reads this registry document structurally (its readDocument); the same checks here.
    await envelopeRead('maintenance settings', '/v1/admin/maintenance/settings', raw => {
      const v = raw as {revision?: unknown; settings?: {windows?: unknown}; cadences?: unknown; tasks?: unknown} | null;
      if (!v || typeof v.revision !== 'number' || !v.settings || !Array.isArray(v.settings.windows) || !Array.isArray(v.cadences) || !Array.isArray(v.tasks)) throw Object.assign(new Error('maintenance settings unreadable'), {code: 'invalid_response'});
      return v;
    });
    await envelopeRead('storage report', '/v1/admin/storage-usage', parseStorageReport);
    // A registry route: the bare report, no administration envelope (the web reads it directly).
    await rec.check('owner', 'update report', 'GET', '/v1/admin/updates', () => api.request<unknown>('/v1/admin/updates').then(parseUpdateReport));
    await rec.check('owner', 'server logs page 1', 'GET', '/v1/admin/logs?limit=20', () =>
      api.request<unknown>('/v1/admin/logs?limit=20').then(parseLogPage));
    await rec.check('owner', 'log settings', 'GET', '/v1/admin/logs/settings', () =>
      api.request<unknown>('/v1/admin/logs/settings').then(parseLogSettings));
    const accessLists: [string, string, (raw: unknown) => unknown][] = [
      ['access members', '/v1/admin/access/members?limit=20', parseAccessMember],
      ['access invitations', '/v1/admin/access/invitations?limit=20', parseInvitation],
      ['access devices', '/v1/admin/access/devices?limit=20', parseAccessDevice],
      ['API keys', '/v1/admin/api-keys?limit=20', parseApiCredential],
    ];
    for (const [name, path, each] of accessLists) {
      await rec.check('owner', name, 'GET', path, async () => {
        const raw = await api.request<{items?: unknown}>(path);
        if (!raw || !Array.isArray(raw.items)) throw new Error('The administration response does not match this server.');
        return raw.items.map(each);
      });
    }
    await rec.check('owner', 'remote access', 'GET', '/v1/networking/remote', () => loadRemoteAccess(api));
    rec.skip('activity screen', 'ActivityService snapshots differ from ConsoleClient.jobs, which already covers jobs (activity.ts)');
    rec.skip('library management directory', 'libraries admin is covered through parseLibraryDocument (library-management.ts)');
  } else {
    rec.skip('owner console reads', `viewer role is '${role}', not 'owner'`);
  }

  detailSvc.dispose();
  contentSvc.dispose();

  const finishedAt = new Date().toISOString();
  const counts: Record<string, {ok: number; failed: number}> = {};
  for (const call of rec.calls) {
    counts[call.area] ??= {ok: 0, failed: 0};
    if (call.ok) counts[call.area].ok++;
    else counts[call.area].failed++;
  }
  return {
    origin: input.origin,
    serverId,
    role,
    limit,
    startedAt,
    finishedAt,
    counts,
    failures: rec.calls.filter(call => !call.ok),
    calls: rec.calls,
    notProbed: rec.notProbed,
  };
}

export function summarize(report: ProbeReport): string {
  const lines = [`parser-probe ${report.origin} server=${report.serverId} role=${report.role} limit=${report.limit}`];
  for (const [area, count] of Object.entries(report.counts)) {
    lines.push(`${area}: ${count.ok} ok, ${count.failed} failed`);
  }
  lines.push(`failures: ${report.failures.length}`);
  for (const failure of report.failures) {
    const detail = failure.error ? ` code=${failure.error.code} message=${failure.error.message}` : '';
    lines.push(`- [${failure.area}] ${failure.name} ${failure.method} ${failure.path} status=${failure.status}${detail}`);
    if (failure.excerpt) lines.push(`  excerpt: ${failure.excerpt.slice(0, 200)}`);
  }
  lines.push(`not probed (${report.notProbed.length}): ${report.notProbed.map(entry => entry.name).join('; ')}`);
  return lines.join('\n');
}

async function main(): Promise<void> {
  const origin = process.env.PROBE_ORIGIN ?? '';
  const sessionFile = process.env.PROBE_SESSION_FILE ?? '';
  const out = process.env.PROBE_OUT ?? '';
  const limitRaw = process.env.PROBE_LIMIT ?? '5';
  if (!origin || !sessionFile) {
    process.stderr.write('usage: PROBE_ORIGIN=<url> PROBE_SESSION_FILE=<json> [PROBE_OUT=<path>] [PROBE_LIMIT=<n>] node --experimental-strip-types packages/client-core/tools/parser-probe.ts\n');
    process.exit(2);
  }
  const limit = Math.max(1, Math.min(50, Number.parseInt(limitRaw, 10) || 5));
  const session = JSON.parse(readFileSync(sessionFile, 'utf8')) as {accessToken?: unknown; viewer?: {serverId?: unknown}};
  const token = typeof session.accessToken === 'string' ? session.accessToken : '';
  if (!token) {
    process.stderr.write('PROBE_SESSION_FILE must be JSON with an accessToken.\n');
    process.exit(2);
  }
  const fileServerId = typeof session.viewer?.serverId === 'string' ? session.viewer.serverId : '';
  const seen = new Map<string, {status: number; body: string}>();
  const api = new HttpLocalApi(origin, token, makeRecordingFetch(globalThis.fetch, seen));
  const report = await walkProbe(api, {
    origin,
    serverId: fileServerId,
    viewerId: 'signed-out',
    authority: '',
    accountId: '',
    profileId: '',
    role: '',
    limit,
    seen,
    token,
  });
  if (out) {
    writeFileSync(out, JSON.stringify(report, null, 2));
    process.stdout.write(`${summarize(report)}\nwrote ${out}\n`);
  } else {
    process.stdout.write(`${summarize(report)}\n`);
  }
}

const invokedAs = process.argv[1] ? pathToFileURL(process.argv[1]).href : '';
if (import.meta.url === invokedAs) {
  await main();
}
