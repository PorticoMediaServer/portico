import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {ResetFileLocation} from '@core/account-reset.ts';
import type {BrowserAccountEntry} from '../../platform/account-entry';
import {browserAccountOrigin} from '../../platform/account-origin';
import {router} from '../../router';
import {AuthError} from '../../kit/accounts';
import {PorticoOwnerRecoveryDone, PorticoOwnerRecoveryRequest} from '../../kit/accounts/PorticoOwnerRecovery';
import {OwnerFile} from './LocalPasswordRecoveryFlow';

type Lease = {active: boolean; pending: boolean};
/** This view owns one original public server opening. A layout lease, never a
 * mutable successor, owns its pending file work and explicit done-page actions. */
export function PorticoOwnerRecoveryFlow({entry, onBack}: {entry: BrowserAccountEntry; onBack: () => void}) {
  const state = useSyncExternalStore(entry.subscribe, entry.getSnapshot);
  const original = useRef({entry, origin: state.origin, serverId: state.server?.id, pin: state.pin}).current;
  const active = useRef<Lease | undefined>(undefined);
  const [working, setWorking] = useState(false);
  const [lease, setLease] = useState<Lease>(), [file, setFile] = useState<ResetFileLocation>(), [error, setError] = useState<unknown>();
  const sameTarget = () => {
    const current = entry.getSnapshot();
    return original.entry === entry && !!original.pin && original.pin === current.pin && original.origin === current.origin && original.serverId === current.server?.id
      && current.server?.signIn === 'portico' && !current.invitation && (current.phase === 'portico' || current.phase === 'ready' && current.ownerResetSessionReady === true);
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({sameTarget});
  useLayoutEffect(() => {setupPorts.current = {sameTarget};});
  useLayoutEffect(() => {
    const {sameTarget} = setupPorts.current;
    const opening: Lease = {active: true, pending: false}; active.current = opening; setLease(opening);
    return () => {
      opening.active = false;
      if (active.current === opening) active.current = undefined;
      if (opening.pending && sameTarget() && entry.getSnapshot().phase === 'portico') entry.cancelPorticoOwnerResetFile();
    };
  }, [entry, original]);
  const current = () => !!lease && lease.active && active.current === lease && sameTarget();
  const available = () => current() && entry.getSnapshot() === state && !lease!.pending && !state.busy;
  const requestFile = async () => {
    if (!available() || state.phase !== 'portico') return;
    const opening = lease!; opening.pending = true; setWorking(true); setError(undefined);
    try {const location = await entry.requestPorticoOwnerResetFile(); if (current() && location) setFile(location);}
    catch (failure) {if (current()) setError(failure);}
    finally {opening.pending = false; if (current()) setWorking(false);}
  };
  const verifyFile = async (code: string) => {
    if (!available() || state.phase !== 'portico') return;
    const opening = lease!; opening.pending = true; setWorking(true); setError(undefined);
    try {await entry.verifyPorticoOwnerResetFile(code);}
    catch (failure) {if (current()) setError(failure);}
    finally {opening.pending = false; if (current()) setWorking(false);}
  };
  const back = () => {
    if (!current() || entry.getSnapshot() !== state || state.phase !== 'portico') return;
    entry.cancelPorticoOwnerResetFile(); onBack();
  };
  const finish = async (row?: 'signin.changeOwner' | 'signin.switchToLocal') => {
    if (!available() || !state.ownerResetSessionReady) return;
    const opening = lease!; opening.pending = true; setWorking(true); setError(undefined);
    const witness = () => current() && entry.getSnapshot() === state;
    try {
      if (await entry.completePorticoOwnerReset(witness) && witness()) {
        if (row) void router.navigate({to: '/settings', search: () => ({scope: 'server', section: 'signin', row})});
        else void router.navigate({to: '/', replace: true});
      }
    } catch (failure) {if (witness()) setError(failure);}
    finally {opening.pending = false; if (current()) setWorking(false);}
  };
  if (!sameTarget()) return null;
  const failure = <AuthError error={state.error ?? error}/>;
  if (state.phase === 'ready' && state.ownerResetSessionReady) return <PorticoOwnerRecoveryDone serverName={state.server!.name} error={failure}
    busy={!!state.busy || working} onContinue={() => {void finish();}} onChangeOwner={() => {void finish('signin.changeOwner');}} onSwitchToLocal={() => {void finish('signin.switchToLocal');}}/>;
  if (file) return <OwnerFile key={`${file.fileDirectory}:${file.retryAfterSeconds}`} serverName={state.server!.name} location={file}
    busy={!!state.busy || working} error={state.error ?? error} onVerify={verifyFile} onRewrite={() => {void requestFile();}} onBack={back}/>;
  return <PorticoOwnerRecoveryRequest serverName={state.server!.name} busy={!!state.busy || working} error={failure} onWriteFile={() => {void requestFile();}} onBack={back}
    onAccountRecovery={() => {
      if (!available()) return;
      const origin = browserAccountOrigin(import.meta.env, location);
      location.assign(origin + (origin.endsWith('/') ? '?mode=forgot' : '/?mode=forgot'));
    }}/>;
}
