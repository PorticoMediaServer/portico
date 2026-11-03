import type {Notification, NotificationAction} from '../../../../../packages/client-core/src/notifications';
import {notificationIntent} from '../../../../../packages/client-core/src/notification-actions';
export {startNotificationScan} from '../../../../../packages/client-core/src/notification-actions';
import type {NativeShellRoute} from '../../shell/routes';

/** A command the Notifications screen carries out in place: Dismiss archives the notice; Scan again starts the scan. */
export type NotificationCommandTarget = Readonly<{kind: 'archive'}> | Readonly<{kind: 'scan'; libraryId: string}>
  /** "Your report has an update" → Open report: the reporter's own report, read only, over the list. */
  | Readonly<{kind: 'report'; reportId: string}>;

/** The real surface a notice action opens on this device, or nothing when this build has none
 * (the action is then left out rather than drawn disabled). The meaning is shared with web (OW-1). */
export function notificationRoute(action: NotificationAction, hasLiveTV: boolean, note?: Notification): NativeShellRoute | undefined {
  const intent = notificationIntent(action, note);
  switch (intent?.kind) {
    case 'home': return {kind: 'shell', destination: {kind: 'home'}};
    case 'library': return {kind: 'shell', destination: {kind: 'library', libraryId: intent.libraryId}};
    case 'live': return hasLiveTV ? {kind: 'shell', destination: {kind: 'live'}} : undefined;
    case 'search': return {kind: 'shell', destination: {kind: 'search'}};
    // Root33: Downloads never claims a file is on this device or locally playable; a failed download's Try again is there.
    case 'downloads': return {kind: 'downloads'};
    case 'recordings': return {kind: 'recordings'};
    case 'server-feedback': return {kind: 'server-feedback'};
    case 'settings': return {kind: 'shell', destination: {kind: 'settings'}};
    // Review security, and "New device signed in" → Review device: Profile & account holds the password and the devices.
    case 'account-security': case 'account-devices': return {kind: 'shell', destination: {kind: 'settings', page: 'profile'}};
    // The administrators' copies: the server's people and devices, Remote access, Storage.
    case 'server-people': return {kind: 'server-settings', page: 'people'};
    case 'server-network': return {kind: 'server-settings', page: 'remote'};
    case 'server-storage': return {kind: 'server-settings', page: 'storage'};
    case 'dashboard': return {kind: 'server-dashboard'};
    case 'person': return {kind: 'person', personId: intent.personId};
    // An item, a reporter's own report, or an in-place command: no route (commands: notificationCommand).
    default: return undefined;
  }
}

export function notificationCommand(action: NotificationAction, note?: Notification): NotificationCommandTarget | undefined {
  const intent = notificationIntent(action, note);
  if (intent?.kind === 'archive-notice') return {kind: 'archive'};
  if (intent?.kind === 'scan-library') return {kind: 'scan', libraryId: intent.libraryId};
  if (intent?.kind === 'own-feedback') return {kind: 'report', reportId: intent.reportId};
  return undefined;
}
