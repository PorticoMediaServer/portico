import {stopServerStream} from '../../../../../../packages/client-core/src/admin/server-now-playing';
import {readPlaySummary, readStatistics} from '../../../../../../packages/client-core/src/admin/statistics';
import {fixDataFolderPermissions} from '../../../../../../packages/client-core/src/admin/data-folder';
import {readServerLoad, type TelemetryReading} from '../../../../../../packages/client-core/src/telemetry';
import type {UpdatesApi} from '../../../../../../packages/client-core/src/server-admin/updates-source';
import type {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import type {OwnerHealthWindow, OwnerViewing, OwnerViewingPeriod} from './model';

/**
 * The Dashboard's own reads and owner commands over the screen lifetime. The admission
 * controller stays the only authority: this borrows its lifetime for every request and never
 * chooses a role. Administrators never reach the owner-only reads because the caller gates them.
 */
export type NativeOwnerDashboardReader = Readonly<{
  telemetry(window: OwnerHealthWindow): Promise<TelemetryReading>;
  /** The statistics' totals per day and the Play history summary, over one range. */
  viewing(period: OwnerViewingPeriod): Promise<OwnerViewing>;
  /** Stops a stream; the viewer is shown the message (`DELETE /v1/admin/sessions/{id}`). */
  stop(sessionId: string, message?: string): Promise<void>;
  /** Lets only the data folder's owner open it (`POST /v1/admin/data-folder/permissions/fix`). */
  fixPermissions(): Promise<void>;
  /** The owner's release check (read and pressed check), on this lifetime. */
  updates: UpdatesApi;
  dispose(): void;
}>;

export function createNativeOwnerDashboardReader(lifetime: NativeScreenLifetime, ports: NativeSettingsLivePorts): NativeOwnerDashboardReader {
  const {context} = ports;
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)};
  return Object.freeze({
    telemetry: (window: OwnerHealthWindow) => readServerLoad(api, window, lifetime.signal),
    viewing: async (period: OwnerViewingPeriod) => {
      const [statistics, summary] = await Promise.all([readStatistics(api, period, lifetime.signal), readPlaySummary(api, {range: period}, lifetime.signal)]);
      return {totals: statistics.totals, mostPlayed: summary.titles, mostActive: summary.people};
    },
    stop: (sessionId: string, message?: string) => stopServerStream(api, sessionId, message, lifetime.signal),
    fixPermissions: () => fixDataFolderPermissions(api, lifetime.signal).then(() => undefined),
    updates: api,
    dispose: () => {},
  });
}
