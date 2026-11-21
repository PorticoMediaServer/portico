import {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {formatBytes} from '../../../../../../packages/client-core/src/presentation/content';
import type {ServerFact} from '../../../kit/shell';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeStorageSpaceSource} from '../../../screens/settings-live/server/storage/space-source';
import {useDashboardRead} from './useDashboardRead';

const noSubscribe = () => () => {};
const noSnapshot = () => undefined;

export function useTVDashboard(controller: NativeDashboardController, ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  const state = useDashboardRead(controller, ports);
  const {t} = runtime.text;
  const [storage, setStorage] = useState<NativeStorageSpaceSource>();
  const {context} = ports;
  const {reportError} = runtime;
  const owner = state.admission === 'owner';
  useLayoutEffect(() => {
    if (!owner) {setStorage(undefined); return;}
    const source = new NativeStorageSpaceSource(context, reportError);
    setStorage(source);
    return source.dispose;
  }, [context, owner, reportError]);
  const disk = useSyncExternalStore(storage?.subscribe ?? noSubscribe, storage?.getSnapshot ?? noSnapshot, storage?.getSnapshot ?? noSnapshot);
  useEffect(() => {if (storage) void storage.load();}, [storage]);

  const health = state.health;
  const facts: ServerFact[] = [];
  if (health && owner) facts.push({id: 'state', text: t(health.degraded ? 'next.shell.health.attention' : 'next.shell.health.normal'),
    tone: health.degraded ? 'warning' : 'normal'});
  if (health?.uptimeSeconds !== undefined) facts.push({id: 'uptime', text: t('settings.server.health.upFor', {duration: defaultI18n.duration(health.uptimeSeconds)})});
  if (health?.freeBytes !== undefined) facts.push({id: 'free', text: t('settings.server.health.free', {bytes: formatBytes(health.freeBytes)})});
  if (health?.databaseBytes !== undefined) facts.push({id: 'database', text: t('settings.server.health.database', {bytes: formatBytes(health.databaseBytes)})});
  const usage = disk?.phase === 'ready' ? disk.usage : undefined;
  const measurements = owner && usage ? [
    ...(health?.freeBytes === undefined ? [] : [{id: 'free', label: t('server.dashboard.freeSpace'), value: defaultI18n.bytes(health.freeBytes)}]),
    {id: 'own', label: t('server.dashboard.porticoFiles'), value: defaultI18n.bytes(usage.folders.reduce((sum, folder) => sum + folder.bytes, 0))},
    ...(health?.databaseBytes === undefined ? [] : [{id: 'database', label: t('server.space.database'), value: defaultI18n.bytes(health.databaseBytes)}]),
  ] : [];
  return {state, owner, facts, measurements};
}
