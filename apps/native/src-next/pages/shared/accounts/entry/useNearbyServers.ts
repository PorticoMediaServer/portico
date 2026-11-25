import {useLayoutEffect, useRef, useState, type MutableRefObject} from 'react';
import {routeOrigin} from '../../../../../../../packages/client-core/src/route-identity';
import {ServerSignInProbe} from '../../../../../../../packages/client-core/src/sign-in';
import {browseNearby, nativeNetworkFetch} from '../../../../platform/server-connections';
import type {NativeAccountEntryStage, NearbyServerEntry} from '../../../../pages/shared/accounts/entry/entry-types';

/** Discovers nearby LAN servers while the entry flow is on the `address` stage. */
export function useNearbyServers(stage: NativeAccountEntryStage, stageRef: MutableRefObject<NativeAccountEntryStage>) {
  const [nearby, setNearby] = useState<readonly NearbyServerEntry[]>([]);
  const [nearbyStatus, setNearbyStatus] = useState<'loading' | 'ready' | 'failed'>('loading');
  const [nearbyRefresh, setNearbyRefresh] = useState(0);
  const nearbySource = useRef<{active: boolean} | undefined>(undefined);

  useLayoutEffect(() => {
    if (stage !== 'address') return;
    const ticket = {active: true}; nearbySource.current = ticket;
    const current = () => ticket.active && nearbySource.current === ticket && stageRef.current === 'address';
    setNearbyStatus('loading');
    const abort = new AbortController(), probes = new Set<ServerSignInProbe>();
    setNearby([]);
    void browseNearby('servers', abort.signal).then(async values => {
      if (!current() || abort.signal.aborted) return;
      const found = values.flatMap(value => {try {
        if (!value || typeof value !== 'object' || !('baseUrl' in value) || typeof value.baseUrl !== 'string') return [];
        if (!('serverId' in value) || typeof value.serverId !== 'string') return [];
        return [{origin: routeOrigin(value.baseUrl), serverId: value.serverId}];
      } catch {return [];}});
      // Discovery supplies addresses, not display names. Ask each server's public system
      // endpoint; never substitute a hostname or make a Portico Services request.
      const unique = found.filter((value, index) => found.findIndex(item => item.serverId === value.serverId) === index);
      for (let index = 0; index < unique.length && current() && !abort.signal.aborted; index += 4) {
        const rows = await Promise.all(unique.slice(index, index + 4).map(async value => {
          if (!current() || abort.signal.aborted) return;
          const probe = new ServerSignInProbe(nativeNetworkFetch, 3000); probes.add(probe);
          try {const descriptor = await probe.inspect(value.origin);
            return descriptor?.id === value.serverId ? {origin: value.origin, server: descriptor} : undefined;
          } finally {probe.dispose(); probes.delete(probe);}
        }));
        if (current() && !abort.signal.aborted) setNearby(previous => [...previous, ...rows.flatMap(value => value ? [value] : [])]);
      }
      if (current() && !abort.signal.aborted) setNearbyStatus('ready');
    }).catch(() => {if (current() && !abort.signal.aborted) setNearbyStatus('failed');});
    return () => {ticket.active = false; abort.abort(); for (const probe of probes) probe.dispose();};
  }, [stage, nearbyRefresh, stageRef]);

  return {nearby, nearbyStatus, setNearbyRefresh, nearbySource};
}
