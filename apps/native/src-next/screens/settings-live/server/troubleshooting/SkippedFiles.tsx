import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {readSkippedFiles, type SkippedFile} from '../../../../../../../packages/client-core/src/admin/skipped-files';
import type {NativeSettingsLivePorts} from '../../ports';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {NativeScreenLifetime} from '../../lifetime';
import {Button} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {messagesReader, troubleshootingApi} from './Messages';

type Read = Readonly<{items: readonly SkippedFile[]; next?: string; loading: boolean; failed: boolean}>;

/** Files Portico could not prepare (console rules 1 and 72), for the owner and administrators: an administrator sees
 *  those of the libraries they are allowed. Each file with its library and the server's reason. */
export function NativeSkippedFiles({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const context = ports.context, theme = useTheme(), t = runtime.text.t;
  const [lifetime, setLifetime] = useState<NativeScreenLifetime>();
  const [read, setRead] = useState<Read>({items: [], loading: true, failed: false});
  useLayoutEffect(() => {const next = new NativeScreenLifetime(context); setLifetime(next); return () => next.dispose();}, [context]);
  const load = (life: NativeScreenLifetime, cursor?: string) => {
    const current = () => life.current() && messagesReader(context);
    if (!current()) return;
    setRead(before => ({...before, loading: true, failed: false}));
    const api = troubleshootingApi(context, life, current);
    void readSkippedFiles(api, cursor).then(page => {if (current()) setRead(before => ({items: cursor ? [...before.items, ...page.items] : page.items, next: page.nextCursor, loading: false, failed: false}));},
      () => {if (current()) setRead(before => ({...before, loading: false, failed: true}));});
  };
  // The first read goes through the latest `load`, published at commit.
  const ports_ = useRef({load});
  useEffect(() => {ports_.current = {load};});
  useEffect(() => {if (lifetime) ports_.current.load(lifetime);}, [lifetime]);
  return <View style={{marginTop: 14, gap: 12}} testID="server-troubleshooting-skipped">
    <Text style={[shellType(17, 800), {color: theme.colors.text}]}>{t('next.settings.troubleshootingStuckFiles')}</Text>
    <Text style={[shellType(13, 400, 18), {color: theme.colors.textSecondary}]}>{t('next.settings.troubleshootingStuckFilesHelp')}</Text>
    {read.failed && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{t('error.unavailable')}</Text>}
    {read.loading && !read.items.length && <Text style={{color: theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}
    {!read.loading && !read.failed && !read.items.length && <Text style={{color: theme.colors.textSecondary}}>{t('next.settings.stuckFilesNone')}</Text>}
    {read.items.map((file, index) => <View key={`${file.libraryId}:${file.path}:${index}`} style={{gap: 3, padding: 12, borderRadius: 12, backgroundColor: theme.colors.raised}}>
      <Text selectable style={[shellType(13.5, 600, 19), {color: theme.colors.text}]}>{file.path}</Text>
      {file.libraryName ? <Text style={{color: theme.colors.textTertiary}}>{file.libraryName}</Text> : null}
      <Text style={{color: theme.colors.textSecondary}}>{file.message}</Text>
    </View>)}
    {read.next && <Button size="small" variant="glass" disabled={read.loading} onPress={() => {if (lifetime) load(lifetime, read.next);}}>{t('server.showMore')}</Button>}
  </View>;
}
