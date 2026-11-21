import {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {LibraryManagementService} from '../../../../../../packages/client-core/src/library-management';
import {useFocusRegistry} from '../../../kit/focus';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import {publicTVHandoffAddress} from '../../../tv/auth/TVHandoff';
import {useScreenEntry} from '../../../screens/settings-live/Screen';
import {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {TVServerPage} from '../../../screens/settings-live/server/tv-server-pages';
import {createTVServerReader, nativeServerAdmission} from '../../../screens/settings-live/server/tv-server-reader';

export function useTVServerSettings({ports, runtime, initialPage}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; initialPage: TVServerPage}) {
  const [page, setPage] = useState<TVServerPage>(initialPage);
  const lifetime = useMemo(() => new NativeScreenLifetime(ports.context), [ports]);
  const admission = nativeServerAdmission(ports.context.account, lifetime);
  const role: 'owner' | 'administrator' = admission === 'owner' ? 'owner' : 'administrator';
  const owner = admission === 'owner';
  // Dashboard and People are shared with administrators; the other read-only
  // settings retain their existing owner admission.
  const audience = owner && page !== 'people' && page !== 'dashboard' ? 'owner' : 'administrators';
  const service = useMemo(() => new LibraryManagementService({
    api: createTVServerReader(ports.context, lifetime),
    scope: {serverId: lifetime.authority.serverId, viewerId: JSON.stringify([lifetime.authority.epoch, lifetime.authority.accountId, lifetime.authority.profileId])},
  }), [ports, lifetime]);
  const state = useSyncExternalStore(service.subscribe, service.getSnapshot).directory;
  const ids = useMemo(() => tvFocusIds(lifetime.authority), [lifetime]);
  const column = ids.surface('server.settings.pages'), rows = ids.surface(`server.settings.${page}`);
  const registry = useFocusRegistry();
  // W-14: Back from anywhere in a page's content (its rows or a panel's own regions) returns to
  // that page in the column before Back leaves server settings.
  useEffect(() => runtime.registerTVRouteBack?.(() => {
    const focused = registry.capture()?.regionId;
    return !!focused && focused !== column && focused !== ids.rail && lifetime.current() && registry.focus({regionId: column, childId: page});
  }, 'server-settings'), [runtime, registry, column, ids, page, lifetime]);
  const enter = useScreenEntry({regionId: column, childId: page}, lifetime.current);
  useEffect(() => {
    const retire = () => service.cancel();
    lifetime.signal.addEventListener('abort', retire, {once: true});
    // Libraries is the owner's page; an administrator's screen never asks the
    // catalogue reader for it, and the reader would refuse that path anyway.
    if (owner) void service.loadLibraries();
    return () => {lifetime.signal.removeEventListener('abort', retire); service.dispose(); lifetime.dispose();};
  }, [owner, service, lifetime]);
  const url = useMemo(() => {
    if (page !== 'libraries' && page !== 'remote') return undefined;
    try {return publicTVHandoffAddress(new URL(page === 'libraries' ? '/settings/server-libraries' : '/settings/server-remote', ports.context.api.baseUrl).href).url;} catch {return undefined;}
  }, [ports, page]);
  function select(next: TVServerPage) {
    if (lifetime.current() && (owner || next === 'dashboard' || next === 'people')) setPage(next);
  }
  return {page, role, audience, lifetime, state, service, column, rows, rail: ids.rail, enter, url, select} as const;
}
