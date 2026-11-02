import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import {notificationPreparationTarget, type Notification, type NotificationAction} from './notifications.ts';

/**
 * OW-1: what a notice's action does, decided once for every client. The server's vocabulary
 * (`notify.go` Commands and Views) is closed; each value a producer publishes maps to one intent here,
 * and web and native map each intent to their own surface. An intent a client has no surface for is
 * the only reason an action is drawn disabled (or left out on native): never a value this module knows.
 *
 * Commands are never synthesised from strings: each reads only its own documented argument.
 */
export type NotificationIntent =
  | Readonly<{kind: 'home'}>
  | Readonly<{kind: 'settings'}>
  /** This account's password and two-step sign-in. */
  | Readonly<{kind: 'account-security'}>
  /** This account's own devices ("New device signed in" → Review device). */
  | Readonly<{kind: 'account-devices'}>
  /** The administrators' copy: the server's people and their devices. Administrators only. */
  | Readonly<{kind: 'server-people'}>
  /** Server › Remote access: certificates, blocked sources, sign-in bursts. Administrators only. */
  | Readonly<{kind: 'server-network'}>
  /** Server › Storage. Administrators only. */
  | Readonly<{kind: 'server-storage'}>
  /** Server › Troubleshooting › Viewer feedback (the reports list). Administrators only. */
  | Readonly<{kind: 'server-feedback'; reportId?: string}>
  | Readonly<{kind: 'dashboard'}>
  | Readonly<{kind: 'recordings'; recordingId?: string}>
  | Readonly<{kind: 'live'}>
  | Readonly<{kind: 'search'}>
  | Readonly<{kind: 'library'; libraryId: string}>
  | Readonly<{kind: 'item'; itemId: string}>
  | Readonly<{kind: 'person'; personId: string}>
  /** Downloads. Opening it confers no transfer or offline authority (Root33); a failed download's Try again is there. */
  | Readonly<{kind: 'downloads'}>
  /** The reporter's own report. No client draws a page for it yet. */
  | Readonly<{kind: 'own-feedback'; reportId: string}>
  /** Start a scan of this library (Scan again). Administrators only; the server checks. */
  | Readonly<{kind: 'scan-library'; libraryId: string}>
  /** Dismiss: this notice goes to Archived (a recording conflict the owner has seen). */
  | Readonly<{kind: 'archive-notice'}>;

const opaque = (value: unknown, max = 160): value is string => typeof value === 'string' && /^[A-Za-z0-9_.:-]+$/.test(value) && value.length <= max;
const arg = (action: NotificationAction, key: string) => {const value = action.arguments?.[key]; return opaque(value) ? value : undefined;};

export function notificationIntent(action: NotificationAction, note?: Notification): NotificationIntent | undefined {
  if (action.kind === 'command') {
    switch (action.command) {
      case 'open-download':
        return note && notificationPreparationTarget(note, action) || arg(action, 'downloadId') ? {kind: 'downloads'} : undefined;
      // The failed download's own Try again lives on Downloads, under the device's transfer authority.
      case 'retry-job': return arg(action, 'downloadId') ? {kind: 'downloads'} : undefined;
      case 'review-device': return arg(action, 'deviceId') ? {kind: 'account-devices'} : undefined;
      case 'dismiss-conflict': return arg(action, 'recordingId') ? {kind: 'archive-notice'} : undefined;
      case 'run-scan': {const libraryId = arg(action, 'libraryId'); return libraryId ? {kind: 'scan-library', libraryId} : undefined;}
      case 'open-feedback': {const reportId = arg(action, 'reportId'); return reportId ? {kind: 'own-feedback', reportId} : undefined;}
      default: return undefined;
    }
  }
  const target = action.target, entityId = opaque(target?.entityId) ? target!.entityId : undefined, libraryId = opaque(target?.libraryId) ? target!.libraryId : undefined;
  switch (target?.view) {
    case 'home': return {kind: 'home'};
    case 'settings': return {kind: 'settings'};
    case 'settings-security': return {kind: 'account-security'};
    case 'admin-devices': return {kind: 'server-people'};
    case 'settings-network': return {kind: 'server-network'};
    case 'settings-storage': return {kind: 'server-storage'};
    case 'admin-feedback': return {kind: 'server-feedback', ...(entityId ? {reportId: entityId} : {})};
    case 'server': return {kind: 'dashboard'};
    case 'recordings': case 'recording': return {kind: 'recordings', ...(entityId ? {recordingId: entityId} : {})};
    case 'live': return {kind: 'live'};
    case 'search': return {kind: 'search'};
    case 'downloads': return {kind: 'downloads'};
    case 'library': return libraryId ? {kind: 'library', libraryId} : undefined;
    case 'item': return entityId ? {kind: 'item', itemId: entityId} : undefined;
    case 'person': return entityId ? {kind: 'person', personId: entityId} : undefined;
    default: return undefined;
  }
}

/** Scan again: starts a scan of the library named by the notice (`POST /v1/libraries/{id}/scans`). */
export async function startNotificationScan(api: Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>, libraryId: string, signal?: AbortSignal): Promise<void> {
  await api.request(encodedRoutePath('post_libraries_id_scans', {id: encodeURIComponent(libraryId)}), 'POST', undefined, signal);
}
