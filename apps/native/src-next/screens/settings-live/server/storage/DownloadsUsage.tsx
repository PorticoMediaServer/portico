import React, {useCallback, useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {clearPreparedCopies, readDownloadsByPerson, type DownloadsUsage} from '../../../../../../../packages/client-core/src/server-admin/downloads-usage-source';
import type {ServerRequester} from '../../../../../../../packages/client-core/src/query/server-api';
import {BottomSheet} from '../../../../kit/overlays';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {Button} from '../../../../kit/controls';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {nativeStorageOwner} from './space-source';

/** Each person's prepared downloads on this server, the total and the room left under the limit (`GET /v1/admin/downloads`). */
export function NativeDownloadsUsage({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const context = ports.context, theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const [usage, setUsage] = useState<DownloadsUsage>(), [failed, setFailed] = useState(false), [attempt, setAttempt] = useState(0);
  const retry = useCallback(() => setAttempt(value => value + 1), []);
  // Clear prepared copies, one person at a time (downloads rule 47): the confirm, the transfer refusal in the sheet, the done line.
  const apiRef = useRef<ServerRequester | undefined>(undefined);
  const [clearing, setClearing] = useState<DownloadsUsage['items'][number]>(), [busy, setBusy] = useState(false), [refusal, setRefusal] = useState<'in-use' | 'failed'>(), [cleared, setCleared] = useState(false);
  useEffect(() => {
    const lifetime = new NativeScreenLifetime(context);
    const current = () => lifetime.current() && nativeStorageOwner(context);
    const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
      ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
    apiRef.current = api; setFailed(false);
    readDownloadsByPerson(api).then(value => {if (current()) setUsage(value);}, () => {if (current()) setFailed(true);});
    return () => lifetime.dispose();
  }, [context, attempt]);
  const clear = async () => {
    const api = apiRef.current;
    if (!clearing || busy || !api) return;
    setBusy(true); setRefusal(undefined);
    try {const outcome = await clearPreparedCopies(api, clearing.userId); if (outcome === 'in-use') setRefusal('in-use'); else {setClearing(undefined); setCleared(true); retry();}}
    catch {setRefusal('failed');}
    finally {setBusy(false);}
  };
  const people = usage?.items.filter(person => person.titles > 0 || person.copiesBytes > 0) ?? [];
  const line = (name: string, detail: string | undefined, bytes: number, key: string, action?: React.ReactNode) => <View key={key} style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: 12, padding: 12, backgroundColor: theme.colors.raised, borderRadius: 12}}>
    <View style={{flex: 1, minWidth: 0}}><Text style={[shellType(15, 600), {color: theme.colors.text}]}>{name}</Text>{detail ? <Text style={[shellType(13, 400), {color: theme.colors.textTertiary}]}>{detail}</Text> : null}</View>
    <Text style={[shellType(14.5, 600), {color: theme.colors.textSecondary}]}>{defaultI18n.bytes(bytes)}</Text>
    {action}
  </View>;
  return <View style={{marginTop: 22, gap: 12}} testID="server-storage-downloads">
    <Text style={[shellType(17, 800), {color: theme.colors.text}]}>{t('next.settingsSpec.server.storage.advanced.groups.2.rows.1.label')}</Text>
    <Text style={[shellType(13, 400, 18), {color: theme.colors.textSecondary}]}>{t('next.settingsSpec.server.storage.advanced.groups.2.rows.1.help')}</Text>
    {!usage && !failed && <Text style={[shellType(14, 400), {color: theme.colors.textSecondary}]}>{t('next.settings.loading')}</Text>}
    {failed && <Button size="compact" variant="glass" onPress={retry}>{t('action.tryAgain')}</Button>}
    {usage && line(t('server.downloadUsage.total'), undefined, usage.copiesBytes, 'total')}
    {usage && (people.length ? people.map(person => line(person.username, t('server.downloadUsage.titles', {count: person.titles}), person.copiesBytes, person.userId,
        person.copiesBytes > 0 ? <Button size="compact" variant="glass" onPress={() => {setRefusal(undefined); setCleared(false); setClearing(person);}}>{t('server.downloadUsage.clear')}</Button> : undefined))
      : <Text style={[shellType(14, 400), {color: theme.colors.textSecondary}]}>{t('server.downloadUsage.none')}</Text>)}
    {usage?.roomBytes !== undefined && <Text style={[shellType(13, 400), {color: theme.colors.textTertiary}]}>{t('server.downloadUsage.remaining', {size: defaultI18n.bytes(usage.roomBytes)})}</Text>}
    {cleared ? <Text style={[shellType(13, 400), {color: theme.colors.textSecondary}]}>{t('server.downloadUsage.cleared')}</Text> : null}
    <BottomSheet open={!!clearing} onOpenChange={open => {if (!open && !busy) setClearing(undefined);}} fitContent testID="server-storage-downloads-clear"
      title={clearing ? t('server.downloadUsage.clearTitle', {name: clearing.username}) : undefined} description={t('server.downloadUsage.removeBody')}
      footer={<View style={{gap: 10}}><Button loading={busy} disabled={busy} onPress={() => {void clear();}}>{t('server.downloadUsage.clear')}</Button><Button variant="glass" disabled={busy} onPress={() => setClearing(undefined)}>{t('next.settings.cancel')}</Button></View>}>
      {refusal === 'in-use' && clearing ? <Text accessibilityRole="alert" style={[shellType(14, 400), {color: theme.colors.record}]}>{t('server.downloadUsage.clearInUse', {name: clearing.username})}</Text> : null}
      {refusal === 'failed' ? <Text accessibilityRole="alert" style={[shellType(14, 400), {color: theme.colors.record}]}>{t('next.states.pageFailedBody')}</Text> : null}
    </BottomSheet>
  </View>;
}
