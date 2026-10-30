import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {FormScrollView} from '../../platform/keyboard';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {passwordStrength} from '../../../../../packages/client-core/src/password-strength';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {ColourBackdrop} from '../../kit/artwork';
import {Button, TextField} from '../../kit/controls';
import {nativeType, useTheme} from '../../kit/theme';
import {qrScannerAvailable, scanQRCode} from '../../platform/qr-scanner';
import {subscribeNativeAppSessions} from '../../platform/app-session-storage';
import type {NativeMemberAccountMove} from '../../platform/member-account-move';

type FormTicket = {live: boolean; scope: object; scan?: AbortController};

/** Connect44b/c. The canonical code is a 43-character secret, not the short
 * illustrative frame data. The verified operation owns preview and redemption. */
export function PhoneMoveCode<Result>({controller, onBack, isCurrent}: {
  controller: NativeMemberAccountMove<Result>; onBack: () => void;
  /** Captured member controller and its original verified operation, never a successor. */
  isCurrent: () => boolean;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const [code, setCode] = useState(''), [username, setUsername] = useState(state.username ?? '');
  const [password, setPassword] = useState(''), [confirmation, setConfirmation] = useState(''), [shown, setShown] = useState(false), [invalidCode, setInvalidCode] = useState(false);
  useEffect(() => {if (state.username !== undefined) setUsername(state.username);}, [state.username]);
  const [scanning, setScanning] = useState(false), [scanError, setScanError] = useState<unknown>();
  const revision = useRef(0), submitted = useRef(false);
  const scope = useMemo(() => ({controller, phase: state.phase}), [controller, state.phase]);
  const active = useRef<FormTicket | undefined>(undefined), [committed, setCommitted] = useState<FormTicket>();
  const retire = (ticket: FormTicket) => {ticket.live = false; ticket.scan?.abort();};
  if (active.current && (active.current.scope !== scope || !isCurrent())) {
    const old = active.current; retire(old); if (active.current === old) active.current = undefined;
  }
  useLayoutEffect(() => {
    const ticket: FormTicket = {scope, live: true}; active.current = ticket;
    revision.current++; submitted.current = false; setPassword(''); setConfirmation(''); setShown(false); setScanning(false); setScanError(undefined); setCommitted(ticket);
    return () => {ticket.live = false; ticket.scan?.abort(); if (active.current === ticket) active.current = undefined;};
  }, [scope]);
  const current = () => !!committed?.live && committed.scope === scope && active.current === committed && isCurrent() && controller.isCurrentOpening();
  const draft = useMemo(() => Object.freeze({code, username, password, confirmation, shown}), [code, username, password, confirmation, shown]);
  const latestDraft = useRef(draft); latestDraft.current = draft;
  const renderedRevision = revision.current;
  const ownsDraft = () => current() && revision.current === renderedRevision && latestDraft.current === draft && controller.getSnapshot() === state;
  const edit = (change: () => void) => {
    if (!ownsDraft() || state.busy || submitted.current) return;
    committed?.scan?.abort(); revision.current++; setScanError(undefined); change();
  };
  const scan = async () => {
    if (!ownsDraft() || !committed || state.phase !== 'code' || state.busy || submitted.current || committed.scan) return;
    const opening = committed, abort = new AbortController(); opening.scan = abort; setScanning(true); setScanError(undefined);
    const guard = () => ownsDraft() && opening.scan === abort && !abort.signal.aborted;
    const stop = () => abort.abort();
    const offOwner = subscribeNativeAppSessions(stop), offController = controller.subscribe(stop);
    try {
      const raw = await scanQRCode({signal: abort.signal, isCurrent: guard, title: t('accounts.move.scanQR'), cancelLabel: t('action.cancel')});
      if (guard() && raw !== undefined) {revision.current++; setCode(raw); setInvalidCode(false);}
    } catch (error) {if (guard()) setScanError(error);}
    finally {
      offOwner(); offController();
      if (opening.scan === abort) opening.scan = undefined;
      if (opening.live && active.current === opening) setScanning(false);
    }
  };
  if (state.phase !== 'code' && state.phase !== 'local') return null;
  const local = state.phase === 'local', strength = passwordStrength(password);
  const strengthKey = strength.label === 'Too short' ? 'services.password.tooShort' : strength.label === 'Too long' ? 'accounts.password.tooLong'
    : strength.label === 'Weak' ? 'accounts.password.weak' : strength.label === 'Fair' ? 'accounts.password.fair'
      : strength.label === 'Good' ? 'accounts.password.good' : 'services.password.strong';
  const submit = () => {
    if (!ownsDraft() || state.busy || submitted.current || !committed) return;
    committed.scan?.abort();
    if (local) {
      if (!username || !strength.acceptable || password !== confirmation) return;
      const supplied = password; const opening = committed; submitted.current = true; revision.current++; setPassword(''); setConfirmation(''); setShown(false);
      void controller.redeemLocal(draft.username, supplied).finally(() => {if (opening.live && active.current === opening) submitted.current = false;});
    } else {
      const supplied = controller.readCodeInput(code);
      if (supplied === undefined) {setInvalidCode(true); return;}
      const opening = committed; submitted.current = true; revision.current++; setCode(''); setInvalidCode(false);
      void controller.previewCode(supplied).finally(() => {if (opening.live && active.current === opening) submitted.current = false;});
    }
  };
  const back = () => {if (current() && !state.busy && !submitted.current) {committed?.scan?.abort(); revision.current++;setCode(''); setPassword(''); setConfirmation(''); setShown(false); controller.cancel(); onBack();}};
  const passwordField = (confirm: boolean) => <View style={{marginBottom: 12}}>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary, marginBottom: 6}]}>{t(confirm ? 'accounts.signIn.confirmPassword' : 'accounts.signIn.password')}</Text>
    <View>
      <TextField accessibilityLabel={t(confirm ? 'accounts.signIn.confirmPassword' : 'accounts.signIn.password')}
        value={confirm ? confirmation : password} onChangeText={value => edit(() => {if (confirm) setConfirmation(value); else setPassword(value);})} editable={!state.busy}
        secureTextEntry={!shown} autoCapitalize="none" autoCorrect={false} autoComplete="new-password" textContentType="newPassword"
        onSubmitEditing={confirm ? submit : undefined} returnKeyType={confirm ? 'go' : 'next'}
        fieldStyle={{height: 48, borderRadius: 11, paddingRight: confirm ? 14 : 94}} style={{fontSize: 16 * theme.textScale}}/>
      {!confirm && <Button variant="text" icon={shown ? 'i-eyeoff' : 'i-eye'} iconSize={20} disabled={state.busy}
        accessibilityState={{selected: shown}} onPress={() => edit(() => setShown(value => !value))}
        style={{position: 'absolute', right: 6, top: 4, height: 40, paddingHorizontal: 8}} textStyle={{fontSize: 13 * theme.textScale}}>
        {t(shown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')}
      </Button>}
    </View>
    {!confirm && password.length > 0 && <Text style={[theme.type.caption, {marginTop: 6, color: strength.acceptable ? theme.colors.textSecondary : theme.colors.danger}]}>{t(strengthKey)}</Text>}
  </View>;
  return <View testID={local ? 'accounts-move-local-sign-in' : 'accounts-move-code'} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="top"/>
    <FormScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{flexGrow: 1, paddingHorizontal: 22,
      paddingTop: Math.max(104, insets.top + 54), paddingBottom: Math.max(30, insets.bottom + 16)}}>
      <Text accessibilityRole="header" style={[nativeType({size: 34, lineHeight: 35.36, weight: 800, tracking: -.04}, theme.textScale),
        {color: theme.colors.text, marginBottom: 8}]}>{t(local ? 'accounts.move.chooseSignIn' : 'accounts.move.enterCode')}</Text>
      {local && <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 20}]}>{t('accounts.move.profilesKept')}</Text>}
      {state.error != null && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 12}]}>{presentError(state.error, 'sign-in').body}</Text>}
      {local ? <>
        <TextField label={t('accounts.signIn.username')} value={username} onChangeText={value => edit(() => setUsername(value))} editable={!state.busy}
          autoCapitalize="none" autoCorrect={false} autoComplete="username" textContentType="username"
          fieldStyle={{height: 48, borderRadius: 11}} style={{fontSize: 16 * theme.textScale}} containerStyle={{marginBottom: 12}}/>
        {passwordField(false)}{passwordField(true)}
        {confirmation.length > 0 && confirmation !== password && <Text style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 12}]}>{t('accounts.signIn.passwordsDiffer')}</Text>}
      </> : <TextField label={t('accounts.move.code')} value={code} onChangeText={value => edit(() => {setCode(value); setInvalidCode(false);})}
        editable={!state.busy} autoCapitalize="none" autoCorrect={false} onSubmitEditing={submit} returnKeyType="go"
        error={invalidCode ? t('accounts.move.codeInvalid') : undefined} fieldStyle={{height: 48, borderRadius: 11}} style={{fontSize: 16 * theme.textScale}}/>}
      <Button disabled={state.busy || (local ? !username || !strength.acceptable || password !== confirmation : !code)} loading={state.busy || undefined}
        onPress={submit} style={{height: 48, borderRadius: 11, alignSelf: 'stretch', marginTop: 20}} textStyle={{fontSize: 16 * theme.textScale}}>{t('accounts.signIn.continue')}</Button>
      {!local && qrScannerAvailable() && <Button variant="text" disabled={state.busy || scanning} loading={scanning || undefined} style={{marginTop: 10}} onPress={() => {void scan();}}>{t('accounts.move.scanQR')}</Button>}
      {scanError != null && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginTop: 10}]}>{presentError(scanError, 'sign-in').body}</Text>}
    </FormScrollView>
    <View style={{position: 'absolute', top: Math.max(50, insets.top), left: 12, height: 48, justifyContent: 'center'}}>
      <Button variant="text" icon="i-left" iconSize={22} disabled={state.busy} onPress={back}>{t('accounts.signIn.back')}</Button>
    </View>
  </View>;
}
