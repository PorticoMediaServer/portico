import {useLayoutEffect, useMemo, useRef, useState} from 'react';

type Ticket = {live: boolean; scope: object};

/** One committed opening of the offline servers list (phone, tablet and TV alike): a command runs only while the opening it
 * was drawn for is still the committed one, its directory still current, and nothing is busy. The parent owns the one
 * serialized connection and its retry timer; this never starts a session. */
export function useOfflineServersGuard({scope: owner, revision, isCurrent, busy, rowIds}: {scope: object | undefined; revision: number;
  isCurrent: () => boolean; busy: boolean; rowIds: readonly string[]}) {
  const scope = useMemo(() => ({owner, revision}), [owner, revision]);
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {active.current.live = false; active.current = undefined;}
  useLayoutEffect(() => {const ticket = {scope, live: true}; active.current = ticket; setCommitted(ticket);
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined;};}, [scope]);
  const current = () => !!committed?.live && committed.scope === scope && active.current === committed && isCurrent();
  // A callback drawn earlier reads the rows and busy state as they are now, not as they were when it was drawn.
  const latest = useRef({rowIds, busy}); latest.current = {rowIds, busy};
  return {current,
    /** A command bound to this opening: runs only while current and not busy. */
    action: (command?: () => void) => command ? () => {if (current() && !latest.current.busy) command();} : undefined,
    /** A row's own Try again: only a row this opening still lists. */
    retry: (id: string, onRetry: (id: string) => void) => {if (current() && !latest.current.busy && latest.current.rowIds.includes(id)) onRetry(id);}};
}
