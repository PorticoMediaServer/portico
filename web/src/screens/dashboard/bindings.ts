import type {EventsClient} from '@core/playback-v1/events.ts';
import type {SessionControls} from '@core/playback-controls.ts';
import type {HttpLocalApi} from '@core/index.ts';
import {ConsoleClient, type ConsoleScope} from '@core/console.ts';
import {DashboardDataClient, type DashboardReport as WireDashboardReport, type DashboardReportFilters, type DashboardPeople, type DashboardStatus} from '@core/admin/dashboard.ts';
import {parseReportContext, type ReportContext} from '@core/report-context.ts';
import {parseDashboardSessions} from '@core/admin/activity-summary/parser.ts';
import type {DashboardSessionsPage} from '@core/admin/activity-summary/types.ts';
import {parseAdminSession, type AdminSession} from '@core/playback-v1/types.ts';
import {call} from '@core/playback-v1/http.ts';
import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {AdminSessionsClient} from '@core/playback-v1/admin-sessions.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import {playbackControlsService} from '@core/playback-controls.ts';
import {sendSeekTo, sendTransport} from '@core/server-admin/now-playing.ts';
import {fetchPlayHistory, type PlayHistoryApi, type PlayHistoryFilters, type PlayHistoryPage} from '@core/play-history.ts';
import {parseTelemetryReading, type TelemetryReading} from '@core/telemetry.ts';
import {retryCertificate, type CertificateAction, type CertificateApi} from '@core/certificates.ts';
import type {FactPart} from '@core/presentation/kit-model/index.ts';
import {settingsServerHeader} from '../settings/server-header';
import type {ArtworkSource} from '../../kit/artwork/types';
import {DASHBOARD_SESSION_PAGE_SIZE} from './session-window';

export type DashboardEvents = Pick<EventsClient, 'on'> & Partial<Pick<EventsClient, 'onResync' | 'onResyncRead' | 'resyncReads'>>;
export type DashboardAlert = DashboardStatus['alerts'][number];
export type DashboardSession = AdminSession & Pick<DashboardSessionsPage['items'][number], 'channel' | 'programme'>;
export type DashboardSessions = Omit<Awaited<ReturnType<AdminSessionsClient['list']>>, 'items'> & {items: readonly DashboardSession[]};
export type DashboardFilters = Omit<PlayHistoryFilters, keyof ReportContext>;
export type DashboardReportQuery = Omit<DashboardReportFilters, keyof ReportContext>;
/** The drawn server header line: the same measurements ServerHeader draws, from this server only. */
export type DashboardServerHeader = Readonly<{health?: 'normal' | 'attention'; facts: readonly FactPart[]}>;
export type DashboardReport = Omit<WireDashboardReport, 'load'> & Readonly<{load: TelemetryReading}>;
export type DashboardBindings = Readonly<{
  scope: ConsoleScope; role: 'owner' | 'admin' | 'member'; isCurrent: () => boolean; isMainProfile: () => boolean;
  /** Captured by the shell once per view/refresh with the checked canonical helper. */
  context: ReportContext;
  events?: DashboardEvents;
  subscribeAuthority?: (listener: () => void) => () => void;
  read: Readonly<{
    sessions: (cursor?: string, signal?: AbortSignal) => Promise<DashboardSessions>;
    status: (signal?: AbortSignal) => Promise<DashboardStatus>;
    /** Canonical session-specific permission/revision receipt, read only while its menu is open. */
    controls?: (id: string, signal: AbortSignal) => Promise<SessionControls>;
    /** Live desktop load or the phone Health panel's canonical finite windows. */
    telemetry: (range: 'live' | '10m' | '1h' | '24h', signal?: AbortSignal) => Promise<TelemetryReading>;
    report: (filters: DashboardReportQuery, signal?: AbortSignal) => Promise<DashboardReport>;
    people: (filters: DashboardFilters, cursor?: string, signal?: AbortSignal) => Promise<DashboardPeople>;
    plays: (filters: DashboardFilters, cursor?: string, signal?: AbortSignal) => Promise<PlayHistoryPage>;
    /** Owner-only server health and facts for the page header. */
    server: (signal?: AbortSignal) => Promise<DashboardServerHeader>;
  }>;
  effects: Readonly<{
    transport: (id: string, command: 'pause' | 'resume') => Promise<void>;
    stop: (id: string, message?: string) => Promise<void>;
    /** The original timeline opening owns cancellation through every canonical control read/retry/write. */
    seek?: (id: string, positionMs: number, signal: AbortSignal) => Promise<void>;
    openHistory: () => void; openHome: () => void; openSettings: (section: string) => void;
    /** Supplied by the action owner; mutation must retain its existing revision/idempotency guard. */
    retryCertificate?: () => Promise<void>;
    reportError: (error: unknown) => void;
  }>;
  artwork: (itemId: string, kind: 'poster' | 'backdrop' | 'square', revision?: string) => ArtworkSource | undefined;
  dispose: () => void;
}>;
export type DashboardBindingOptions = Pick<DashboardBindings, 'scope' | 'role' | 'context' | 'isCurrent' | 'isMainProfile' | 'events' | 'artwork' | 'subscribeAuthority'> & {
  api: Pick<HttpLocalApi, 'request' | 'requestRaw'>;
  effects: Pick<DashboardBindings['effects'], 'openSettings' | 'openHome' | 'reportError'> & {openHistory: (context: ReportContext) => void};
};

/** Captures one selected-server/main-profile lifetime. All asynchronous reads and writes recheck it. */
export function createDashboardBindings(options: DashboardBindingOptions): DashboardBindings {
  const context = parseReportContext(options.context);
  const life = new AbortController(), listeners = new Set<() => void>();
  const close = () => {if (!life.signal.aborted) {life.abort(); for (const listener of [...listeners]) listener();}};
  const current = () => !life.signal.aborted && options.isCurrent() && options.isMainProfile() && (options.role === 'owner' || options.role === 'admin');
  const assert = (owner = false) => {if (!current()) {close(); throw new DOMException('Dashboard authority changed', 'AbortError');} if (owner && options.role !== 'owner') throw new DOMException('Dashboard authority changed', 'AbortError');};
  const offAuthority = options.subscribeAuthority?.(() => {if (!current()) close();});
  const api: PlayHistoryApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    assert(); try {const readSignal = signal ? AbortSignal.any([signal, life.signal]) : life.signal;
      const request = () => options.api.request<T>(path, method, body, readSignal);
      const result = await (options.events?.resyncReads && (!method || method === 'GET') ? options.events.resyncReads.run(readSignal, request) : request()); assert(); return result;} catch (error) {if (error && typeof error === 'object' && 'status' in error && (error.status === 401 || error.status === 403)) close(); throw error;}
  }};
  const http = localV1Http({requestRaw: async (...args: Parameters<HttpLocalApi['requestRaw']>) => {
    assert(); const [path, method, init] = args;
    const readSignal = init?.signal ? AbortSignal.any([init.signal, life.signal]) : life.signal;
    const request = () => options.api.requestRaw(path, method, {...init, signal: readSignal});
    const result = await (options.events?.resyncReads && method === 'GET' ? options.events.resyncReads.run(readSignal, request) : request()); if (result.status === 401 || result.status === 403) close(); assert(); return result;
  }});
  const data = new DashboardDataClient(api), sessions = new AdminSessionsClient(http), consoleClient = new ConsoleClient(api, options.scope);
  const controls = playbackControlsService(api.request.bind(api));
  const certificates: CertificateApi = {requestCertificate: <T>(action: CertificateAction, body: unknown, signal: AbortSignal): Promise<T> => {
    assert();
    const path = routePath(action === 'status' ? 'get_networking_certificate' : action === 'config' ? 'post_networking_certificate_config' : 'post_networking_certificate_retry');
    return options.api.request<T>(path, action === 'status' ? 'GET' : 'POST', body, signal ? AbortSignal.any([signal, life.signal]) : life.signal).then(value => {assert(); return value;});
  }};
  const operationControls = (signal: AbortSignal) => {
    const service = playbackControlsService(async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
      assert(); signal.throwIfAborted();
      const value = await api.request<T>(path, method, body, signal);
      assert(); signal.throwIfAborted(); return value;
    });
    return {...service, readAdmin: async (id: string) => {
      const value = await service.readAdmin(id);
      // A canonical reread may update the revision, never the original target.
      if (value.sessionId !== id) throw new Error('dashboard_controls_session_mismatch');
      return value;
    }};
  };
  let fence: string | undefined;
  let observed: string | undefined;
  const ownerRead = async <T>(work: () => Promise<T>) => {assert(true); const value = await work(); assert(true); return value;};
  return {
    scope: {...options.scope}, role: options.role, context, isCurrent: current, isMainProfile: options.isMainProfile, events: options.events, subscribeAuthority: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}, artwork: options.artwork,
    read: {
      sessions: async (cursor, signal) => {
        assert(); const query = new URLSearchParams({limit: String(DASHBOARD_SESSION_PAGE_SIZE)}); if (cursor) query.set('cursor', cursor);
        const raw = parseDashboardSessions((await call(http, {method: 'GET', path: routePath('list_admin_sessions') + '?' + query, signal})).body); assert();
        if (raw.scope.serverId !== options.scope.serverId || fence !== undefined && fence !== raw.scope.viewerFence || observed !== undefined && Date.parse(raw.observedAt) < Date.parse(observed)) {close(); throw new DOMException('Dashboard authority changed', 'AbortError');}
        fence = raw.scope.viewerFence; observed = raw.observedAt;
        return {items: raw.items.map(item => ({...parseAdminSession(item), channel: item.channel, programme: item.programme})), nextCursor: raw.page.nextCursor ?? '', scope: raw.scope, totals: raw.totals};
      },
      status: signal => ownerRead(() => data.status(signal)),
      controls: async (id, signal) => {assert(); signal.throwIfAborted(); const value = await operationControls(signal).readAdmin(id); assert(); signal.throwIfAborted(); return value;},
      telemetry: (range, signal) => ownerRead(async () => {if (signal?.aborted) throw signal.reason; const value = await consoleClient.telemetry(range, context); if (signal?.aborted) throw signal.reason; return value;}),
      report: (filters, signal) => ownerRead(async () => {const value = await data.report({...filters, ...context}, signal); return {...value, load: parseTelemetryReading(value.load)};}),
      people: (filters, cursor, signal) => ownerRead(() => data.people({...filters, ...context}, cursor, 100, signal)),
      plays: (filters, cursor, signal) => ownerRead(() => fetchPlayHistory(api, {...filters, ...context}, cursor, signal)),
      server: signal => ownerRead(async () => {
        const [health, storage] = await Promise.allSettled([consoleClient.measurement('health'), consoleClient.measurement('storage')]);
        if (signal?.aborted) throw signal.reason;
        return settingsServerHeader(health.status === 'fulfilled' ? health.value : undefined, storage.status === 'fulfilled' ? storage.value : undefined);
      }),
    },
    effects: {...options.effects,
      openHistory: () => {assert(true); options.effects.openHistory(context);},
      openHome: () => {assert(); options.effects.openHome();},
      transport: async (id, command) => {assert(); await sendTransport(controls, id, command); assert();},
      seek: async (id, positionMs, signal) => {assert(); signal.throwIfAborted(); await sendSeekTo(operationControls(signal), id, positionMs); assert(); signal.throwIfAborted();},
      stop: async (id, message) => {assert(true); await sessions.terminate(id, message); assert(true);},
      retryCertificate: async () => {assert(true); await retryCertificate(certificates, AbortSignal.timeout(15000)); assert(true);},
    },
    dispose: () => {close(); offAuthority?.(); consoleClient.dispose(); listeners.clear();},
  };
}
