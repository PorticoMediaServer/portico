import {useLayoutEffect, useRef, useState} from 'react';
import {DocumentBootReady} from '../shell/DocumentBootReady';
import {ApiError} from '@core/index.ts';
import type {CapturedPorticoReturn} from '../platform/portico-return-dispatch';
import {acceptSecurityReturn, OriginalProviderSecurity, originalSecurityClient, cancelOriginalAccountSecurity} from '../platform/account-security';
import {browserSecurityJournal, type AccountSecurityPending, type HostedSecurityBridge} from '../platform/account-security-journal';
import {ServicesApp} from './ServicesApp';
import {HostedSecurityTask, SignedOutRecoveryCodes} from './SecurityPanel';
import {ProviderSecurityFactorFields} from '../screens/settings/account-profile/ProviderSecurityFactorFields';
import {AccountSecurityReturnFrame} from './AccountSecurityReturnFrame';
import {ServicesNotice} from '../kit/accounts/ServicesAuth';
import {OriginalAccountSecurityWorkspace} from './OriginalAccountSecurityWorkspace';
import {credentialTaskTitle} from './ProviderSecurityStartDialog';
import {servicesI18n} from './i18n';
import {beginHostedSecurityBridge, finishHostedSecurityBridge, cancelHostedSecurityBridge, type HostedSecurityArrival} from './account-security-return';

export type AccountSecurityEntry = Readonly<{kind: 'server'; captured: CapturedPorticoReturn} | {kind: 'hosted'; captured: HostedSecurityArrival}>;
const invalid = () => new Error('This account security continuation no longer matches its original opening.');

/** This fixed intake renders the original Account Settings credential task.
 * Ordinary app/bootstrap/session renewal starts only after the task is closed.
 * No LocalSession is created, admitted or replaced by a provider Security return. */
export function AccountSecurityReturnEntry({arrival}: {arrival: AccountSecurityEntry}) {
  const {t} = servicesI18n();
  const [pending, setPending] = useState<AccountSecurityPending>(), [proof, setProof] = useState<OriginalProviderSecurity>();
  const [factor, setFactor] = useState(''), [busy, setBusy] = useState(true), [error, setError] = useState<unknown>();
  const [ordinary, setOrdinary] = useState<{identityTransaction?: string; providerReturn?: boolean}>(), [ended, setEnded] = useState<{codes?: readonly string[]}>();
  type Opening = {live: boolean; mounted: boolean; busy: boolean; closed?: boolean; retired?: boolean; cancelTicket?: symbol; handedOff?: boolean; abort: AbortController; record?: AccountSecurityPending; bridge?: HostedSecurityBridge; retryFactor?: string};
  const life = useRef<Opening | undefined>(undefined);
  const originalDestination = () => arrival.kind === 'server' ? '/settings?scope=account&section=profile' : '/account?tab=security';
  const back = () => location.assign(originalDestination());
  const current = (o: Opening) => life.current === o && o.live && !o.retired && !o.abort.signal.aborted;
  const complete = async (original: Opening, record: AccountSecurityPending, input: string) => {
    if (!current(original)) return;
    original.busy = true; setBusy(true); setError(undefined); setFactor('');
    const captured = original.retryFactor ?? input;
    original.retryFactor = captured;
    try {
      const verified = await OriginalProviderSecurity.complete(record, captured, original.abort.signal);
      original.retryFactor = undefined;
      if (current(original)) {
        // The validated original UI context chooses this fixed Account page;
        // neither the callback nor Hosted supplies an arbitrary destination.
        history.replaceState(null, '', originalDestination());
        setProof(verified);
      }
    } catch (value) {
      if (value instanceof ApiError && !value.retryable && ['challenge_invalid', 'two_factor_invalid', 'two_factor_required'].includes(value.code)) original.retryFactor = undefined;
      if (current(original)) setError(value);
    } finally {original.busy = false; if (current(original)) setBusy(false);}
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({complete});
  useLayoutEffect(() => {setupPorts.current = {complete};});
  useLayoutEffect(() => {
    const {complete} = setupPorts.current;
    const original: Opening = {live: true, mounted: true, busy: true, abort: new AbortController()}; life.current = original;
    void (async () => {
      const signal = original.abort.signal, journal = browserSecurityJournal();
      if (arrival.kind === 'server') {
        const record = await acceptSecurityReturn(arrival.captured.href + arrival.captured.fragment, signal, journal, record => {original.record = record;});
        if (!current(original)) return;
        original.record = record; setPending(record);
        if (!record.original.state.mfaEnabled) await complete(original, record, '');
      } else {
        const captured = arrival.captured;
        if (captured.kind === 'invalid') throw invalid();
        if (captured.kind === 'begin') {await beginHostedSecurityBridge(captured.intentId, signal, journal, () => {original.handedOff = true;}, record => {original.bridge = record;}); return;}
        const record = await journal.read(); signal.throwIfAborted();
        if (record && !('original' in record)) {
          original.bridge = record;
          await finishHostedSecurityBridge(record, captured.transactionId, signal, journal, () => {original.handedOff = true;}); return;
        }
        if (record && 'original' in record) {
          if (record.original.kind !== 'hosted-account' || record.transactionId !== captured.transactionId || record.original.hostedOrigin !== location.origin) throw invalid();
          const tx = await originalSecurityClient(record.original.hostedOrigin).transaction(captured.transactionId, signal);
          if (!tx || tx.mode !== 'reauthenticate' || tx.securityReturn || tx.provider !== record.original.provider.provider || !['verified', 'proof_issued'].includes(tx.status)) throw invalid();
          original.record = record;
          if (current(original)) setPending(record);
          if (!record.original.state.mfaEnabled) await complete(original, record, '');
        } else {
          // Preserve ordinary provider login/link consumers. A missing protected
          // Security original can never fall through to either grant consumer.
          const tx = await originalSecurityClient(location.origin).transaction(captured.transactionId, signal);
          if (!tx || tx.securityReturn || tx.mode === 'reauthenticate') throw invalid();
          if (current(original)) {
            // Login callbacks share Hosted's /account callback URL with Security,
            // but resume the original sign-in/start root. Only linking stays here.
            if (tx.mode === 'login') history.replaceState(null, '', '/');
            setOrdinary(tx.mode === 'link' ? {identityTransaction: captured.transactionId} : {providerReturn: true});
          }
        }
      }
    })().catch(value => {if (current(original)) {original.handedOff=false;setError(value);}}).finally(() => {original.busy = false; if (current(original)) setBusy(false);});
    return () => {original.mounted = false;original.cancelTicket=undefined;original.live = false; original.abort.abort(); if (!original.closed && !original.handedOff) {if (original.record) void cancelOriginalAccountSecurity(original.record).catch(()=>undefined); if (original.bridge) void cancelHostedSecurityBridge(original.bridge).catch(()=>undefined);} if (life.current === original) life.current = undefined;};
  }, [arrival]);
  const cancel = async () => {
    const original = life.current;
    if (!original || !original.live || life.current!==original || original.busy) return;
    const ticket=Symbol('original account cancellation'), originalPage=location.href, destination=originalDestination();
    original.cancelTicket=ticket;
    const moved=()=>{original.cancelTicket=undefined;original.retired=true;};
    addEventListener('popstate',moved);addEventListener('pagehide',moved);
    original.closed = true;original.busy = true;original.live = false; original.abort.abort(); setBusy(true);setFactor('');
    try {
      if (original.record) await cancelOriginalAccountSecurity(original.record).catch(() => undefined);
      if (original.bridge) await cancelHostedSecurityBridge(original.bridge).catch(() => undefined);
      // live=false/abort above deliberately retire the operation, not its page.
      // Only this still-mounted close ticket can navigate that original page.
      if(life.current===original&&original.mounted&&!original.retired&&original.cancelTicket===ticket&&location.href===originalPage)location.assign(destination);
    } finally {removeEventListener('popstate',moved);removeEventListener('pagehide',moved);if(original.cancelTicket===ticket)original.cancelTicket=undefined;}
  };
  const opening = life.current;
  if (ordinary) return <ServicesApp identityTransaction={ordinary.identityTransaction} providerReturn={ordinary.providerReturn}/>;
  if (ended?.codes?.length) return <DocumentBootReady><SignedOutRecoveryCodes codes={ended.codes} onDone={back}/></DocumentBootReady>;
  if (ended) return <DocumentBootReady><AccountSecurityReturnFrame title={t('auth.welcomeHome')} busy={false} onCancel={back}><ServicesNotice>{t('services.signin.ended')}</ServicesNotice></AccountSecurityReturnFrame></DocumentBootReady>;
  if (proof) return <DocumentBootReady><OriginalAccountSecurityWorkspace proof={proof} onRetired={() => {if(!opening||life.current!==opening||!opening.mounted||opening.retired)return;opening.retired=true;opening.cancelTicket=undefined;opening.abort.abort();setProof(undefined);setPending(undefined);setBusy(false);setError(invalid());if(!opening.closed)void proof.close().catch(()=>undefined);}}><HostedSecurityTask provider={proof} identity={{accountId: proof.original.session.account.id, familyId: proof.original.session.familyId!}} state={proof.original.state} task={proof.original.intent.purpose}
    isCurrent={proof.current} onClose={() => {if(!opening||life.current!==opening||!opening.mounted||!opening.live||opening.retired||!proof.acceptsUIURL(location.href))return;opening.closed=true;back();}} onSecurityEnded={codes => {if(!opening||life.current!==opening||!opening.mounted||opening.retired)return;opening.closed=true;setProof(undefined); setEnded({...(codes ? {codes: Object.freeze([...codes])} : {})});}}/></OriginalAccountSecurityWorkspace></DocumentBootReady>;
  const needsFactor = pending?.original.state.mfaEnabled === true;
  return <DocumentBootReady ready={!busy || Boolean(error) || Boolean(pending)}><AccountSecurityReturnFrame title={pending ? credentialTaskTitle(pending.original.intent.purpose) : t('web.claim.loading')} error={error} busy={busy} onCancel={() => void cancel()}
    onContinue={pending ? () => {const original = life.current; if (original && current(original) && !original.busy) void complete(original, pending, factor);} : undefined}
    continueLabel={opening?.retryFactor !== undefined ? t('next.states.tryAgain') : t('action.continue')} continueDisabled={needsFactor && !opening?.retryFactor && !factor.trim()}>
    {needsFactor && <ProviderSecurityFactorFields value={factor} disabled={busy || opening?.retryFactor !== undefined} onChange={value => {const original = life.current; if (original && current(original) && !original.busy && original.retryFactor === undefined) setFactor(value);}}/>}
  </AccountSecurityReturnFrame></DocumentBootReady>;
}
