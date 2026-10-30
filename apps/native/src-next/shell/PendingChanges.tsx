import React, {useCallback, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import type {UserMutationRecoveryDestination, UserMutationRecoveryRow} from '../../../../packages/client-core/src/user-mutation-recovery';
import {Button, TextField} from '../kit/controls';
import {BottomSheet} from '../kit/overlays';
import {StateView} from '../kit/states';
import {useTheme} from '../kit/theme';
import type {NativePendingChanges} from './pending-changes';
import type {NativeShellRuntime} from './runtime';

const {t} = defaultI18n;
const categoryLabels = {saved: 'saved.title', settings: 'settings.title', playback: 'server.feedback.category.playback'} as const;
const statusLabels = {waiting: 'recovery.status.waiting', review: 'recovery.status.review', done: 'recovery.status.done'} as const;

export function PendingChanges({owner, runtime, openDestination}: Readonly<{
  owner: NativePendingChanges; runtime: NativeShellRuntime; openDestination: (destination: UserMutationRecoveryDestination) => void;
}>) {
  const open = useSyncExternalStore(owner.subscribe, owner.getSnapshot);
  // Unmount the inputs immediately when closed or retired. The sheet's own
  // animation is never allowed to retain secret fields from the old opening.
  return open ? <PendingChangesOpening key={open} owner={owner} runtime={runtime} openDestination={openDestination}/> : null;
}

export function PendingChangesOpening({owner, runtime, openDestination}: Readonly<{
  owner: NativePendingChanges; runtime: NativeShellRuntime; openDestination: (destination: UserMutationRecoveryDestination) => void;
}>) {
  const theme = useTheme(), model = owner.model;
  const state = useSyncExternalStore(model.subscribe, model.snapshot);
  const [busy, setBusy] = useState<string>(), [failure, setFailure] = useState<{error: unknown; key: string}>();
  const running = useRef(false);
  const signal = owner.signal();
  const current = () => owner.current() && !signal?.aborted && owner.signal() === signal;
  const run = async (key: string, work: () => Promise<void>) => {
    if (!current() || running.current) return;
    running.current = true; setBusy(key); setFailure(undefined);
    try {await work();}
    catch (error) {if (current()) setFailure({error, key});}
    finally {running.current = false; if (current()) setBusy(undefined);}
  };
  return <BottomSheet open title={t('recovery.title')} testID="native-pending-changes" keyboardAvoiding
    showClose={false} onOpenChange={open => {if (!open && current()) owner.close();}}
    footer={<Button variant="text" onPress={() => {if (current()) owner.close();}}>{t('action.close')}</Button>}>
    <View style={{gap: 20, paddingHorizontal: theme.isTV ? 0 : 16, paddingVertical: 16}}>
      {state.rows.map(row => <PendingChangeRow key={row.key} row={row} busy={!!busy} isCurrent={current}
        checkAgain={() => {void run(row.key, () => model.checkAgain(row.key));}}
        review={() => {void run(row.key, async () => {
          const destination = await model.review(row.key, signal);
          if (current()) {owner.close(); openDestination(destination);}
        });}}
        keep={() => {void run(row.key, () => model.keepAsIs(row.key));}}
        reenter={values => {void run(row.key, () => model.reenter(row.key, values, signal));}}/>)}
      {failure && <StateView {...runtime.error(failure.error, () => {void run(failure.key, () => model.checkAgain(failure.key));}, 'settings')} variant="inline"/>}
    </View>
  </BottomSheet>;
}

export function PendingChangeRow({row, busy, isCurrent, checkAgain, review, keep, reenter}: Readonly<{
  row: UserMutationRecoveryRow; busy: boolean; isCurrent: () => boolean; checkAgain: () => void; review: () => void; keep: () => void;
  reenter: (values: Readonly<Record<string, string>>) => void;
}>) {
  const theme = useTheme();
  const [editing, setEditing] = useState(false), [values, setValues] = useState<Record<string, string>>({});
  const formToken = useRef<object | undefined>(undefined), input = useRef<Record<string, string>>({}), blocked = useRef(busy);
  blocked.current = busy;
  const token = editing ? formToken.current : undefined;
  const currentForm = () => !!token && formToken.current === token && isCurrent() && !blocked.current;
  const clear = useCallback(() => {formToken.current = undefined; input.current = {}; setValues({}); setEditing(false);}, []);
  const fields = row.secretFields.map(field => field.path).join('\n');
  useEffect(() => {clear(); return () => {formToken.current = undefined; input.current = {};};}, [row.key, row.status, fields, clear]);
  return <View style={{gap: 12, padding: 16, borderRadius: 14, backgroundColor: theme.colors.raisedHigh}}>
    <View style={{gap: 4}}>
      <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(categoryLabels[row.category])}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{defaultI18n.dateTime(row.createdMs)}</Text>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{t(statusLabels[row.status])}</Text>
    </View>
    {row.status === 'review' && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('recovery.reviewNote')}</Text>}
    {!!row.secretFields.length && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('recovery.reentryNote')}</Text>}
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10}}>
      <Button variant="glass" disabled={busy} onPress={checkAgain}>{t('recovery.checkAgain')}</Button>
      <Button variant="glass" disabled={busy} onPress={review}>{t('recovery.review')}</Button>
      {row.canKeep && <Button variant="text" disabled={busy} onPress={keep}>{t('recovery.keepAsIs')}</Button>}
      {!!row.secretFields.length && !editing && <Button variant="glass" disabled={busy} onPress={() => {if (isCurrent() && !blocked.current) {formToken.current = {}; input.current = {}; setValues({}); setEditing(true);}}}>{t('recovery.reenter')}</Button>}
    </View>
    {editing && <View style={{gap: 12}}>
      {row.secretFields.map(field => <TextField key={field.path} label={t(field.labelKey)} value={values[field.path] ?? ''}
        maxLength={field.maxLength} secureTextEntry autoCapitalize="none" autoCorrect={false} textContentType="none"
        editable={!busy} onChangeText={value => {if (currentForm()) {input.current = {...input.current, [field.path]: value}; setValues(input.current);}}}/>)}
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10}}>
        <Button disabled={busy || row.secretFields.some(field => !values[field.path])} onPress={() => {
          if (!currentForm() || row.secretFields.some(field => !input.current[field.path])) return;
          const submitted = input.current; clear(); reenter(submitted);
        }}>{t('action.save')}</Button>
        <Button variant="text" disabled={busy} onPress={() => {if (currentForm()) clear();}}>{t('action.cancel')}</Button>
      </View>
    </View>}
  </View>;
}
