import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls/Button';
import {Dialog} from '../../kit/overlays/Dialog';
import {SecureRefusalDialog} from './SecureRefusalDialog';
import {ownerSwitchContextAllowed} from '../../platform/secure-pending';
import type {OwnerClaimAttempt} from '../../platform/owner-claim';
import type {SettingsOwnerSwitch} from './owner-switch';

const t = defaultI18n.t;
const LABEL = 'next.settingsSpec.server.signin.groups.0.rows.4.control.label';
const CONFIRM = 'next.settingsSpec.server.signin.groups.0.rows.4.control.confirm';

/** One drawn confirmation. Its phase is the claim: only the active open opening
 * can prepare, and a retained Confirm from an older opening can never act. */
type Opening = {
  readonly id: number;
  readonly abort: AbortController;
  phase: 'open' | 'preparing' | 'handed' | 'closed';
};

/** The portico-account Change owner row. The current owner confirms here, then
 * approves a new Portico Account on Hosted through the existing owner claim
 * handoff. This surface never prepares a move or a local owner transfer. */
export function ChangeOwner({source, isCurrent, onError}: {
  source: SettingsOwnerSwitch; isCurrent: () => boolean; onError: (error: unknown) => void;
}) {
  const [confirming, setConfirming] = useState(false), [secure, setSecure] = useState(false), [busy, setBusy] = useState(false);
  const [opening, setOpening] = useState<Opening | undefined>(undefined);
  const active = useRef<Opening | undefined>(undefined), sourceRef = useRef(source), ticket = useRef<{retired: boolean} | undefined>(undefined), sequence = useRef(0);
  const opener = useRef<HTMLButtonElement>(null), cancel = useRef<HTMLButtonElement>(null);
  // Layout retirement aborts this exact opening synchronously. A delivered
  // handoff is left to the Settings lifetime; an undelivered prepare is stopped
  // and its caller guard clears the protected journal attempt.
  useLayoutEffect(() => {
    const owned = {retired: false};
    ticket.current = owned;sourceRef.current = source;
    return () => {
      owned.retired = true;
      const current = active.current;
      if (!current) return;
      active.current = undefined;
      current.phase = 'closed';current.abort.abort();
    };
  }, [source]);
  const label = t(LABEL), verb = label.replace(/…$/, '');
  const open = () => {
    const owned = ticket.current;
    if (!owned || owned.retired || source !== sourceRef.current || active.current || busy || !isCurrent()) return;
    // The claim producer requires a secure context and encrypted pending storage.
    if (!ownerSwitchContextAllowed()) {setSecure(true);return;}
    const next: Opening = {id: ++sequence.current, abort: new AbortController(), phase: 'open'};
    active.current = next;setOpening(next);setConfirming(true);
  };
  const close = (target: Opening) => {
    if (active.current !== target || target.phase !== 'open') return;
    target.phase = 'closed';target.abort.abort();active.current = undefined;setOpening(undefined);setConfirming(false);
  };
  const run = (target: Opening) => {
    if (active.current !== target || target.phase !== 'open' || busy || !isCurrent()) return;
    const owned = ticket.current;
    if (!owned || owned.retired || ticket.current !== owned || source !== sourceRef.current) return;
    target.phase = 'preparing';setBusy(true);
    const attempt: OwnerClaimAttempt = {};
    const caller = {
      isCurrent: () => active.current === target && target.phase === 'preparing' && ticket.current === owned && !owned.retired,
      signal: target.abort.signal,
    };
    void (async () => {
      try {
        const url = await source.prepareChangeOwner(caller, attempt);
        if (active.current !== target || target.phase !== 'preparing' || ticket.current !== owned || owned.retired ||
          sourceRef.current !== source || target.abort.signal.aborted) {
          target.phase = 'closed';await attempt.lease?.cancel().catch(() => {});return;
        }
        target.phase = 'handed';window.location.assign(url);
      } catch (error) {
        target.phase = 'closed';target.abort.abort();
        if (active.current === target) active.current = undefined;
        await attempt.lease?.cancel().catch(() => {});
        if (ticket.current === owned && !owned.retired && isCurrent()) onError(error);
      } finally {
        if (ticket.current === owned && !owned.retired) setBusy(false);
      }
    })();
  };
  const current = opening && active.current === opening ? opening : undefined;
  return <>
    <Button ref={opener} variant="glass" size="small" loading={busy} disabled={!isCurrent()} onClick={open}>{label}</Button>
    <SecureRefusalDialog open={secure} onOpenChange={value => {if (!value) setSecure(false);}} />
    <Dialog open={confirming && !!current} onOpenChange={value => {if (!busy && !value && current) close(current);}} title={`${verb}?`} description={t(CONFIRM)}
      initialFocusRef={cancel} returnFocusRef={opener} dismissOnBack={!busy} dismissOnOutsidePress={!busy} showCloseButton={!busy}
      footer={<>
        <Button ref={cancel} size="small" disabled={busy} onClick={() => {if (current) close(current);}}>{t('action.cancel')}</Button>
        <Button variant="primary" size="small" loading={busy} onClick={() => {if (current) run(current);}}>{verb}</Button></>}>
      <span />
    </Dialog>
  </>;
}
