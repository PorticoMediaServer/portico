import type {NotificationAction} from '../../../../../packages/client-core/src/notifications';
import type {NativeShellRoute} from '../../shell/routes';

/** The real surface a notice action opens on this device, or nothing when this build has none
 * (the action is then left out rather than drawn disabled). */
export function notificationRoute(action: NotificationAction, hasLiveTV: boolean): NativeShellRoute | undefined {
  if (action.kind === 'command') {
    if (action.command === 'open-download') return {kind: 'downloads'};
    // "New device signed in": Review device opens Settings, where this account's devices are.
    if (action.command === 'review-device' && action.arguments?.deviceId) return {kind: 'shell', destination: {kind: 'settings'}};
    return undefined;
  }
  const targetView = action.target?.view, libraryId = action.target?.libraryId, entityId = action.target?.entityId;
  switch (targetView) {
    case 'home': return {kind: 'shell', destination: {kind: 'home'}};
    case 'library': return libraryId ? {kind: 'shell', destination: {kind: 'library', libraryId}} : undefined;
    case 'live': return hasLiveTV ? {kind: 'shell', destination: {kind: 'live'}} : undefined;
    case 'search': return {kind: 'shell', destination: {kind: 'search'}};
    // Root33: a preparation-ready notice only points at the real Downloads
    // surface. It never claims the file is on this device or locally playable.
    case 'downloads': return {kind: 'downloads'};
    case 'recordings':
    case 'recording': return {kind: 'recordings'};
    case 'admin-feedback': return {kind: 'server-feedback'};
    case 'settings': return {kind: 'shell', destination: {kind: 'settings'}};
    // Review security: this account's password and two-step sign-in, in Settings.
    case 'settings-security': return {kind: 'shell', destination: {kind: 'settings'}};
    // The administrators' copy (Review devices): the server's people and their devices.
    case 'admin-devices': return {kind: 'server-settings', page: 'people'};
    case 'person': return entityId ? {kind: 'person', personId: entityId} : undefined;
    // A view this build has no real surface for stays out rather than guessing.
    default: return undefined;
  }
}
