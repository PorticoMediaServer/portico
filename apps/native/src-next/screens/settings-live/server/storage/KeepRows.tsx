import React, {useCallback, useEffect, useMemo, useState} from 'react';
import {Text, View} from 'react-native';
import {KEEPING_PRESETS, readKeeping, readNoticesKeep, saveKeeping, saveNoticesKeep, type NoticesKeep, type StorageKeeping} from '../../../../../../../packages/client-core/src/server-admin/server-storage';
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

/** Storage & backups › Advanced › Keeping, on the rebuilt server: Play history and Messages keep their own settings on
 * their pages (links), notifications keep 7 to 180 days (notices document), and generated files keep Balanced,
 * Shorter or Longer (GET/PUT /v1/admin/storage/keeping). Each choice is saved when it is made, as on web. */
export function StorageKeepRows({context, t, onOpenPage}: {context: NativeShellBootstrapContext; t: Translate; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const theme = useTheme(), {api, lifetime} = useSettingsRequestScope(context);
  const documents = useMemo(() => withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}), [api, context, lifetime]);
  const [notices, setNotices] = useState<NoticesKeep>(), [keeping, setKeeping] = useState<StorageKeeping>(), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const load = useCallback(() => {
    void readNoticesKeep(api).then(value => {if (lifetime.current()) setNotices(value);}, () => {if (lifetime.current()) setFailed(true);});
    void readKeeping(api).then(value => {if (lifetime.current()) setKeeping(value);}, () => {if (lifetime.current()) setFailed(true);});
  }, [api, lifetime]);
  useEffect(load, [load]);
  const run = async (work: () => Promise<unknown>) => {
    if (busy || !lifetime.current()) return;
    setBusy(true); setFailed(false);
    try {await work();} catch {if (lifetime.current()) setFailed(true);}
    finally {if (lifetime.current()) {setBusy(false); load();}}
  };
  const choices = (id: string) => (row(id).control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)}));
  const notification = row('storage.keepNotifications'), files = row('storage.keepFiles');
  // Custom is the server's answer when the owner set kinds one by one elsewhere; it is shown, never chosen here.
  const fileChoices = choices('storage.keepFiles').filter(option => (KEEPING_PRESETS as readonly string[]).includes(option.value) || option.value === 'custom' && keeping?.preset === 'custom').map(option => option.value === 'custom' ? {...option, disabled: true} : option);
  return <View style={{gap: 8}}>
    <SettingsRow first navigates label={t(row('storage.keepHistory').label)} onPress={() => onOpenPage('history')}/>
    {notices && <SettingsRow label={t(notification.label)} description={notification.help ? t(notification.help) : undefined}
      control={<Select testID="storage.keepNotifications" accessibilityLabel={t(notification.label)} value={String(notices.days)} disabled={busy} options={choices('storage.keepNotifications')}
        onValueChange={value => {void run(() => saveNoticesKeep(documents, notices, Number(value)));}}/>}/>}
    <SettingsRow navigates label={t(row('storage.keepLogsLink').label)} onPress={() => onOpenPage('troubleshooting')}/>
    {keeping && <SettingsRow label={t(files.label)} description={files.help ? t(files.help) : undefined}
      control={<Select testID="storage.keepFiles" accessibilityLabel={t(files.label)} value={keeping.preset} disabled={busy}
        options={fileChoices}
        onValueChange={value => {if ((KEEPING_PRESETS as readonly string[]).includes(value)) void run(() => saveKeeping(api, {preset: value as typeof KEEPING_PRESETS[number]}));}}/>}/>}
    {failed && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{t('error.unavailable')}</Text>}
  </View>;
}
