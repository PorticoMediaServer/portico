import {useCallback, useEffect, useLayoutEffect, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction} from 'react';
import {Platform} from 'react-native';
import type {IdentityOptions} from '../../../../../../../packages/client-core/src/hosted-security';
import type {SetupCodeSnapshot} from '../../../../../../../packages/client-core/src/setup-code';
import {nativeAccounts} from '../../../../platform/accounts';
import {NativePorticoSignIn} from '../../../../platform/portico-sign-in';
import type {NativeServerSignIn} from '../../../../platform/server-sign-in';
import {createNativePorticoTVSignIn} from '../../../../platform/tv-sign-in';
import {createTVApprovalCredentialOpening, type TVApprovalCredentialOpening} from '../../../../screens/accounts/tv-approval-credentials';
import {createTVApprovalFinalization, type TVApprovalFinalization} from '../../../../screens/accounts/tv-approval-finalization';
import {createTVCodeOpening, type TVCodeCapture, type TVCodeKind, type TVCodeOpening} from '../../../../screens/accounts/tv-code-opening';
import {useCodeRetry} from '../../../../tv/auth/code-retry';
import {computeTVNoCodeState} from '../../../../pages/shared/accounts/entry/entry-helpers';
import type {EntryResult, NativeAccountEntryStage, RequiredSetup} from '../../../../pages/shared/accounts/entry/entry-types';

type TVCodeFlowParams = {
  stage: NativeAccountEntryStage;
  busy: boolean;
  stageError: unknown;
  address: string;
  lifetime: MutableRefObject<boolean>;
  activeRef: MutableRefObject<boolean>;
  busyRef: MutableRefObject<boolean>;
  acting: MutableRefObject<boolean>;
  stageRef: MutableRefObject<NativeAccountEntryStage>;
  operation: MutableRefObject<NativeServerSignIn | undefined>;
  account: MutableRefObject<NativePorticoSignIn | undefined>;
  preparedAuthSource: MutableRefObject<NativePorticoSignIn | undefined>;
  directoryRef: MutableRefObject<() => Promise<void>>;
  inspectRef: MutableRefObject<(origin?: string) => Promise<NativeServerSignIn | undefined>>;
  consume: (result: EntryResult) => void;
  setStage: Dispatch<SetStateAction<NativeAccountEntryStage>>;
  setBusy: Dispatch<SetStateAction<boolean>>;
  setError: Dispatch<SetStateAction<unknown>>;
  setOptions: Dispatch<SetStateAction<IdentityOptions | undefined>>;
  setPassword: Dispatch<SetStateAction<string>>;
  setCode: Dispatch<SetStateAction<string>>;
  setRecovery: Dispatch<SetStateAction<boolean>>;
  setRecoveryCode: Dispatch<SetStateAction<string>>;
  setRequiredSetup: Dispatch<SetStateAction<RequiredSetup | undefined>>;
};

/** Owns the TV activation code session, approval finalization, retry cadence, and credentials/code transitions. */
export function useTVCodeFlow(params: TVCodeFlowParams) {
  const {
    stage, busy, stageError, address, lifetime, activeRef, busyRef, acting, stageRef,
    operation, account, preparedAuthSource, directoryRef, inspectRef, consume,
    setStage, setBusy, setError, setOptions, setPassword, setCode, setRecovery, setRecoveryCode, setRequiredSetup,
  } = params;
  const paramsRef = useRef(params);
  paramsRef.current = params;

  const [tvView, setTVView] = useState<{opening: TVCodeOpening; state: SetupCodeSnapshot<unknown>}>();
  const tvState: SetupCodeSnapshot<unknown> = tvView?.state ?? {phase: 'idle'};
  const [tvKind, setTVKind] = useState<'local' | 'portico'>('portico');
  const tvOpening = useRef<TVCodeOpening | undefined>(undefined);
  const adoptingTV = useRef(false);
  const tvDoorTried = useRef(false);

  const retireTVOpening = useCallback((cancel = false, expected = tvOpening.current, publish = true) => {
    if (!expected) return;
    expected.retire(cancel);
    if (tvOpening.current !== expected) return;
    tvOpening.current = undefined; adoptingTV.current = false;
    if (publish && paramsRef.current.lifetime.current) paramsRef.current.setBusy(paramsRef.current.acting.current);
  }, []);

  useLayoutEffect(() => {if (stage !== 'tv') retireTVOpening();}, [stage, retireTVOpening]);

  const typeTVCredentials = async (kind: TVCodeKind, current: () => boolean, opening: TVCodeOpening) => {
    if (!current()) return;
    if (kind === 'portico') {
      preparedAuthSource.current = undefined;
      account.current?.dispose(); const next = account.current = new NativePorticoSignIn();
      const available = await next.prepare();
      if (!current() || account.current !== next) {
        if (account.current === next) {next.dispose(); account.current = undefined; preparedAuthSource.current = undefined;}
        return;
      }
      preparedAuthSource.current = next; setOptions(available);
    }
    if (!current()) return;
    retireTVOpening(false, opening); stageRef.current = 'tv-credentials';
    setPassword(''); setCode(''); setRecoveryCode(''); setRecovery(false); setStage('tv-credentials');
  };

  async function openTV<T>(
    capture: TVCodeCapture<T, NativeServerSignIn | undefined>,
    accept: (grant: T, current: () => boolean) => Promise<(() => void | Promise<void>) | undefined>,
    approval?: Readonly<{
      watch: (current: () => boolean, retired: () => void) => TVApprovalCredentialOpening;
      create: (grant: T, current: () => boolean) => TVApprovalFinalization;
      finish: () => Promise<void>;
    }>,
  ) {
    const opening: TVCodeOpening = createTVCodeOpening(capture, {
      owns: value => tvOpening.current === value,
      current: original => lifetime.current && activeRef.current && stageRef.current === 'tv'
        && (original.kind !== 'local' || operation.current === original.source),
      publish: (owner, state) => setTVView({opening: owner, state}),
      busy: (pending, adopting) => {adoptingTV.current = adopting; setBusy(pending || acting.current);},
      error: setError,
      accept: async (original, grant, current) => {
        if (!current()) return;
        const finish = await accept(grant, current);
        if (!current() || !finish) return;
        const completed = await original.service.complete(grant);
        if (!current() || !completed) return;
        retireTVOpening(false, opening);
        await finish();
      },
      watchApproval: approval?.watch,
      approval: approval ? (_original, grant, current) => approval.create(grant, current) : undefined,
      approved: approval ? async () => {retireTVOpening(false, opening); setError(undefined); await approval.finish();} : undefined,
      approvalRetired: () => {
        if (tvOpening.current !== opening || !lifetime.current || stageRef.current !== 'tv') return;
        retireTVOpening(false, opening); stageRef.current = 'welcome'; setStage('welcome');
      },
      replace: async (original, current) => {if (current()) await startTV(original.kind, original.source);},
      credentials: async (original, current) => {await typeTVCredentials(original.kind, current, opening);},
    });
    retireTVOpening();
    tvOpening.current = opening; stageRef.current = 'tv';
    setTVKind(capture.kind); setTVView({opening, state: {phase: 'idle'}}); setError(undefined);
    setStage('tv'); void opening.start();
  }

  const startTV = async (kind: TVCodeKind, originalSource = operation.current) => {
    if (!lifetime.current) return;
    if (kind === 'local') {
      const source = originalSource;
      if (!source || operation.current !== source || !lifetime.current) return;
      return openTV({kind, source, service: source.localTVSignIn()}, async (grant, current) => {
        if (!current()) return;
        const result = await source.acceptLocalTVSession(grant);
        if (current()) return () => consume(result);
      });
    }
    const service = createNativePorticoTVSignIn(), credentials = nativeAccounts.accountCredentials();
    let credentialOpening: TVApprovalCredentialOpening | undefined;
    return openTV({kind, source: undefined, service}, async () => undefined, {
      watch: (current, retired) => credentialOpening = createTVApprovalCredentialOpening(credentials, {current, retired}),
      create: (grant, current) => createTVApprovalFinalization({
        service, grant, credentials, credentialOpening, current,
        adopt: (original, callerCurrent) => nativeAccounts.adoptPorticoTVSession(original, callerCurrent),
      }),
      finish: () => directoryRef.current(),
    });
  };

  const returnToTVCode = async () => {
    account.current?.dispose(); account.current = undefined; preparedAuthSource.current = undefined;
    setPassword(''); setCode(''); setRecoveryCode(''); setRecovery(false); setRequiredSetup(undefined);
    if (tvKind === 'local') await inspectRef.current(address); else await startTV('portico');
  };

  const {selfRetrying, noCodeNow} = computeTVNoCodeState(Platform.isTV, stage, tvKind, tvState, tvView?.opening.approval()?.phase, stageError);
  useCodeRetry(noCodeNow && !selfRetrying && !busy, () => {
    const opening = tvView?.opening;
    if (opening?.current() && !busyRef.current && !opening.pending()) void opening.newCode();
  });
  useEffect(() => {if (stage === 'address') tvDoorTried.current = false;}, [stage]);

  const renderedTV = stage === 'tv' ? tvView?.opening : undefined;
  return {tvState, tvKind, tvOpening, adoptingTV, tvDoorTried, noCodeNow, renderedTV, retireTVOpening, startTV, returnToTVCode};
}
