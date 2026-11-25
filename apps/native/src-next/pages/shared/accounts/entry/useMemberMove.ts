import {useCallback, useEffect, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction} from 'react';
import {Platform} from 'react-native';
import type {IdentityOptions} from '../../../../../../../packages/client-core/src/hosted-security';
import type {ServerSignIn} from '../../../../../../../packages/client-core/src/sign-in';
import {nativeAccounts} from '../../../../platform/accounts';
import {NativePorticoSignIn} from '../../../../platform/portico-sign-in';
import {NativeServerSignIn} from '../../../../platform/server-sign-in';
import type {NativeSignInHint} from '../../../../platform/sign-in-ended';
import type {EntryResult, MemberMove, MemberMoveSnapshot, NativeAccountEntryStage} from '../../../../pages/shared/accounts/entry/entry-types';

type MemberMoveParams = {
  stage: NativeAccountEntryStage;
  server: ServerSignIn | undefined;
  endedSignIn: NativeSignInHint | undefined;
  latestEndedHint: MutableRefObject<NativeSignInHint | undefined>;
  lifetime: MutableRefObject<boolean>;
  stageRef: MutableRefObject<NativeAccountEntryStage>;
  operation: MutableRefObject<NativeServerSignIn | undefined>;
  account: MutableRefObject<NativePorticoSignIn | undefined>;
  preparedAuthSource: MutableRefObject<NativePorticoSignIn | undefined>;
  accountScope: MutableRefObject<{accountId: string; familyId: string} | undefined>;
  retireOwnerRecoveryContact: () => void;
  retireOwnerReset: () => void;
  retireRecovery: () => boolean;
  publishPreparedAccount: (source: NativePorticoSignIn, available: IdentityOptions, current: () => boolean) => boolean;
  consume: (result: EntryResult) => void;
  setServer: Dispatch<SetStateAction<ServerSignIn | undefined>>;
  setPassword: Dispatch<SetStateAction<string>>;
  setCode: Dispatch<SetStateAction<string>>;
  setStage: Dispatch<SetStateAction<NativeAccountEntryStage>>;
};

/** Owns member account-move state, subscription, and move-to-Portico/move-to-local entry flows. */
export function useMemberMove({
  stage, server, endedSignIn, latestEndedHint, lifetime, stageRef, operation, account, preparedAuthSource,
  accountScope, retireOwnerRecoveryContact, retireOwnerReset, retireRecovery, publishPreparedAccount, consume,
  setServer, setPassword, setCode, setStage,
}: MemberMoveParams) {
  const [memberMove, setMemberMove] = useState<MemberMove>();
  const [moveSnapshot, setMoveSnapshot] = useState<MemberMoveSnapshot>();
  const memberMoveRef = useRef<MemberMove | undefined>(undefined);
  const memberOperation = useRef<NativeServerSignIn | undefined>(undefined);
  const memberFactorOperation = useRef<NativeServerSignIn | undefined>(undefined);

  const disposeMemberMove = useCallback(() => {
    memberMoveRef.current?.dispose();
  }, []);

  const stopMemberMove = useCallback(() => {
    memberMoveRef.current?.dispose(); memberMoveRef.current = undefined;
    memberOperation.current = undefined; setMemberMove(undefined); setMoveSnapshot(undefined);
  }, []);

  useEffect(() => {
    if (!memberMove) return;
    const publish = () => setMoveSnapshot(memberMove.getSnapshot());
    publish();
    return memberMove.subscribe(publish);
  }, [memberMove]);

  useEffect(() => {
    if (stage !== 'member-move' || !memberMove || moveSnapshot?.phase !== 'adopted'
      || moveSnapshot.result?.kind !== 'challenge' || memberOperation.current !== operation.current) return;
    const result = memberMove.takeAcceptedResult();
    if (result?.kind === 'challenge') {memberFactorOperation.current = operation.current; consume(result);}
  }, [stage, memberMove, moveSnapshot, consume, operation]);

  const beginMemberMove = async () => {
    const source = operation.current;
    if (!source || !server || server.move !== 'to_portico' || Platform.isTV) return;
    stopMemberMove();
    const move = await source.createMemberMove('password');
    if (!lifetime.current || operation.current !== source) {move.dispose(); return;}
    memberMoveRef.current = move; memberOperation.current = source;
    setMemberMove(move); setMoveSnapshot(move.getSnapshot()); setStage('member-move');
  };

  const beginMemberCode = async () => {
    const source = operation.current;
    // The owner's move code: for a move to local sign-in, and (P2-66) a move to Portico Accounts.
    if (!source || !server || !(server.signIn === 'local' && server.move === 'to_local' || server.signIn === 'portico' && server.move === 'to_portico') || Platform.isTV) return;
    stopMemberMove();
    const move = await source.createMemberMove('code');
    if (!lifetime.current || operation.current !== source) {move.dispose(); return;}
    memberMoveRef.current = move; memberOperation.current = source;
    setMemberMove(move); setMoveSnapshot(move.getSnapshot()); setStage('member-move');
  };

  const beginEndedMemberCode = async (openingCurrent: () => boolean, signal: AbortSignal) => {
    const hint = endedSignIn;
    if (Platform.isTV || !hint || hint.signIn !== 'portico' || signal.aborted || !openingCurrent() || !lifetime.current || stageRef.current !== 'ended-sign-in') return;
    retireOwnerRecoveryContact(); retireOwnerReset(); retireRecovery(); operation.current?.dispose();
    const source = new NativeServerSignIn(nativeAccounts, {expectedTarget: {serverId: hint.serverId, fingerprint: hint.fingerprint}});
    operation.current = source;
    const retire = () => source.dispose(); signal.addEventListener('abort', retire, {once: true});
    const current = () => !signal.aborted && openingCurrent() && lifetime.current && stageRef.current === 'ended-sign-in'
      && latestEndedHint.current === hint && operation.current === source;
    let handed = false;
    try {
      if (!current()) return;
      const descriptor = await source.inspect(hint.origin);
      if (!current()) return;
      if (!descriptor || descriptor.id !== hint.serverId || descriptor.signIn !== 'local' || descriptor.move !== 'to_local' || descriptor.setupRequired)
        throw Object.assign(new Error('native_move_identity_changed'), {code: 'identity_mismatch'});
      const pin = await source.inspectLocalIdentity(); if (!current()) return;
      await source.confirmLocal(pin); if (!current()) return;
      source.assertSignInTarget(hint.serverId, hint.fingerprint);
      const move = await source.createMemberMove('code');
      if (!current()) {move.dispose(); return;}
      stopMemberMove(); memberMoveRef.current = move; memberOperation.current = source;
      signal.removeEventListener('abort', retire); handed = true;
      setServer(source.serverDescriptor()); setMemberMove(move); setMoveSnapshot(move.getSnapshot());
      setPassword(''); setCode(''); stageRef.current = 'member-move'; setStage('member-move');
    } finally {
      signal.removeEventListener('abort', retire);
      if (!handed) {source.dispose(); if (operation.current === source) operation.current = undefined;}
    }
  };

  const createMoveAccount = async () => {
    const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
    if (!lifetime.current || !memberMoveRef.current) return;
    const existing = credentials.getSnapshot().session;
    if (existing) {accountScope.current = {accountId: existing.account.id, familyId: existing.familyId}; setStage('account-sign-out'); return;}
    const move = memberMoveRef.current;
    preparedAuthSource.current = undefined; account.current?.dispose(); const next = account.current = new NativePorticoSignIn();
    const available = await next.prepare();
    if (publishPreparedAccount(next, available, () => !!move && memberMoveRef.current === move)) setStage('register');
  };

  const differentMoveAccount = async () => {
    const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
    if (!lifetime.current || !memberMoveRef.current) return;
    const signedIn = credentials.getSnapshot().session;
    if (signedIn) {accountScope.current = {accountId: signedIn.account.id, familyId: signedIn.familyId}; setStage('account-sign-out');}
    else {memberMoveRef.current.cancel(); setStage('member-move');}
  };

  return {
    memberMove, moveSnapshot, memberMoveRef, memberOperation, memberFactorOperation,
    disposeMemberMove, stopMemberMove, beginMemberMove, beginMemberCode, beginEndedMemberCode,
    createMoveAccount, differentMoveAccount,
  };
}
