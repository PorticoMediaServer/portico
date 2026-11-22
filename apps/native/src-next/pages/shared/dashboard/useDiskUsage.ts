import {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeStorageSpaceSource} from '../../../screens/settings-live/server/storage/space-source';

export type DiskUsageRow = Readonly<{id: string; label: string; value: string}>;
const nothing = () => () => {}, none = () => undefined;

/** TV35's Disk usage: the volume's free space, Portico's own data-folder files and the database, from the server's own
 * observations. Only facts the server reported are listed; an unavailable measurement is left out, and nothing is listed
 * until the folders are read. */
export function useDiskUsage({enabled, context, runtime, freeBytes, databaseBytes}: {
  /** Only the owner's Dashboard reads storage; an administrator's never asks. */
  enabled: boolean; context: NativeShellBootstrapContext; runtime: NativeShellRuntime; freeBytes?: number; databaseBytes?: number;
}): readonly DiskUsageRow[] | undefined {
  const t = runtime.text.t, reportError = runtime.reportError;
  const [source, setSource] = useState<NativeStorageSpaceSource>();
  useLayoutEffect(() => {
    if (!enabled) {setSource(undefined); return;}
    const next = new NativeStorageSpaceSource(context, reportError); setSource(next); return next.dispose;
  }, [enabled, context, reportError]);
  const state = useSyncExternalStore(source?.subscribe ?? nothing, source?.getSnapshot ?? none, source?.getSnapshot ?? none);
  useEffect(() => {if (source) void source.load();}, [source]);
  if (!enabled || !state || state.phase !== 'ready' || !state.usage) return undefined;
  const own = state.usage.folders.reduce((sum, folder) => sum + folder.bytes, 0);
  const rows = [
    freeBytes === undefined ? undefined : {id: 'free', label: t('server.dashboard.freeSpace'), value: defaultI18n.bytes(freeBytes)},
    {id: 'own', label: t('server.dashboard.porticoFiles'), value: defaultI18n.bytes(own)},
    databaseBytes === undefined ? undefined : {id: 'database', label: t('server.space.database'), value: defaultI18n.bytes(databaseBytes)},
  ].filter((row): row is DiskUsageRow => row !== undefined);
  return rows.length ? rows : undefined;
}
