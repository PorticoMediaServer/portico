import {ConsoleClient, type Alert, type Job} from '../../../../../../packages/client-core/src/console';
import {AdminSessionsClient} from '../../../../../../packages/client-core/src/playback-v1/admin-sessions';
import {localV1Http} from '../../../../../../packages/client-core/src/playback-v1/local-http';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import {fetchPlaySummary, type PlaySummary} from '../../../../../../packages/client-core/src/play-history';
import {playbackControlsService} from '../../../../../../packages/client-core/src/playback-controls';
import {sendSeekTo, sendTransport, type TransportCommand} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {fixStatePermissions} from '../../../../../../packages/client-core/src/backups';
import type {TelemetryReading} from '../../../../../../packages/client-core/src/telemetry';
import type {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import type {OwnerHealthWindow, OwnerViewingPeriod} from './model';

/**
 * The Dashboard's own reads and owner commands over the screen lifetime. The admission
 * controller stays the only authority: this borrows its lifetime for every request and never
 * chooses a role. Administrators never reach the owner-only reads because the caller gates them.
 */
export type NativeOwnerDashboardReader = Readonly<{
  /** Show all reads the same server-ordered list, a bounded page at a time. */
  sessions(cursor?: string, limit?: number): ReturnType<AdminSessionsClient['list']>;
  telemetry(window: OwnerHealthWindow): Promise<TelemetryReading>;
  playSummary(period: OwnerViewingPeriod): Promise<PlaySummary>;
  acknowledge(alert: Alert): Promise<void>;
  jobCommand(job: Job, action: 'pause' | 'resume' | 'retry' | 'cancel'): Promise<void>;
  stop(sessionId: string, message?: string): Promise<void>;
  transport(sessionId: string, command: TransportCommand): Promise<void>;
  move(sessionId: string, positionMs: number): Promise<void>;
  fixPermissions(operationId: string): Promise<void>;
  dispose(): void;
}>;

const zone = (): string => {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; }
};

export function createNativeOwnerDashboardReader(lifetime: NativeScreenLifetime, ports: NativeSettingsLivePorts): NativeOwnerDashboardReader {
  const {context} = ports, authority = lifetime.authority;
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)};
  const console_ = new ConsoleClient(api, {serverId: authority.serverId, viewerId: authority.profileId});
  const raw = localV1Http(context.api);
  const sessions = new AdminSessionsClient({send: request => lifetime.request(signal => raw.send({...request, signal}), request.signal)}, ports.requestId);
  const controls = playbackControlsService(<T,>(path: string, method?: string, body?: unknown) => lifetime.request(signal => context.api.request<T>(path, method, body, signal)));
  return Object.freeze({
    sessions: (cursor?: string, limit = 200) => sessions.list(cursor, Math.max(1, Math.min(limit, 200)), lifetime.signal),
    telemetry: (window: OwnerHealthWindow) => console_.telemetry(window, captureReportContext(zone())),
    playSummary: (period: OwnerViewingPeriod) => fetchPlaySummary(api, {...captureReportContext(zone()), period}),
    acknowledge: (alert: Alert) => console_.acknowledge(alert).then(() => undefined),
    jobCommand: (job: Job, action: 'pause' | 'resume' | 'retry' | 'cancel') => console_.jobCommand(job, action).then(() => undefined),
    stop: (sessionId: string, message?: string) => sessions.terminate(sessionId, message, lifetime.signal),
    transport: (sessionId: string, command: TransportCommand) => sendTransport(controls, sessionId, command).then(() => undefined),
    move: (sessionId: string, positionMs: number) => sendSeekTo(controls, sessionId, positionMs).then(() => undefined),
    fixPermissions: (operationId: string) => fixStatePermissions(api, authority.serverId, operationId, lifetime.signal),
    dispose: () => console_.dispose(),
  });
}
