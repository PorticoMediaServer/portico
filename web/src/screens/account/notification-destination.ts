import type {Notification, NotificationAction} from '@core/notifications.ts';
import {notificationIntent} from '@core/notification-actions.ts';

export type NoticeDestination = Readonly<{view: 'home'}> | Readonly<{view: 'settings'; scope?: 'account' | 'server'; section?: string; row?: string}> | Readonly<{view: 'dashboard'}>
  | Readonly<{view: 'library'; id: string}> | Readonly<{view: 'item'; id: string}> | Readonly<{view: 'downloads'}>
  | Readonly<{view: 'live'; tab?: 'recordings'}> | Readonly<{view: 'search'}> | Readonly<{view: 'person'; id: string}>
  /** Commands carried out in place: Scan again starts the library's scan; Dismiss archives the notice. */
  | Readonly<{view: 'scan'; libraryId: string}> | Readonly<{view: 'archive'}>
  /** The reporter's own report, read only, over the notice. */
  | Readonly<{view: 'own-report'; reportId: string}>;

/**
 * Where a notice action goes on web (OW-1): the shared intent (`@core/notification-actions`) mapped to a real web surface.
 * Server-only surfaces need the server's management capability; a library must be one this profile can see. Undefined only
 * when web has no surface for it, and the action is then drawn disabled. Opening Downloads confers no transfer authority.
 */
export function notificationDestination(action: NotificationAction, libraries: readonly {id: string}[], canManageServer: boolean, note?: Notification): NoticeDestination | undefined {
  const intent = notificationIntent(action, note);
  if (!intent) return;
  switch (intent.kind) {
    case 'home': return {view: 'home'};
    case 'settings': return {view: 'settings'};
    // Review security: this account's password and two-step sign-in.
    case 'account-security': return {view: 'settings', scope: 'account', section: 'profile', row: 'profile.password'};
    // "New device signed in": Review device opens this account's own devices.
    case 'account-devices': return {view: 'settings', scope: 'account', section: 'profile', row: 'profile.devices'};
    case 'server-people': return canManageServer ? {view: 'settings', scope: 'server', section: 'people'} : undefined;
    case 'server-network': return canManageServer ? {view: 'settings', scope: 'server', section: 'remote'} : undefined;
    case 'server-storage': return canManageServer ? {view: 'settings', scope: 'server', section: 'storage'} : undefined;
    case 'server-feedback': return canManageServer ? {view: 'settings', scope: 'server', section: 'troubleshooting'} : undefined;
    case 'dashboard': return canManageServer ? {view: 'dashboard'} : undefined;
    case 'recordings': return {view: 'live', tab: 'recordings'};
    case 'live': return {view: 'live'};
    case 'search': return {view: 'search'};
    case 'downloads': return {view: 'downloads'};
    case 'library': return libraries.some(library => library.id === intent.libraryId) ? {view: 'library', id: intent.libraryId} : undefined;
    case 'item': return {view: 'item', id: intent.itemId};
    case 'person': return {view: 'person', id: intent.personId};
    case 'scan-library': return canManageServer && libraries.some(library => library.id === intent.libraryId) ? {view: 'scan', libraryId: intent.libraryId} : undefined;
    case 'archive-notice': return {view: 'archive'};
    // No frame draws a reporter's report page or editor: a read-only sheet over the notice (OwnFeedbackReport).
    case 'own-feedback': return {view: 'own-report', reportId: intent.reportId};
  }
}
