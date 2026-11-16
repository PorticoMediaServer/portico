import React, {useCallback, useEffect, useMemo, useState} from 'react';
import {Text, View} from 'react-native';
import {readNoticesKeep, saveNoticesKeep, type NoticesKeep} from '../../../../../../../packages/client-core/src/server-admin/server-storage';
import {withGuardedRaw} from '../../../../../../../packages/client-core/src/home-selection/list-destinations';
import {settingsRowIndex} from '../../../../../../../packages/client-core/src/presentation/settings-spec';
import type {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Select} from '../../../../kit/controls';
import {SettingsRow} from '../../../../kit/shell';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import {screenScopeEnded} from '../../lifetime';
import {useSettingsRequestScope} from '../../use-settings-request-scope';

type Translate = typeof defaultI18n.t;
const row = (id: string) => settingsRowIndex.get(id)!.row;

/** Storage & backups › Advanced: notification retention and the Messages settings link. */
export function StorageKeepRows({context, t, onOpenPage}: {context: NativeShellBootstrapContext; t: Translate; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const theme = useTheme(), {api, lifetime} = useSettingsRequestScope(context);
  const documents = useMemo(() => withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}), [api, context, lifetime]);
  const [notices, setNotices] = useState<NoticesKeep>(), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const load = useCallback(() => {
    void readNoticesKeep(api).then(value => {if (lifetime.current()) setNotices(value);}, () => {if (lifetime.current()) setFailed(true);});
  }, [api, lifetime]);
  useEffect(load, [load]);
  const run = async (work: () => Promise<unknown>) => {
    if (busy || !lifetime.current()) return;
    setBusy(true); setFailed(false);
    try {await work();} catch {if (lifetime.current()) setFailed(true);}
    finally {if (lifetime.current()) {setBusy(false); load();}}
  };
  const choices = (id: string) => (row(id).control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)}));
  const notification = row('storage.keepNotifications');
  return <View style={{gap: 8}}>
    {notices && <SettingsRow first label={t(notification.label)} description={notification.help ? t(notification.help) : undefined}
      control={<Select testID="storage.keepNotifications" accessibilityLabel={t(notification.label)} value={String(notices.days)} disabled={busy} options={choices('storage.keepNotifications')}
        onValueChange={value => {void run(() => saveNoticesKeep(documents, notices, Number(value)));}}/>}/>}
    <SettingsRow navigates label={t(row('storage.keepLogsLink').label)} onPress={() => onOpenPage('troubleshooting')}/>
    {failed && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{t('error.unavailable')}</Text>}
  </View>;
}
