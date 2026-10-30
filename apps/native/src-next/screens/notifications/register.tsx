import {readEventState} from '../../platform/event-resync';
import React from 'react';
import {Platform} from 'react-native';
import {InboxService} from '../../../../../packages/client-core/src/inbox';
import {parseNotificationSummary} from '../../../../../packages/client-core/src/notifications';
import {routePath} from '../../../../../packages/contracts/src/routes';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import {NativeScreenLifetime} from '../settings-live/lifetime';
import {NativeNotificationsScreen} from './NotificationsScreen';

const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const invalid = () => Object.assign(new Error('invalid_notifications_response'), {code: 'invalid_response'});

export type NotificationsRetirement = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => Error | undefined;
}>;

type Session = Readonly<{
  route: NativeShellRoute;
  revision: number;
  lifetime: NativeScreenLifetime;
  profile: InboxService;
  admin: InboxService;
  retirement: NotificationsRetirement;
  retired: () => boolean;
  dispose: () => void;
}>;

/** One opened Notifications route owns one exact inbox session. A back/reopen gets a
 * fresh session at the new navigation revision; a fence refusal retires this one. */
export function createNativeNotificationsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  let session: Session | undefined;

  const create = (route: NativeShellRoute, revision: number): Session => {
    const lifetime = new NativeScreenLifetime(context);
    const authority = lifetime.authority;
    let fence: string | undefined, retired = false;
    let retirement: Error | undefined;
    const listeners = new Set<() => void>();
    const retirementStore: NotificationsRetirement = {
      subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
      getSnapshot: () => retirement,
    };
    let profile: InboxService | undefined, admin: InboxService | undefined;
    const retire = (error: Error) => {
      if (retired) return;
      retired = true;
      retirement = error;
      lifetime.dispose();
      profile?.dispose();
      admin?.dispose();
      for (const listener of [...listeners]) listener();
    };
    const scopeChanged = () => {
      const error = Object.assign(new Error('notifications_scope_changed'), {code: 'access_refused'});
      retire(error);
      return error;
    };
    const check = (raw: unknown, requestedAudience?: string) => {
      if (!object(raw) || !('data' in raw)) throw invalid();
      const scope = raw.scope;
      if (!object(scope)) throw invalid();
      if (typeof scope.serverId !== 'string' || typeof scope.viewerFence !== 'string') throw invalid();
      if (scope.serverId !== authority.serverId) throw scopeChanged();
      if (!/^[a-f0-9]{64}$/.test(scope.viewerFence)) throw invalid();
      if (fence !== undefined && fence !== scope.viewerFence) throw scopeChanged();
      fence = scope.viewerFence;
      if (requestedAudience !== undefined) {
        const data = raw.data;
        if (!object(data) || typeof data.audience !== 'string' || data.audience !== requestedAudience) throw invalid();
      }
    };
    const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (retired) throw scopeChanged();
      const raw = await lifetime.request(next => (!method || method === 'GET')
        ? readEventState(context.events, next, () => context.api.request<T>(path, method, body, next))
        : context.api.request<T>(path, method, body, next), signal);
      // A concurrent response may have retired this session while this read was
      // in flight. Never hand that raw body to the service or the retained screen.
      if (retired) throw scopeChanged();
      const route = path.split('?')[0];
      const query = new URLSearchParams(path.includes('?') ? path.slice(path.indexOf('?') + 1) : '');
      if (route === routePath('get_notifications_inbox')) check(raw, query.get('audience') ?? undefined);
      else if (route === routePath('get_notifications_unread_count')) {
        if (!object(raw)) throw invalid();
        parseNotificationSummary(raw.data);
        check(raw, query.get('audience') ?? undefined);
      } else if (route === routePath('post_notifications_inbox_actions')) {
        const audience = object(body) && typeof body.audience === 'string' ? body.audience : undefined;
        check(raw, audience);
      } else check(raw);
      return raw;
    }};
    const operationId = () => [...nativeRouteCrypto.random(16)].map(byte => byte.toString(16).padStart(2, '0')).join('');
    profile = new InboxService(api, operationId);
    admin = new InboxService(api, operationId);
    const dispose = () => {
      if (retired) return;
      retired = true;
      lifetime.dispose();
      profile?.dispose();
      admin?.dispose();
      listeners.clear();
    };
    return Object.freeze({route, revision, lifetime, profile: profile!, admin: admin!, retirement: retirementStore, retired: () => retired, dispose});
  };

  const acquire = (route: NativeShellRoute): Session => {
    const revision = context.navigation.getSnapshot().revision;
    if (!session || session.revision !== revision || session.route !== route) {
      session?.dispose();
      session = create(route, revision);
    }
    return session;
  };

  return {
    render: (route, runtime) => {
      const current = acquire(route);
      if (!current.retired()) current.lifetime.assert();
      if (route.kind !== 'notifications') throw new Error('native_notifications_route_mismatch');
      return <NativeNotificationsScreen context={context} route={route} runtime={runtime}
        profileService={current.profile} adminService={current.admin} retirement={current.retirement}/>;
    },
    dispose: () => {session?.dispose(); session = undefined;},
  };
}

// Touch/tablet only. The television's own settings menu is not changed by this route.
if (!Platform.isTV) nativeShellRoutes.registerRoute('notifications', createNativeNotificationsRenderer);
