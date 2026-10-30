import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {ApiError} from '@core/index.ts';
import {moveOfferDays} from '@core/sign-in.ts';
import type {TakeAccountMove} from '../../route-registry-arrival';
import type {TakeAccountReturn} from '../../route-registry-return';
import {router} from '../../router';
import {profileSettingsDestination} from '../../route-registry-destination';
import {hasTwoStepSetup} from '../../platform/two-step-continuation';
import type {BrowserAccountEntry} from '../../platform/account-entry';
import {browserAppSessions} from '../../platform/server-connections';
import {browserAccountOrigin} from '../../platform/account-origin';
import {moveReconnected, rememberMoveReconnected} from '../../platform/move-reconnected';
import {AuthError, AuthStage, LocalServerSignIn, PorticoServerSignIn} from '../../kit/accounts';
import {RequiredPasswordDialog} from '../../kit/accounts/RequiredSetup';
import {ServerChallenge} from '../../kit/accounts/ServerChallenge';
import {Button} from '../../kit/controls';
import {AccountSignInForm} from './sign-in-form';
import {ShortInvitationEntry} from './ShortInvitationEntry';
import {canOpenShortInvitation} from './short-invitation-join';
import {initializeAccountSignIn} from './sign-in-start';
import {RequiredFactorDialogs} from './RequiredSetup';
import {LocalPasswordRecoveryFlow} from './LocalPasswordRecoveryFlow';
import {PorticoOwnerRecoveryFlow} from './PorticoOwnerRecoveryFlow';
import {AccountIdentityFailure} from './AccountIdentityFailure';
import {MemberMoveForm} from './MemberMoveForm';
import {localAccountStorage, readRememberedLocalAccounts, rememberLocalAccount, rememberedLocalKey, type RememberedLocalAccount} from './remembered-local-accounts';

type RecoveryTarget = Readonly<{entry: BrowserAccountEntry; origin: string; serverId: string; pin: NonNullable<ReturnType<BrowserAccountEntry['getSnapshot']>['pin']>}>;

export function AccountSignInView({entry, form, takeReturn, takeMove, initialize = true, initialOrigin, onCancel}: {entry: BrowserAccountEntry; form: AccountSignInForm; takeReturn?: TakeAccountReturn; takeMove?: TakeAccountMove; initialize?: boolean; initialOrigin?: string; onCancel?: () => void}) {
  const state = useSyncExternalStore(entry.subscribe, entry.getSnapshot), input = useSyncExternalStore(form.subscribe, form.getSnapshot);
  const [error, setError] = useState<unknown>(), [initializing, setInitializing] = useState(initialize), [joining, setJoining] = useState(false), started = useRef(false), alive = useRef(true), joinOrigin = useRef<string | undefined>(undefined);
  const rememberedKey = state.server?.signIn === 'local' && state.pin?.serverId === state.server.id ? rememberedLocalKey(state.pin) : undefined;
  const [remembered, setRemembered] = useState<Readonly<{key: string; accounts: readonly RememberedLocalAccount[]}>>();
  useEffect(() => {
    if (!rememberedKey) {setRemembered(undefined);return;}
    const read = () => setRemembered({key: rememberedKey, accounts: readRememberedLocalAccounts(localAccountStorage(), rememberedKey)});
    const changed = (event: StorageEvent) => {if (event.key === rememberedKey || event.key === null) read();};
    read();window.addEventListener('storage', changed);
    return () => window.removeEventListener('storage', changed);
  }, [rememberedKey]);
  useEffect(() => {
    if (!rememberedKey || entry.getSnapshot() !== state || state.busy || state.ownerResetSessionReady || input.factor || input.busy
      || state.phase !== 'profiles' && state.phase !== 'ready' || !state.snapshot || state.snapshot.twoStepSetupRequired
      || state.snapshot.serverId !== state.server?.id || state.snapshot.account.disabled) return;
    const {id: accountId, username} = state.snapshot.account;
    rememberLocalAccount(localAccountStorage(), rememberedKey, {accountId, username});
  }, [entry, state, input, rememberedKey]);
  // A browser that has reconnected to this server after its move never offers the old sign-in there again.
  const reconnectedServer = state.memberMoveCompleted ? state.server?.id : undefined;
  useEffect(() => {if (reconnectedServer) rememberMoveReconnected(reconnectedServer);}, [reconnectedServer]);
  const [recoveryChoice, setRecoveryChoice] = useState(false);
  const [recovery, setRecovery] = useState<RecoveryTarget>();
  const [porticoRecovery, setPorticoRecovery] = useState<RecoveryTarget>();
  const [acknowledgedMove, setAcknowledgedMove] = useState<ReturnType<BrowserAccountEntry['getSnapshot']>>();
  const [memberView, setMemberView] = useState<Readonly<{mode: 'old-password' | 'code'; target: RecoveryTarget}>>();
  const pageOrigin = initialOrigin ?? import.meta.env.VITE_SERVER_URL ?? location.origin;
  useEffect(() => {
    alive.current = true;
    if (initialize && !started.current) {
      started.current = true;
      void initializeAccountSignIn(entry, {takeReturn, takeMove, pageOrigin, isCurrent: () => alive.current,
        selectedOrigin: async () => (await browserAppSessions().selected())?.connection.logicalOrigin, username: value => form.setUsername(value),
      }).catch(failure => {if (alive.current) setError(failure);}).finally(() => {if (alive.current) setInitializing(false);});
    }
    return () => {alive.current = false;};
  }, [entry, form, takeReturn, takeMove, pageOrigin, initialize]);
  useEffect(() => {
    if (joining || initializing || entry.getSnapshot() !== state || input.factor || input.busy || state.memberMoveCompleted && !state.snapshot?.twoStepSetupRequired && (state.phase === 'profiles' || state.phase === 'ready') && acknowledgedMove !== state) return;
    if (state.phase === 'ready' && !state.ownerResetSessionReady) void router.navigate(state.setupCompleted ? {to: '/setup', replace: true}
      : state.server && state.snapshot && hasTwoStepSetup(state.server.id, state.snapshot.account.id) ? {...profileSettingsDestination, replace: true} : {to: '/', replace: true});
    else if (state.phase === 'profiles' && state.snapshot && !state.snapshot.twoStepSetupRequired) void router.navigate({to: '/profiles', replace: true});
    else if (state.phase === 'two-step-setup' && state.origin) {
      form.setUsername(state.snapshot?.account.username ?? input.username);
      void entry.inspect(state.origin);
    }
  }, [entry, form, state, input, initializing, joining, acknowledgedMove]);
  const join = () => {
    const current = entry.getSnapshot(), currentForm = form.getSnapshot();
    if (!canOpenShortInvitation(current, state, currentForm, alive.current, joining, initializing)) return;
    joinOrigin.current = current.origin ?? pageOrigin;
    form.clear();
    entry.cancel();
    setJoining(true);
  };
  const leaveJoin = () => {
    setJoining(false);
    const origin = joinOrigin.current ?? pageOrigin;
    joinOrigin.current = undefined;
    void entry.inspect(origin);
  };
  const reauthenticate = () => {
    if (!alive.current || entry.getSnapshot() !== state || form.getSnapshot() !== input) return;
    setRecovery(undefined); setPorticoRecovery(undefined);
    setMemberView(undefined);
    const origin = entry.getSnapshot().origin ?? pageOrigin;
    form.clear(); setError(undefined); void entry.inspect(origin);
  };
  const cancel = () => {if (!alive.current || entry.getSnapshot() !== state || form.getSnapshot() !== input) return;form.clear(); entry.cancel(); if (onCancel) onCancel(); else void router.navigate({to: '/servers'});};
  const busy = state.busy || input.busy, failure = state.error ?? error;
  const openRecovery = () => {
    if (!alive.current || busy || joining || initializing || entry.getSnapshot() !== state || form.getSnapshot() !== input
      || state.phase !== 'password' || state.server?.signIn !== 'local' || state.invitation || !state.origin || !state.pin) return;
    setError(undefined);
    setRecoveryChoice(true);
  };
  const openRecoveryFromFile = () => {
    if (!alive.current || busy || entry.getSnapshot() !== state || form.getSnapshot() !== input || state.phase !== 'password' || !state.origin || !state.pin || !state.server) return;
    form.clear(); setError(undefined); setRecoveryChoice(false);
    setRecovery({entry, origin: state.origin, serverId: state.server.id, pin: state.pin});
  };
  const recoveryCurrent = recovery?.entry === entry && recovery.origin === state.origin && recovery.serverId === state.server?.id
    && recovery.pin === state.pin && state.server?.signIn === 'local' && !state.invitation;
  const porticoRecoveryCurrent = porticoRecovery?.entry === entry && porticoRecovery.origin === state.origin && porticoRecovery.serverId === state.server?.id
    && porticoRecovery.pin === state.pin && state.server?.signIn === 'portico' && !state.invitation;
  const openPorticoRecovery = () => {
    if (!alive.current || busy || joining || initializing || entry.getSnapshot() !== state || form.getSnapshot() !== input
      || state.phase !== 'portico' || state.server?.signIn !== 'portico' || state.invitation || !state.origin || !state.pin) return;
    form.clear(); setError(undefined);
    setPorticoRecovery({entry, origin: state.origin, serverId: state.server.id, pin: state.pin});
  };
  const currentMember = memberView?.target.entry === entry && memberView.target.origin === state.origin && memberView.target.serverId === state.server?.id
    && memberView.target.pin === state.pin && !state.invitation;
  const openMember = (mode: 'old-password' | 'code') => {
    if (!alive.current || busy || initializing || joining || entry.getSnapshot() !== state || form.getSnapshot() !== input || !state.origin || !state.pin || !state.server || state.invitation) return;
    if (mode === 'old-password' ? state.phase !== 'portico' || state.server.move !== 'to_portico' : state.phase !== 'password' || state.server.move !== 'to_local') return;
    form.clear(); setRecovery(undefined); setError(undefined);
    setMemberView({mode, target: {entry, origin: state.origin, serverId: state.server.id, pin: state.pin}});
  };
  const runMove = (command: () => Promise<unknown>) => {
    if (!alive.current || busy || entry.getSnapshot() !== state || form.getSnapshot() !== input || state.invitation) return;
    void command().catch(failure => {if (alive.current && entry.getSnapshot().origin === state.origin && entry.getSnapshot().pin === state.pin) setError(failure);});
  };
  const continuePortico = () => {
    const epoch = input.inputEpoch, serverId = state.server?.id, origin = state.origin, pin = state.pin, phase = state.phase;
    if (!serverId || busy || !alive.current || entry.getSnapshot() !== state || form.getSnapshot() !== input || phase !== 'portico' && phase !== 'move-portico') return;
    void entry.preparePorticoSignIn(browserAccountOrigin(import.meta.env, location)).then(url => {
      const current = entry.getSnapshot();
      if (url && alive.current && form.getSnapshot().inputEpoch === epoch && current.phase === phase && current.server?.id === serverId && current.origin === origin && current.pin === pin) location.assign(url);
    }).catch(failure => {if (alive.current && form.getSnapshot().inputEpoch === epoch && entry.getSnapshot().origin === origin && entry.getSnapshot().pin === pin) setError(failure);});
  };
  const identityFailure = entry.serverIdentityFailure(() => {void router.navigate({to: '/servers'});});
  if (identityFailure) return <AccountIdentityFailure binding={identityFailure}/>;
  if (joining) return <ShortInvitationEntry onBack={leaveJoin} onJoined={destination => location.assign(destination.origin)} />;
  if (!input.factor && state.memberMoveCompleted && !state.busy && !state.error && state.snapshot && !state.snapshot.twoStepSetupRequired && (state.phase === 'profiles' || state.phase === 'ready') && acknowledgedMove !== state) return <AuthStage>
    <h1>{defaultI18n.t('accounts.move.linked')}</h1>
    <p>{defaultI18n.t('accounts.move.linkedBody')}</p>
    <Button variant="primary" disabled={busy} onClick={() => {
      if (alive.current && !busy && entry.getSnapshot() === state && form.getSnapshot() === input) setAcknowledgedMove(state);
    }}>{defaultI18n.t(state.phase === 'profiles' ? 'accounts.move.chooseProfile' : 'action.continue')}</Button>
  </AuthStage>;
  if (input.factor) return <RequiredFactorDialogs form={input.factor} error={failure} onCancel={cancel} onDone={() => {form.clear(); void router.navigate({to: '/profiles', replace: true});}} />;
  if (state.phase === 'password-change') return <RequiredPasswordDialog key={input.inputEpoch} busy={busy} error={failure}
    onChange={(current, next) => {form.clear(); void entry.changePassword(current, next);}} onCancel={cancel} />;
  if (state.phase === 'challenge') return <ServerChallenge key={input.inputEpoch} busy={busy} error={failure} onCode={code => void form.completeChallenge(code)} onBack={reauthenticate} />;
  if (failure instanceof ApiError && failure.code === 'move_collision' && failure.current) return <AuthStage>
    <h1>{defaultI18n.t('accounts.move.collision')}</h1>
    <p>{defaultI18n.t('accounts.move.collisionBody', {server: failure.current.serverName, username: failure.current.existingUsername})}</p>
    <Button variant="primary" disabled={busy} onClick={reauthenticate}>{defaultI18n.t('accounts.move.differentAccount')}</Button>
    <Button variant="ghost" disabled={busy} onClick={cancel}>{defaultI18n.t('action.back')}</Button>
  </AuthStage>;
  const moveMode = state.phase === 'move-local' ? 'local' : state.phase === 'move-portico' ? 'portico' : state.phase === 'move-code' ? 'code'
    : memberView && currentMember && (memberView.mode === 'old-password' ? state.phase === 'portico' : state.phase === 'password') ? memberView.mode : undefined;
  if (moveMode && state.server && !state.invitation) return <MemberMoveForm key={`${moveMode}:${input.inputEpoch}`} mode={moveMode} serverName={state.server.name}
    initialUsername={state.movePreview?.username ?? input.username} busy={busy} error={failure} onBack={reauthenticate}
    onCode={code => runMove(() => entry.previewMoveCode(code))} onOldPassword={(username, password) => runMove(() => entry.proveLocalMovePassword(username, password))}
    onLocal={(username, password) => runMove(() => form.redeemLocalMove(username, password))} onPortico={continuePortico}/>;
  if (porticoRecoveryCurrent && (state.phase === 'portico' || state.phase === 'ready' && state.ownerResetSessionReady)) return <PorticoOwnerRecoveryFlow entry={entry}
    onBack={() => {setPorticoRecovery(undefined); setError(undefined); form.clear();}}/>;
  if (recoveryCurrent && (state.phase === 'password' || state.phase === 'owner-reset-password')) return <LocalPasswordRecoveryFlow entry={entry} form={form}
    onBack={() => {setRecovery(undefined); setError(undefined); form.clear();}} />;
  if (recoveryChoice && !state.invitation && state.server?.signIn === 'local' && (state.phase === 'password' || state.phase === 'owner-reset-password')) return <AuthStage legal={false}>
    <h1>{defaultI18n.t('accounts.recovery.choiceTitle')}</h1>
    <p>{defaultI18n.t('accounts.recovery.choiceBody')}</p>
    <Button variant="primary" disabled={busy} onClick={openRecoveryFromFile}>{defaultI18n.t('accounts.recovery.choiceFile')}</Button>
    <p>{defaultI18n.t('accounts.recovery.choicePorticoHint')}</p>
    <Button variant="ghost" disabled={busy} onClick={() => setRecoveryChoice(false)}>{defaultI18n.t('action.cancel')}</Button>
  </AuthStage>;
  if (state.phase === 'password' && state.server?.signIn === 'local') return <LocalServerSignIn key={input.inputEpoch} serverName={state.server.name} initialUsername={input.username}
    remembered={!state.invitation && remembered?.key === rememberedKey ? remembered?.accounts : undefined}
    busy={busy} error={failure} retryAt={input.retryAt} onSignIn={(username, password) => {setError(undefined); void form.signIn(username, password);}} onJoin={join}
    onForgotPassword={state.invitation ? undefined : openRecovery}
    onMoveCode={state.server.move === 'to_local' && !state.invitation ? () => openMember('code') : undefined} />;
  if (state.phase === 'portico' && state.server?.signIn === 'portico') return <PorticoServerSignIn serverName={state.server.name} busy={busy} error={failure} onContinue={continuePortico}
    moveDays={state.server.move === 'to_portico' && !state.invitation && !moveReconnected(state.server.id) ? moveOfferDays(state.server.moveEndsAt) : undefined} onOldPassword={() => openMember('old-password')}
    onRecovery={state.invitation ? undefined : openPorticoRecovery}/>;
  if (!failure && (initializing || busy || state.phase === 'checking' || state.phase === 'profiles' || state.phase === 'ready')) return <AuthStage legal={false}>
    <p role="status">{defaultI18n.t(state.phase === 'profiles' || state.phase === 'ready' ? 'auth.loadingProfiles' : 'status.loading')}</p>
  </AuthStage>;
  return <AuthStage legal={false}><AuthError error={failure} />{Boolean(failure) && <>
    <Button onClick={reauthenticate}>{defaultI18n.t('action.tryAgain')}</Button>
    <Button onClick={cancel}>{defaultI18n.t('settings.switchServer')}</Button>
  </>}</AuthStage>;
}
