import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {passwordStrength} from '../../../../../../packages/client-core/src/password-strength';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {qrScannerAvailable, scanQRCode} from '../../../platform/qr-scanner';
import {subscribeNativeAppSessions} from '../../../platform/app-session-storage';
import type {NativeMemberAccountMove} from '../../../platform/member-account-move';

type FormTicket = {live: boolean; scope: object; scan?: AbortController};

/** Connect44b/c: the owner's move code (typed or scanned), then the new local sign-in. The verified operation checks
 * the code and owns redemption. A draft belongs to one opening of one step: an edit, scan or submit from a render the
 * step has moved past, or once the captured controller is no longer current, does nothing. Passwords are cleared when
 * handed over, on Back and whenever the step changes. Moved from the old screen as it was. */
export function useMoveCode<Result>(controller: NativeMemberAccountMove<Result>, onBack: () => void, isCurrent: () => boolean) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), t = defaultI18n.t;
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
  const back = () => {if (current() && !state.busy && !submitted.current) {committed?.scan?.abort(); revision.current++; setCode(''); setPassword(''); setConfirmation(''); setShown(false); controller.cancel(); onBack();}};
  return {
    visible: state.phase === 'code' || state.phase === 'local', local, busy: state.busy,
    error: state.error != null ? presentError(state.error, 'sign-in').body : undefined,
    scanError: scanError != null ? presentError(scanError, 'sign-in').body : undefined,
    code, setCode: (value: string) => edit(() => {setCode(value); setInvalidCode(false);}), invalidCode,
    username, setUsername: (value: string) => edit(() => setUsername(value)),
    password, setPassword: (value: string) => edit(() => setPassword(value)),
    confirmation, setConfirmation: (value: string) => edit(() => setConfirmation(value)),
    shown, toggleShown: () => edit(() => setShown(value => !value)),
    strength: password.length > 0 ? {text: t(strengthKey), acceptable: strength.acceptable} : undefined,
    differ: confirmation.length > 0 && confirmation !== password,
    ready: !state.busy && (local ? !!username && strength.acceptable && password === confirmation : !!code),
    canScan: !local && qrScannerAvailable(), scanning, scan: () => {void scan();},
    submit, back,
  };
}
