import React, {useLayoutEffect, useMemo, useState} from 'react';
import {Text, View} from 'react-native';
import type {BackupSettings, BackupSettingsPatch} from '../../../../../../../packages/client-core/src/backups';
import type {SettingsBackupSource} from '../../../../../../../packages/client-core/src/server-admin/backup-source';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button, Switch, TextField} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';

type Editor = Readonly<{abort: AbortController; settings: BackupSettings}>;
/** Passphrases live only in the exact open editor and its one submitted request. */
export function NativeBackupProtection({source, settings, current: ownerCurrent, failure, t}: {source: SettingsBackupSource; settings: BackupSettings; current: () => boolean; failure: (error: unknown) => void; t: typeof defaultI18n.t}) {
  const theme = useTheme(), [editor, setEditor] = useState<Editor>(), [secret, setSecret] = useState(''), [repeat, setRepeat] = useState(''), [busy, setBusy] = useState(false), [problem, setProblem] = useState(false), [uncertain, setUncertain] = useState(false);
  const view = useMemo(() => ({source, active: true, editor: undefined as Editor | undefined, flight: undefined as object | undefined, uncertain: false}), [source]);
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.editor?.abort.abort();view.editor = undefined;view.flight = undefined;};}, [view]);
  useLayoutEffect(() => {if (view.editor && view.editor.settings !== settings) {view.editor.abort.abort();view.editor = undefined;setEditor(undefined);setSecret('');setRepeat('');}}, [settings, view]);
  const current = () => view.active && view.source === source && ownerCurrent();
  const held = editor && view.editor === editor && !editor.abort.signal.aborted && current() ? editor : undefined;
  const close = () => {if (!held || !current() || view.editor !== held) return;view.editor = undefined;held.abort.abort();setEditor(undefined);setSecret('');setRepeat('');};
  const open = () => {if (!current() || view.flight || view.uncertain || source.getSnapshot().busy || source.getSnapshot().settings !== settings) return;view.editor?.abort.abort();const next = {abort: new AbortController(),settings};view.editor = next;setEditor(next);setSecret('');setRepeat('');setProblem(false);};
  const save = async (patch: BackupSettingsPatch, captured?: Editor) => {
    if (!current() || view.flight || view.uncertain || source.getSnapshot().busy || captured && (view.editor !== captured || captured.abort.signal.aborted || source.getSnapshot().settings !== captured.settings)) return;
    const flight = {};view.flight = flight;setBusy(true);setProblem(false);
    try {
      if (!current() || captured && (view.editor !== captured || captured.abort.signal.aborted)) return;
      const done = await source.save(Object.freeze({...patch}), captured?.abort.signal);
      if (!done && current()) {view.uncertain = true;setUncertain(true);setProblem(true);}
      if (current() && done && captured && view.editor === captured) {view.editor = undefined;captured.abort.abort();setEditor(undefined);setSecret('');setRepeat('');}
    } catch (error) {failure(error);if (current()) {view.uncertain = true;setUncertain(true);setProblem(true);}}
    finally {if (view.flight === flight) {view.flight = undefined;if (current()) setBusy(false);}}
  };
  const protect = () => {const captured = held;if (!captured || !current() || view.editor !== captured || captured.abort.signal.aborted || source.getSnapshot().settings !== captured.settings || !secret || secret !== repeat || view.flight) return;const passphrase = secret;setSecret('');setRepeat('');void save({passphraseProtected:true,passphrase},captured);};
  const reconcile = async () => {if (!current() || view.flight) return;const flight = {};view.flight = flight;setBusy(true);try {await source.load();if (current() && source.getSnapshot().settings) {view.uncertain = false;setUncertain(false);setProblem(false);}} catch (error) {failure(error);if (current()) setProblem(true);} finally {if (view.flight === flight) {view.flight = undefined;if (current()) setBusy(false);}}};
  return <View style={{gap:10}}>
    <Text style={[shellType(15,600),{color:theme.colors.text}]}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.help')}</Text>
    <Switch checked={settings.includeArtwork} disabled={busy || uncertain || source.getSnapshot().busy} onCheckedChange={includeArtwork => {if (current() && source.getSnapshot().settings === settings) void save({includeArtwork});}}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label')}</Switch>
    <Text style={[shellType(15,600),{color:theme.colors.text}]}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.help')}</Text>
    <Button size="compact" variant="glass" disabled={busy || uncertain || source.getSnapshot().busy} onPress={open}>{t(settings.passphraseSet ? 'next.settings.backup.changeTitle' : 'next.settings.backup.protectTitle')}</Button>
    <Switch checked={settings.passphraseProtected} disabled={busy || uncertain || source.getSnapshot().busy} onCheckedChange={checked => {if (!current() || source.getSnapshot().settings !== settings) return;if (checked) open();else void save({passphraseProtected:false});}}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label')}</Switch>
    {problem && <Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(uncertain ? 'error.unconfirmedChange' : 'error.unavailable')}</Text>}{uncertain && <Button size="compact" variant="glass" disabled={busy} onPress={() => {void reconcile();}}>{t('action.tryAgain')}</Button>}
    <BottomSheet open={!!held} onOpenChange={value => {if (!value) close();}} title={t(settings.passphraseSet ? 'next.settings.backup.changeTitle' : 'next.settings.backup.protectTitle')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button><Button disabled={busy || uncertain || !secret || secret !== repeat} loading={busy} onPress={protect}>{t('next.settings.backup.protect')}</Button></View>}><View style={{gap:12}}><TextField label={t('next.settings.backup.passphrase')} value={secret} secureTextEntry editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={value => {if (current() && view.editor === held && !view.flight) setSecret(value);}}/><TextField label={t('next.settings.backup.repeat')} value={repeat} secureTextEntry editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={value => {if (current() && view.editor === held && !view.flight) setRepeat(value);}}/>{repeat && repeat !== secret && <Text style={{color:theme.colors.record}}>{t('next.settings.backup.mismatch')}</Text>}</View></BottomSheet>
  </View>;
}
