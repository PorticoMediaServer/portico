import React, {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';

type Ticket = {live: boolean; scope: object};
/** An explicit code intent from an ended sign-in. Its exact committed opening
 * remains live while the parent proves the old issuer; no code or grant lives here. */
export function EndedMoveCodeAction({scope, busy, isCurrent, onStart}: {
  scope: object; busy: boolean; isCurrent: () => boolean; onStart: (guard: () => boolean, signal: AbortSignal) => Promise<void>;
}) {
  const active = useRef<Ticket | undefined>(undefined), flight = useRef<{ticket: Ticket; abort: AbortController} | undefined>(undefined);
  const [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {
    const retired = active.current;
    retired.live = false;
    if (flight.current?.ticket === retired) flight.current.abort.abort();
    if (active.current === retired) active.current = undefined;
  }
  useLayoutEffect(() => {const ticket = {scope, live: true}; active.current = ticket; flight.current = undefined; setCommitted(ticket);
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined; if (flight.current?.ticket === ticket) flight.current.abort.abort();};}, [scope]);
  const current = () => !!committed?.live && committed.scope === scope && active.current === committed && isCurrent();
  const start = () => {
    if (busy || flight.current || !current() || !committed) return;
    const submitted = {ticket: committed, abort: new AbortController()}; flight.current = submitted;
    const guard = () => current() && flight.current === submitted && !submitted.abort.signal.aborted;
    const release = () => {if (flight.current === submitted) flight.current = undefined;};
    // The account owner presents failures through its normal run() boundary.
    void onStart(guard, submitted.abort.signal).then(release, release);
  };
  return <Button variant="text" disabled={busy} onPress={start}>{defaultI18n.t('accounts.move.haveCode')}</Button>;
}
