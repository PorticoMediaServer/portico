import {useCallback, useEffect, useLayoutEffect, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction} from 'react';
import type {HostedServer} from '../../../../../../../packages/client-core/src';
import {presentError} from '../../../../../../../packages/client-core/src/presentation/errors';
import type {ServerSignIn} from '../../../../../../../packages/client-core/src/sign-in';
import {accountExpectation, nativeAccountRead} from '../../../../platform/account-work';
import {nativeAccounts} from '../../../../platform/accounts';
import {HostedSignInUnavailable} from '../../../../platform/hosted-sign-in-unavailable';
import type {NativePorticoSignIn} from '../../../../platform/portico-sign-in';
import {NativeServerSignIn} from '../../../../platform/server-sign-in';
import {directoryFailure, directoryRows, nextRetryDelay} from '../../../../screens/accounts/directory-chooser';
import {isTransientDirectoryFailure, isValidDirectoryResponse} from '../../../../pages/shared/accounts/entry/entry-helpers';
import type {
  EntryResult,
  InvitationIntent,
  InvitationSwitch,
  MemberMove,
  NativeAccountEntryStage,
} from '../../../../pages/shared/accounts/entry/entry-types';

type DirectoryFlowParams = {
  stage: NativeAccountEntryStage;
  busy: boolean;
  error: unknown;
  appActive: boolean;
  activeRef: MutableRefObject<boolean>;
  lifetime: MutableRefObject<boolean>;
  busyRef: MutableRefObject<boolean>;
  acting: MutableRefObject<boolean>;
  adoptingTV: MutableRefObject<boolean>;
  stageRef: MutableRefObject<NativeAccountEntryStage>;
  operation: MutableRefObject<NativeServerSignIn | undefined>;
  account: MutableRefObject<NativePorticoSignIn | undefined>;
  accountScope: MutableRefObject<{accountId: string; familyId: string} | undefined>;
  invitationSwitch: MutableRefObject<InvitationSwitch | undefined>;
  invitationIntent: MutableRefObject<InvitationIntent | undefined>;
  memberMoveRef: MutableRefObject<MemberMove | undefined>;
  memberOperation: MutableRefObject<NativeServerSignIn | undefined>;
  resetRef: MutableRefObject<() => void>;
  inspectRef: MutableRefObject<(origin?: string) => Promise<NativeServerSignIn | undefined>>;
  retireInvitationSwitch: () => void;
  retireOwnerRecoveryContact: () => void;
  retireOwnerReset: () => void;
  retireRecovery: () => boolean;
  consume: (result: EntryResult) => void;
  setStage: Dispatch<SetStateAction<NativeAccountEntryStage>>;
  setBusy: Dispatch<SetStateAction<boolean>>;
  setError: Dispatch<SetStateAction<unknown>>;
  setPassword: Dispatch<SetStateAction<string>>;
  setServer: Dispatch<SetStateAction<ServerSignIn | undefined>>;
  setAccountEnded: Dispatch<SetStateAction<boolean>>;
};

/** Owns the Portico server directory list, connection attempts, automatic offline retries, and session-ended watching. */
export function useDirectoryFlow(params: DirectoryFlowParams) {
  const {
    stage, busy, error, appActive, activeRef, lifetime, busyRef, acting, adoptingTV, stageRef,
    operation, account, accountScope, invitationSwitch, invitationIntent, memberMoveRef, memberOperation,
    resetRef, inspectRef, retireInvitationSwitch, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery,
    consume, setStage, setBusy, setError, setPassword, setServer, setAccountEnded,
  } = params;
  const paramsRef = useRef(params);
  paramsRef.current = params;
  const [servers, setServers] = useState<HostedServer[]>([]);
  const [target, setTarget] = useState<HostedServer>();
  const [retryIn, setRetryIn] = useState(20);
  const [directoryList, setDirectoryList] = useState<'idle' | 'loading' | 'ready' | 'failed'>('idle');
  const directoryAttempt = useRef(0);
  const retryDelay = useRef(20);
  const targetRef = useRef(target);
  const retryEpoch = useRef(0);
  const remainingRetry = useRef(20);
  const retryTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const directoryConnectRef = useRef<(value: HostedServer, automatic?: boolean, expectedTarget?: HostedServer) => void>(() => {});

  const stopRetry = useCallback(() => {
    ++retryEpoch.current;
    if (retryTimer.current !== undefined) clearTimeout(retryTimer.current);
    retryTimer.current = undefined;
  }, []);

  const cancelDirectory = useCallback(() => {
    const p = paramsRef.current;
    p.retireOwnerRecoveryContact(); p.retireOwnerReset(); setDirectoryList('idle'); p.retireRecovery(); stopRetry();
    targetRef.current = undefined; ++directoryAttempt.current; p.operation.current?.dispose(); p.operation.current = undefined;
    p.acting.current = false; p.setBusy(false); setTarget(undefined); p.setError(undefined);
  }, [stopRetry]);

  const directory = async () => {
    const handoff = invitationSwitch.current;
    if (handoff?.active && invitationIntent.current === handoff.entry) {
      const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
      if (!lifetime.current || invitationSwitch.current !== handoff || !handoff.active || invitationIntent.current !== handoff.entry) return;
      const session = credentials.getSnapshot().session;
      if (!session) return;
      accountScope.current = {accountId: session.account.id, familyId: session.familyId};
      retireInvitationSwitch(); account.current?.dispose(); account.current = undefined;
      await inspectRef.current(handoff.entry.server); return;
    }
    const move = memberMoveRef.current;
    if (move && memberOperation.current === operation.current && move.getSnapshot().phase === 'portico') {
      // Signed in: name both accounts and stop on the confirm. Nothing moves until Move is pressed.
      await move.preparePortico();
      if (lifetime.current && memberMoveRef.current === move && memberOperation.current === operation.current) setStage('member-move');
      return;
    }
    const attempt = ++directoryAttempt.current;
    const credentials = nativeAccounts.accountCredentials(), session = credentials.getSnapshot().session;
    if (!session) {resetRef.current(); return;}
    const expected = accountExpectation(session);
    const current = () => {
      const live = credentials.getSnapshot().session;
      return lifetime.current && attempt === directoryAttempt.current && live?.account.id === expected.accountId && live.familyId === expected.familyId;
    };
    if (!current()) return;
    accountScope.current = {accountId: expected.accountId, familyId: expected.familyId};
    stopRetry(); targetRef.current = undefined; setTarget(undefined); setError(undefined); setPassword('');
    setServers([]); setDirectoryList('loading'); setStage('servers');
    try {
      const out = await nativeAccountRead(credentials, issued => nativeAccounts.porticoServers(issued),
        {expected, isCurrent: current});
      if (!current()) return;
      if (!isValidDirectoryResponse(out))
        throw Object.assign(new Error('native_directory_invalid'), {code: 'invalid_response'});
      setServers(out.items); setDirectoryList('ready');
    } catch (failure) {
      if (!current()) {
        if (lifetime.current && attempt === directoryAttempt.current) {resetRef.current(); if (credentials.getSnapshot().ended) setAccountEnded(true);}
        return;
      }
      const presentation = presentError(failure, 'portico-account');
      if (presentation.silent) {setDirectoryList('idle'); return;}
      if (isTransientDirectoryFailure(presentation.category)) {
        setDirectoryList('failed'); return;
      }
      setDirectoryList('idle'); throw failure;
    }
  };

  const connectDirectory = (value: HostedServer, automatic = false, expectedTarget = value) => {
    if (busyRef.current || acting.current || adoptingTV.current || !lifetime.current) return;
    if (automatic && (stageRef.current !== 'servers' || targetRef.current !== expectedTarget || !activeRef.current || !servers.includes(value))) return;
    stopRetry(); targetRef.current = value;
    if (!automatic) {retryDelay.current = 20; setRetryIn(20);}
    remainingRetry.current = retryDelay.current;
    const attempt = ++directoryAttempt.current;
    setTarget(value); setError(undefined); acting.current = true; setBusy(true);
    void (async () => {
      let next: NativeServerSignIn | undefined;
      try {
        retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); operation.current?.dispose(); next = operation.current = new NativeServerSignIn();
        const result = await next.connectPortico(value);
        if (!lifetime.current || attempt !== directoryAttempt.current) return;
        if (result.kind !== 'complete') setServer(next.serverDescriptor()); consume(result);
      } catch (failure) {
        if (lifetime.current && attempt === directoryAttempt.current) {
          if (failure instanceof HostedSignInUnavailable) {retryDelay.current = Math.max(retryDelay.current, failure.retryAfterSeconds); remainingRetry.current = retryDelay.current; setRetryIn(remainingRetry.current);}
          setError(failure);
        }
      } finally {
        if (!lifetime.current) acting.current = false;
        else if (attempt === directoryAttempt.current) {acting.current = false; setBusy(false);}
      }
    })();
  };
  directoryConnectRef.current = connectDirectory;
  const chooseAnother = () => cancelDirectory();

  const directoryView = stage === 'servers' ? directoryFailure(target, error) : 'other';
  const hostedWaiting = stage === 'servers' && !!target && error instanceof HostedSignInUnavailable;
  const directoryScope = accountScope.current;

  useLayoutEffect(() => {
    if (stage !== 'servers' || !directoryScope) return;
    const credentials = nativeAccounts.accountCredentials(); let retired = false;
    const changed = () => {
      if (retired || !lifetime.current || stageRef.current !== 'servers' || accountScope.current !== directoryScope) return;
      const live = credentials.getSnapshot().session;
      if (live?.account.id === directoryScope.accountId && live.familyId === directoryScope.familyId) return;
      retired = true; resetRef.current(); // retire this view; canonical owner alone ends credentials
      if (credentials.getSnapshot().ended) setAccountEnded(true);
    };
    const off = credentials.subscribe(changed); changed(); return off;
    // resetRef keeps one identity for this entry, so the stage and scope alone reopen this view.
  }, [accountScope, directoryScope, lifetime, resetRef, setAccountEnded, stage, stageRef]);

  const offlineRows = stage === 'servers' && directoryList === 'ready' ? directoryRows(servers) : [];
  const allDirectoryOffline = offlineRows.length > 0 && offlineRows.every(row => row.offline);

  useEffect(() => {
    if (!allDirectoryOffline || !lifetime.current || stageRef.current !== 'servers' || !directoryScope) return;
    const live = nativeAccounts.accountCredentials().getSnapshot().session;
    if (allDirectoryOffline && lifetime.current && stageRef.current === 'servers' && !target && !targetRef.current && !busy && !busyRef.current
      && !acting.current && !adoptingTV.current && appActive && activeRef.current && servers[0] && directoryScope
      && accountScope.current === directoryScope && live?.account.id === directoryScope.accountId && live.familyId === directoryScope.familyId)
      directoryConnectRef.current(servers[0]);
  }, [accountScope, acting, activeRef, adoptingTV, allDirectoryOffline, appActive, busy, busyRef, directoryScope, lifetime, servers, stageRef, target]);

  useEffect(() => {
    if (stage !== 'servers' || !target || (directoryView !== 'waiting' && !(allDirectoryOffline && directoryView === 'unreachable') && !hostedWaiting) || busy || !appActive) return;
    const attempt = directoryAttempt.current, epoch = ++retryEpoch.current, capturedTarget = target, capturedScope = directoryScope;
    const current = () => lifetime.current && retryEpoch.current === epoch && directoryAttempt.current === attempt
      && stageRef.current === 'servers' && targetRef.current === capturedTarget && activeRef.current
      && !busyRef.current && !acting.current && !adoptingTV.current && capturedScope !== undefined && accountScope.current === capturedScope
      && nativeAccounts.accountCredentials().getSnapshot().session?.account.id === capturedScope.accountId
      && nativeAccounts.accountCredentials().getSnapshot().session?.familyId === capturedScope.familyId;
    const tick = () => {
      if (!current()) return;
      const seconds = remainingRetry.current;
      setRetryIn(seconds);
      if (seconds <= 0) {
        retryDelay.current = nextRetryDelay(retryDelay.current);
        const index = servers.indexOf(capturedTarget);
        const next = allDirectoryOffline && !hostedWaiting && index >= 0 ? servers[(index + 1) % servers.length] : capturedTarget;
        directoryConnectRef.current(next ?? capturedTarget, true, capturedTarget); return;
      }
      retryTimer.current = setTimeout(() => {if (!current()) return; remainingRetry.current -= 1; tick();}, 1000);
    };
    tick();
    const stopOwnedRetry = () => {if (retryEpoch.current === epoch) stopRetry();};
    return stopOwnedRetry;
  }, [accountScope, acting, activeRef, adoptingTV, allDirectoryOffline, appActive, busy, busyRef, directoryScope, directoryView, hostedWaiting, lifetime, servers, stage, stageRef, stopRetry, target]);

  const resetDirectory = useCallback(() => {
    setDirectoryList('idle'); stopRetry(); targetRef.current = undefined;
    ++directoryAttempt.current; setTarget(undefined); retryDelay.current = 20; setRetryIn(20);
  }, [stopRetry]);

  return {
    servers, target, setTarget, targetRef,
    retryIn, setRetryIn, retryDelay,
    directoryList, setDirectoryList, directoryAttempt,
    stopRetry, resetDirectory, cancelDirectory, directory, connectDirectory, chooseAnother,
    directoryView, hostedWaiting, offlineRows, allDirectoryOffline,
  };
}
