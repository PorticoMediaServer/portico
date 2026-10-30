import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {router} from '../../router';
import {Button} from '../../kit/controls';
import {LocalChangeOwnerDialog} from './LocalChangeOwnerDialog';
import type {LocalOwnerTransferPort, LocalOwnerTransferSource} from './local-owner-transfer';

const t = defaultI18n.t;

/** One rendered opening of the local Change owner row. The source, the exact
 * location object it was opened under and the route's liveness are captured before
 * the read; only this ticket's port may be closed by its retirement. */
type Ticket = {
  readonly admission: Admission;
  readonly location: typeof router.state.location;
  readonly abort: AbortController;
  live: boolean;
  port?: LocalOwnerTransferPort;
};

/** The row's own layout lifetime for one exact source/isCurrent/onError tuple.
 * Handlers from an earlier render can only act while this admission is the live
 * one and still carries the same ticket. */
type Admission = {
  readonly source: LocalOwnerTransferSource;
  readonly isCurrent: () => boolean;
  readonly onError: (error: unknown) => void;
  live: boolean;
  ticket?: Ticket;
};

/** auth 24a/24h: the Sign-in row and its dialog. The row owns the opening; the
 * guarded source owns the read and the transfer. */
export function LocalChangeOwnerRow({source, isCurrent, onError}: {
  source: LocalOwnerTransferSource;
  isCurrent: () => boolean;
  onError: (error: unknown) => void;
}) {
  const [admission, setAdmission] = useState<Admission | null>(null);
  const admissionRef = useRef<Admission | null>(null);
  const [ticket, setTicket] = useState<Ticket | null>(null), [port, setPort] = useState<LocalOwnerTransferPort | null>(null), [opening, setOpening] = useState(false);
  const retire = (held: Ticket) => {
    held.live = false;
    held.abort.abort();
    if (held.admission.ticket === held) held.admission.ticket = undefined;
    const owned = held.port;
    held.port = undefined;
    owned?.close();
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({onError});
  useLayoutEffect(() => {setupPorts.current = {onError};});
  useLayoutEffect(() => {
    const {onError} = setupPorts.current;
    const fresh: Admission = {source, isCurrent, onError, live: true};
    admissionRef.current = fresh;
    setAdmission(fresh);
    // A route change, even one that keeps this same Settings row mounted, ends the
    // opening synchronously so its dead ticket cannot block a reopen.
    const off = router.subscribe('onBeforeNavigate', () => {
      const held = fresh.ticket;
      if (!fresh.live || !held) return;
      fresh.ticket = undefined;
      const owned = held.port;
      retire(held);
      setTicket(current => current === held ? null : current);
      setPort(current => current === owned ? null : current);
      setOpening(false);
    });
    return () => {
      off();
      fresh.live = false;
      if (admissionRef.current === fresh) admissionRef.current = null;
      const held = fresh.ticket, ownedPort = held?.port;
      fresh.ticket = undefined;
      if (held) retire(held);
      setAdmission(current => current === fresh ? null : current);
      setTicket(current => current?.admission === fresh ? null : current);
      setPort(current => current === ownedPort ? null : current);
      setOpening(false);
    };
  }, [source, isCurrent]);
  // A new prop tuple must not act through the previous admission before its layout
  // replacement: handlers use this render's exact matching admission only.
  const shown = admission && admission.live && admissionRef.current === admission
    && admission.source === source && admission.isCurrent === isCurrent ? admission : null;
  const open = () => {
    const current = shown;
    if (!current || current.ticket || opening || !current.isCurrent()) return;
    const held: Ticket = {admission: current, location: router.state.location, abort: new AbortController(), live: true};
    current.ticket = held;
    setTicket(held);setPort(null);setOpening(true);
    const caller = {isCurrent: () => held.live && current.live && admissionRef.current === current && current.ticket === held &&
      router.state.location === held.location && held.admission.isCurrent() && !held.abort.signal.aborted, signal: held.abort.signal};
    void (async () => {
      try {
        const opened = await source.open(caller);
        if (!held.live || current.ticket !== held || !caller.isCurrent()) {opened.close();return;}
        held.port = opened;
        setPort(opened);setOpening(false);
      } catch (failure) {
        // A replaced location/admission or a retired ticket never reports its error
        // against the surface that replaced it.
        if (!held.live || current.ticket !== held || !caller.isCurrent()) return;
        retire(held);
        setTicket(current => current === held ? null : current);
        setPort(null);setOpening(false);
        if (!(failure instanceof DOMException && failure.name === 'AbortError')) current.onError(failure);
      }
    })();
  };
  const close = (held: Ticket) => {
    // Close only the exact rendered ticket; a retained handler from A can never
    // retire or clear B.
    if (!held || !held.live || !shown || held.admission !== shown || held !== ticket || held.admission.ticket !== held) return;
    const owned = held.port;
    retire(held);
    setTicket(current => current === held ? null : current);
    setPort(current => current === owned ? null : current);
    setOpening(false);
  };
  return <>
    <Button variant="glass" size="small" loading={opening} disabled={opening || !shown} onClick={open}>
      {t('next.settingsSpec.server.signin.groups.0.rows.5.control.label')}
    </Button>
    {shown && ticket && ticket.admission === shown && port
      ? <LocalChangeOwnerDialog open port={port} onClose={() => close(ticket)} onError={shown.onError} />
      : null}
  </>;
}
