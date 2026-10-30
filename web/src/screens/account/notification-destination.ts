import {notificationPreparationTarget, type Notification, type NotificationAction} from '@core/notifications.ts';

export type NoticeDestination = Readonly<{view: 'home'}> | Readonly<{view: 'settings'}> | Readonly<{view: 'dashboard'}>
  | Readonly<{view: 'library'; id: string}> | Readonly<{view: 'item'; id: string}> | Readonly<{view: 'downloads'}>;
/** Explicit navigation and the strict Root33 preparation command only. Opening
 * Downloads confers no save/transfer or completed-offline authority. */
export function notificationDestination(action: NotificationAction, libraries: readonly {id: string}[], canManageServer: boolean, note?: Notification): NoticeDestination | undefined {
  if (note && notificationPreparationTarget(note, action)) return {view: 'downloads'};
  if (action.kind !== 'navigate' || !action.target) return;
  const {view, entityId, libraryId} = action.target;
  if (view === 'home' || view === 'settings') return {view};
  if (view === 'server' && canManageServer) return {view: 'dashboard'};
  if (view === 'library' && libraryId && libraries.some(library => library.id === libraryId)) return {view: 'library', id: libraryId};
  if (view === 'item' && entityId) return {view: 'item', id: entityId};
}
