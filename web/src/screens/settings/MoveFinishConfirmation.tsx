import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../../kit/overlays';
import {Button} from '../../kit/controls';
import type {AccountMove} from '@core/account-move.ts';
import type {OwnerMoveCaller} from './owner-switch';

type Lease = {controller: AbortController; pending: boolean};
const t = defaultI18n.t;

/** ACCOUNTS §14.1: one explicit confirmation for this original move. */
export function MoveFinishConfirmation(props: {
  move: AccountMove; isCurrent(): boolean; onClose(): void; onConfirm(caller: OwnerMoveCaller): Promise<void>;
}) {
  const [opening] = useState(() => props), [lease, setLease] = useState<Lease>();
  const [busy, setBusy] = useState<Lease>();
  const active = useRef<Lease | undefined>(undefined), rendered = useRef(props.move);
  rendered.current = props.move;
  useLayoutEffect(() => {
    const next: Lease = {controller: new AbortController(), pending: false};
    active.current = next; setLease(next);
    return () => {if (active.current === next) active.current = undefined; next.controller.abort();};
  }, []);
  const current = (ticket: Lease) => active.current === ticket && !ticket.controller.signal.aborted &&
    rendered.current === opening.move && opening.isCurrent();
  const close = () => {
    if (!lease || !current(lease) || lease.pending) return;
    active.current = undefined; lease.controller.abort(); opening.onClose();
  };
  const confirm = async () => {
    if (!lease || !current(lease) || lease.pending) return;
    const ticket = lease;
    ticket.pending = true; setBusy(ticket);
    try {
      await opening.onConfirm({signal: ticket.controller.signal, isCurrent: () => current(ticket)});
      if (current(ticket)) {active.current = undefined; ticket.controller.abort(); opening.onClose();}
    } catch { /* The captured owner reports the failure; consent stays here for an explicit retry. */ }
    finally {ticket.pending = false; setBusy(value => value === ticket ? undefined : value);}
  };
  const pending = lease !== undefined && busy === lease;
  return <Dialog open size="sm" title={t('accounts.settings.finishNow')} onOpenChange={open => {if (!open) close();}}
    dismissOnBack={!pending} dismissOnOutsidePress={!pending} showCloseButton={!pending}
    footer={<><Button size="small" disabled={pending} onClick={close}>{t('action.cancel')}</Button>
      <Button size="small" variant="primary" loading={pending} disabled={!lease || !current(lease)} onClick={() => void confirm()}>{t('accounts.settings.finishNow')}</Button></>}>
    <p>{t('accounts.settings.finishNowConfirm', {count: opening.move.waiting})}</p>
  </Dialog>;
}
