import {readEventState} from '../../platform/event-resync';
import React from 'react';
import {Platform} from 'react-native';
import {InboxService} from '../../../../../packages/client-core/src/inbox';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import {NativeScreenLifetime} from '../settings-live/lifetime';
import {NativeNotificationsScreen} from './NotificationsScreen';
import {viewerQueriesFor} from '../../shell/freshness';

export type NotificationsRetirement = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => Error | undefined;
}>;

type Session = Readonly<{
  route: NativeShellRoute;
  revision: number;
  lifetime: NativeScreenLifetime;
  profile: InboxService;
  retirement: NotificationsRetirement;
  retired: () => boolean;
  dispose: () => void;
}>;

/** One opened Notifications route owns one exact inbox session. A back/reopen gets a
 * fresh session at the new navigation revision (its views come from the viewer's cache);
 * an answer from another server retires this one. */
export function createNativeNotificationsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  let session: Session | undefined;

  const create = (route: NativeShellRoute, revision: number): Session => {
    const lifetime = new NativeScreenLifetime(context);
    const authority = lifetime.authority;
    let retired = false;
    let retirement: Error | undefined;
    const listeners = new Set<() => void>();
    const retirementStore: NotificationsRetirement = {
      subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
      getSnapshot: () => retirement,
    };
    let profile: InboxService | undefined;
    const retire = (error: Error) => {
      if (retired) return;
      retired = true;
      retirement = error;
      lifetime.dispose();
      profile?.dispose();
      for (const listener of [...listeners]) listener();
    };
    const scopeChanged = () => {
      const error = Object.assign(new Error('notifications_scope_changed'), {code: 'access_refused'});
      retire(error);
      return error;
    };
    const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (retired) throw scopeChanged();
      const raw = await lifetime.request(next => (!method || method === 'GET')
        ? readEventState(context.events, next, () => context.api.request<T>(path, method, body, next))
        : context.api.request<T>(path, method, body, next), signal);
      // A concurrent response may have retired this session while this read was
      // in flight. Never hand that raw body to the service or the retained screen.
      if (retired) throw scopeChanged();
      // The rebuilt inbox answers a plain page (and empty successful writes).
      // This captured API and lifetime own the viewer; the core reader owns the response shape.
      return raw;
    }};
    const operationId = () => [...nativeRouteCrypto.random(16)].map(byte => byte.toString(16).padStart(2, '0')).join('');
    // The profile's inbox includes its server notices and shares the shell's viewer cache.
    const queries = viewerQueriesFor(context.api, {serverId: authority.serverId});
    // Shared first pages use the shell's viewer-bound requester. Continuations and writes
    // use this screen's lifetime; closing one screen must not abort another screen's shared read.
    const onScopeChanged = () => {scopeChanged();};
    profile = new InboxService(api, operationId, {queries, onScopeChanged});
    const dispose = () => {
      if (retired) return;
      retired = true;
      lifetime.dispose();
      profile?.dispose();
      listeners.clear();
    };
    return Object.freeze({route, revision, lifetime, profile: profile!, retirement: retirementStore, retired: () => retired, dispose});
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
        profileService={current.profile} retirement={current.retirement}/>;
    },
    dispose: () => {session?.dispose(); session = undefined;},
  };
}

// Touch/tablet only. The television's own settings menu is not changed by this route.
if (!Platform.isTV) nativeShellRoutes.registerRoute('notifications', createNativeNotificationsRenderer);
