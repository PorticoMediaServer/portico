import {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {NativeModules} from 'react-native';
import {type MessageId} from '../../../../../../packages/i18n/src';
import {providerOnlySignIn, type SecurityState} from '../../../../../../packages/client-core/src/hosted-security';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {copyText} from '../../../platform/clipboard';
import type {NativeAccountWorkspace} from '../../../platform/account-workspace';
import type {NativeAccountSecurity, NativeAccountSecurityKind, NativeAccountSecuritySnapshot} from '../../../platform/account-security';

const idle: NativeAccountSecuritySnapshot = {step: 'form', busy: false};
const noSubscribe = () => () => {};
const idleSnapshot = () => idle;
export const accountSecurityTitles: Record<NativeAccountSecurityKind, MessageId> = {
  password: 'account.changePasswordTitle', email: 'web.hostedSecurity.changeEmailTitle', 'mfa-on': 'web.twoStep.turnOnTitle',
  'mfa-off': 'web.twoStep.turnOffTitle', codes: 'web.hostedSecurity.newCodesTitle', export: 'web.account.export.button', delete: 'web.deleteAccount.row',
};

/** Pure availability shared by the account workspace and server Settings. */
export function accountSecurityAvailability(security: Pick<SecurityState, 'hasPassword' | 'providers'>,
  bridge: Readonly<{canOpenBrowser?: boolean; canSignInApple?: boolean}> | undefined) {
  // Root §70: a Google- or Apple-only account confirms credential changes with that provider.
  const providerProof = !security.hasPassword && security.providers.length > 0 && (bridge?.canOpenBrowser === true || bridge?.canSignInApple === true);
  return {providerProof, credentialLocked: !security.hasPassword && !providerProof};
}

/** Account-only security uses the same scoped service with or without a reachable server. Moved from the old screen. */
export function useAccountSecurity({workspace, security}: {workspace: NativeAccountWorkspace; security: SecurityState}) {
  const [source, setSource] = useState<NativeAccountSecurity>();
  const [unavailable, setUnavailable] = useState<string>();
  // A Google- or Apple-only account (Justin, 1 Nov): Add a password waits behind More sign-in options.
  const [moreSignIn, setMoreSignIn] = useState(false);
  // Add a password and Two-step's "Set a password" open the hosted reset page after one plain line (#3392).
  const [resetAsk, setResetAsk] = useState(false);
  useLayoutEffect(() => {
    if (!workspace.current()) return;
    let controller: NativeAccountSecurity;
    try {controller = workspace.securityActions();} catch (error) {setUnavailable(presentError(error, 'portico-account').body); return;}
    setUnavailable(undefined); setSource(controller);
    return () => {controller.dispose(); setSource(value => value === controller ? undefined : value);};
  }, [workspace, security]);
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? idleSnapshot);
  const current = () => !!source?.current() && source.getSnapshot() === state;
  const {credentialLocked} = accountSecurityAvailability(security, NativeModules.PorticoAccountAuth);
  return {source, unavailable, credentialLocked, providerOnly: providerOnlySignIn(security),
    moreSignIn, showMoreSignIn: () => setMoreSignIn(true), resetAsk, askReset: () => setResetAsk(true), closeReset: () => setResetAsk(false),
    open: (kind: NativeAccountSecurityKind) => {if (current()) source!.open(kind);},
    finished: () => {void workspace.open('security');}};
}

/** The canonical account-owned task, including provider confirmation and its factor step. Moved from the old screen. */
export function useAccountSecuritySheet({security, source, onFinished}: {security: SecurityState; source?: NativeAccountSecurity; onFinished: () => void}) {
  const [password, setPassword] = useState(''), [factor, setFactor] = useState(''), [newPassword, setNewPassword] = useState('');
  const [email, setEmail] = useState(''), [code, setCode] = useState(''), [copyFailure, setCopyFailure] = useState(false);
  // Deletion: the password (and code) typed for it, then one final irreversible confirmation (#3392).
  const [finalDelete, setFinalDelete] = useState(false);
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? idleSnapshot);
  const current = () => !!source?.current() && source.getSnapshot() === state;
  const clear = () => {setPassword(''); setFactor(''); setNewPassword(''); setEmail(''); setCode(''); setCopyFailure(false); setFinalDelete(false);};
  useLayoutEffect(() => {
    setPassword(''); setFactor(''); setNewPassword(''); setEmail(''); setCode(''); setCopyFailure(false); setFinalDelete(false);
  }, [source, state.kind]);
  // A refused deletion goes back to the form so the password or code can be typed again.
  useLayoutEffect(() => {if (state.failure) setFinalDelete(false);}, [state.failure]);
  const close = () => {if (!current() || state.busy) return; const refresh = state.step !== 'form'; clear(); source!.close(); if (refresh) onFinished();};
  const credentials = !!state.kind && !['export', 'delete'].includes(state.kind) && state.step === 'form';
  const deleting = state.kind === 'delete' && state.step === 'form';
  const providerForm = credentials && !security.hasPassword;
  const providerInput = state.kind === 'password' ? !!newPassword : state.kind === 'email' ? !!email.trim() : true;
  const complete = state.step === 'codes' || state.step === 'done' || state.step === 'export';
  const submitDisabled = deleting ? !password || security.mfaEnabled && !factor.trim()
    : state.step === 'email' || state.step === 'mfa' || state.step === 'factor' ? !code.trim()
      : credentials ? !password || security.mfaEnabled && !factor.trim()
        || state.kind === 'password' && !newPassword || state.kind === 'email' && !email.trim() : false;
  return {state, close, credentials, deleting, providerForm, providerInput, complete, submitDisabled, editable: !state.busy && !state.locked,
    password, setPassword, factor, setFactor, newPassword, setNewPassword, email, setEmail, code, setCode, copyFailure, finalDelete,
    title: state.kind ? (state.kind === 'password' && !security.hasPassword ? 'account.setPassword' as const : accountSecurityTitles[state.kind]) : undefined,
    toFinalDelete: () => {if (current()) setFinalDelete(true);},
    deleteAccount: () => {if (!current()) return; void source!.deleteAccount(password, factor.trim());},
    submit: () => {if (!current()) return; void source!.submit({password, factor, newPassword, email, code});},
    confirmWithProvider: (id: string) => {if (!current()) return; void source!.confirmWithProvider(id, {newPassword, email});},
    copyCodes: () => {
      if (!current()) return;
      const captured = state.result;
      void copyText(captured?.recoveryCodes?.join('\n') ?? '').catch(() => {if (source?.current() && source.getSnapshot().result === captured) setCopyFailure(true);});
    }};
}

/** Add a password, wherever it is offered: one plain line, then the hosted reset page. It stays busy while the person is
 * there; on return the adapter checks the sign-in. Moved from the old screen. */
export function usePasswordReset({source, open, onClose, onFinished}: {source?: NativeAccountSecurity; open: boolean; onClose: () => void; onFinished: () => void}) {
  const [busy, setBusy] = useState(false), [failure, setFailure] = useState<string>();
  useLayoutEffect(() => {if (!open) {setBusy(false); setFailure(undefined);}}, [open]);
  const go = async () => {
    if (busy || !source?.current()) return;
    setBusy(true); setFailure(undefined);
    try {await source.openPasswordReset(); onClose(); onFinished();}
    catch (error) {setFailure(presentError(error, 'portico-account').body);}
    finally {setBusy(false);}
  };
  return {busy, failure, go: () => {void go();}};
}
