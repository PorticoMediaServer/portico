import {needsOfflineServerDialog, presenceLabel, sortServersByPresence, type HostedServer} from '../../../../../packages/client-core/src';
import {presentError, type ErrorCategory} from '../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../packages/i18n/src';

export type DirectorySubtitle = Readonly<{
  key: 'web.chooser.current' | 'web.chooser.lastSeen' | 'web.chooser.neverSeen' | 'web.servers.online';
  values?: Readonly<{date: string}>;
}>;
export type DirectoryRow = Readonly<{id: string; name: string; owner?: true; subtitle?: DirectorySubtitle; offline: boolean;
  /** The cached last-seen fact behind the subtitle, kept raw so a surface can draw its own same-day form. */
  lastSeen?: Readonly<{at: string; recent: boolean}>}>;

/** The phone directory's rows: online first, then last seen, then unknown. Offline rows
 * are dimmed but remain chooseable; a malformed or absent presence renders normally. */
export function directoryRows(servers: readonly HostedServer[], currentId?: string, nowMs = Date.now()): readonly DirectoryRow[] {
  return sortServersByPresence(servers).map(server => {
    const identity = {id: server.id, name: server.name, ...(server.ownedByMe === true && server.role === 'owner' ? {owner: true as const} : {})};
    if (currentId && server.id === currentId) return {...identity, subtitle: {key: 'web.chooser.current'}, offline: false};
    const label = presenceLabel(server.presence, nowMs);
    if (label.kind === 'online') return {...identity, subtitle: {key: 'web.servers.online'}, offline: false};
    if (label.kind === 'lastSeen') return {...identity, lastSeen: {at: label.lastSeenAt, recent: label.recent},
      subtitle: {key: 'web.chooser.lastSeen', values: {date: defaultI18n.date(label.lastSeenAt, 'dayMonth')}}, offline: true};
    if (label.kind === 'neverSeen') return {...identity, subtitle: {key: 'web.chooser.neverSeen'}, offline: true};
    return {...identity, offline: false};
  });
}

export type DirectoryFailure = 'other' | 'opening' | 'waiting' | 'unreachable';
/** Route/connection refusals that mean the server could not be reached, not a feature
 * or resource that is unavailable. Generic `busy`/`unavailable` codes keep their own
 * error presentation rather than becoming a keep-trying chooser state. */
const connectionCodes: ReadonlySet<string> = new Set(['server_unavailable', 'route_unavailable']);

/** Which chooser state a connect attempt is in. A silent cancellation is `other`, so the
 * caller returns to the list without drawing a failure. */
export function directoryFailure(server: HostedServer | undefined, error: unknown, nowMs = Date.now()): DirectoryFailure {
  if (!server) return 'other';
  if (error === undefined) return 'opening';
  const presentation = presentError(error, 'sign-in');
  const connection = presentation.category === 'offline' || presentation.category === 'unreachable' || presentation.category === 'timeout'
    || presentation.category === 'unavailable' && !!presentation.code && connectionCodes.has(presentation.code);
  if (presentation.silent || !connection) return 'other';
  return needsOfflineServerDialog(server.presence, true, nowMs) ? 'unreachable' : 'waiting';
}

export function directoryOfflineBody(server: HostedServer): Readonly<{
  key: 'web.chooser.unreachableBody' | 'web.chooser.unreachableBodyNever';
  values?: Readonly<{date: string}>;
}> {
  const label = presenceLabel(server.presence);
  return label.kind === 'lastSeen'
    ? {key: 'web.chooser.unreachableBody', values: {date: defaultI18n.date(label.lastSeenAt, 'dayMonth')}}
    : {key: 'web.chooser.unreachableBodyNever'};
}

/** The waiting chooser retries after 20 seconds, doubling to a five-minute ceiling. */
export function nextRetryDelay(current: number): number {
  return Math.min(Math.max(current, 20) * 2, 300);
}
