import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {NativeStorageSpaceSource} from './storage/space-source';

/** TV35's Disk usage: the volume's free space, Portico's own data-folder files
 * and the database, from the server's own observations. It draws only facts the
 * server reported; an unavailable measurement is left out. */
export function TVDiskUsage({context, runtime, freeBytes, databaseBytes}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; freeBytes?: number; databaseBytes?: number}) {
  const theme = useTheme(), t = runtime.text.t;
  const [source, setSource] = useState<NativeStorageSpaceSource>();
  const reportError = runtime.reportError;
  useLayoutEffect(() => {const next = new NativeStorageSpaceSource(context, reportError); setSource(next); return next.dispose;}, [context, reportError]);
  if (!source) return null;
  return <Usage source={source} runtime={runtime} freeBytes={freeBytes} databaseBytes={databaseBytes} theme={theme} t={t}/>;
}

function Usage({source, runtime, freeBytes, databaseBytes, theme, t}: {source: NativeStorageSpaceSource; runtime: NativeShellRuntime; freeBytes?: number; databaseBytes?: number; theme: ReturnType<typeof useTheme>; t: NativeShellRuntime['text']['t']}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  useEffect(() => {void source.load();}, [source]);
  if (state.phase !== 'ready' || !state.usage) return null;
  const own = state.usage.folders.reduce((sum, folder) => sum + folder.bytes, 0);
  const rows = [
    freeBytes === undefined ? undefined : {id: 'free', label: t('server.dashboard.freeSpace'), value: defaultI18n.bytes(freeBytes)},
    {id: 'own', label: t('server.dashboard.porticoFiles'), value: defaultI18n.bytes(own)},
    databaseBytes === undefined ? undefined : {id: 'database', label: t('server.space.database'), value: defaultI18n.bytes(databaseBytes)},
  ].filter((row): row is {id: string; label: string; value: string} => row !== undefined);
  if (!rows.length) return null;
  return <View style={{marginTop: 36}}>
    <Text accessibilityRole="header" style={[shellType(20, 700), {color: theme.colors.textSecondary, marginBottom: 14}]}>{t('settings.server.diskUsage')}</Text>
    {rows.map(row => <View key={row.id} style={{flexDirection: 'row', justifyContent: 'space-between', gap: 20, padding: 22, marginBottom: 3, borderRadius: 12, backgroundColor: theme.colors.raised}}>
      <Text style={[shellType(24, 600), {color: theme.colors.text}]}>{row.label}</Text>
      <Text style={[shellType(22, 600), {color: theme.colors.textSecondary}]}>{row.value}</Text>
    </View>)}
  </View>;
}
