import {defaultI18n} from '../../../../packages/i18n/src';
import type {DirectSnapshot} from '../../../../packages/client-core/src/profile-management';
import type {IconName} from '../kit/Icon';
import {nativeShellRoutes} from './route-registry';
import type {NativeShellSnapshot} from './runtime';
import type {NativeShellRoute} from './routes';

type MenuEntry = Readonly<{id: string; label: string; icon: IconName; onPress: () => void; route: NativeShellRoute}>;

/** A route this build can render right now. An action that would open a blank
 * page is not offered: it is hidden until its screen registers. */
export function nativeMenuRouteAvailable(route: NativeShellRoute): boolean {
  return route.kind === 'shell' ? nativeShellRoutes.hasDestination(route.destination.kind) : nativeShellRoutes.hasRoute(route.kind);
}

/** All actions address the verified current account. No name, profile or role is fabricated.
 * The projection itself is pure; the shell passes the registered-route check. */
export function nativeAccountMenu(account: DirectSnapshot, open: (route: NativeShellRoute) => void, tv = false,
  available: (route: NativeShellRoute) => boolean = () => true): NativeShellSnapshot['account'] {
  const t = defaultI18n.t;
  const viewerRole = account.account.role === 'admin' ? 'administrator' : account.account.role;
  const entries: MenuEntry[] = [
    ...(!tv || account.profiles.length > 1 ? [{id: 'profiles', label: t('settings.switchProfile'), icon: 'i-switch' as const, route: {kind: 'profiles' as const}, onPress: () => open({kind: 'profiles'})}] : []),
    {id: 'servers', label: t('settings.switchServer'), icon: 'i-server' as const, route: {kind: 'servers' as const}, onPress: () => open({kind: 'servers'})},
    {id: 'notifications', label: t('notifications.title'), icon: 'i-bell' as const, route: {kind: 'notifications' as const}, onPress: () => open({kind: 'notifications'})},
    {id: 'together', label: t('entry.group.together'), icon: 'i-people' as const, route: {kind: 'together' as const}, onPress: () => open({kind: 'together'})},
    ...(!tv ? [{id: 'settings', label: t('settings.title'), icon: 'i-settings' as const, route: {kind: 'shell' as const, destination: {kind: 'settings' as const}}, onPress: () => open({kind: 'shell', destination: {kind: 'settings'}})}] : []),
  ];
  const dashboard: NativeShellRoute = tv ? {kind: 'server-settings'} : {kind: 'server-dashboard'};
  return Object.freeze({accountLine: account.account.username, viewerRole,
    actions: Object.freeze(entries.filter(entry => available(entry.route)).map(({route: _route, ...action}) => action)),
    ...((viewerRole === 'owner' || viewerRole === 'administrator') && available(dashboard) ? {serverAction: {id: 'server', label: t(tv ? 'next.shell.serverSettings' : 'settings.server.dashboard'), icon: 'i-server' as const, onPress: () => open(dashboard)}} : {}),
  });
}
