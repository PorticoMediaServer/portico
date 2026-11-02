import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Text} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {nativeManageGate, nativeSessionLifecycle, unlockNativeManagement} from '../../platform/native-session-lifecycle';
import {Dialog} from '../../kit/overlays';
import {Button} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {PhonePINEntry} from './PhonePINEntry';
import {profilePinFailureText} from './profile-pin-failure';

const t = defaultI18n.t;

/**
 * PR-1, phone and tablet: a remembered main profile past its management hour is refused on every management request
 * (profiles, People, server settings). This asks for the open profile's PIN when that happens, whichever screen asked;
 * the PIN chooses the same profile again, which opens management for the hour and reopens the shell on that sign-in.
 * Watching never needs it. TV manages on a phone or computer, so it never mounts this.
 */
export function ManageUnlockHost() {
  const theme = useTheme(), {lifecycle} = nativeSessionLifecycle();
  const refusals = useSyncExternalStore(nativeManageGate.subscribe, nativeManageGate.getSnapshot, nativeManageGate.getSnapshot);
  const state = useSyncExternalStore(lifecycle.subscribe, () => lifecycle.getSnapshot().state, () => lifecycle.getSnapshot().state);
  const [open, setOpen] = useState(false), [pin, setPin] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const heard = useRef(refusals), alive = useRef(true);
  useEffect(() => () => {alive.current = false;}, []);
  useEffect(() => {
    if (refusals === heard.current) return;
    heard.current = refusals;
    if (!busy) {setPin(''); setError(undefined); setOpen(true);}
  }, [refusals]); // eslint-disable-line react-hooks/exhaustive-deps -- a new refusal is the only trigger
  if (state.kind !== 'ready') return null;
  const familyId = state.viewer.familyId, profile = state.profiles.find(value => value.id === state.viewer.profileId);
  const submit = async (value: string) => {
    if (busy || !/^\d{4}$/.test(value)) return;
    setBusy(true); setError(undefined);
    try {await unlockNativeManagement(familyId, value); if (alive.current) setOpen(false);}
    catch (refusal) {if (alive.current) {setError(refusal); setPin('');}}
    finally {if (alive.current) setBusy(false);}
  };
  const failure = error ? profilePinFailureText(error) ?? presentError(error, 'profiles', {operation: 'action'}).body : undefined;
  return <Dialog testID="manage-unlock" open={open && state.manageLocked} onOpenChange={next => {if (!next && !busy) setOpen(false);}}
    title={t('profile.pinTitle', {name: profile?.name ?? ''})} description={t('error.profilePinManage')}
    dismissOnBack={!busy} dismissOnGesture={!busy} dismissOnOutsidePress={!busy}
    footer={<Button focusId="cancel" variant="glass" disabled={busy} onPress={() => setOpen(false)}>{t('action.cancel')}</Button>}>
    <PhonePINEntry testID="manage-unlock-pin" value={pin} onChange={setPin} onComplete={value => {void submit(value);}} busy={busy} error={!!error}/>
    {failure ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginTop: 8}]}>{failure}</Text> : null}
  </Dialog>;
}
