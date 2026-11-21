import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import type {DirectoryRow} from '../../../screens/accounts/directory-chooser';

export type OfflineServerProps = {
  rows: readonly DirectoryRow[]; scope: object | undefined; revision: number; isCurrent: () => boolean;
  onDownloads?: () => void; username?: string; onSettings?: () => void; onJoin?: () => void; onFind?: () => void;
  busy: boolean; retryIn?: number; retryProgress?: number; activeId?: string;
  onRetry: (id: string) => void; onBack: () => void; onRefresh: () => void;
};

export function useOfflineServerOpening(props: OfflineServerProps) {
  const scope = useMemo(() => ({owner: props.scope, revision: props.revision}), [props.scope, props.revision]);
  type Ticket = {live: boolean; scope: typeof scope};
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {active.current.live = false; active.current = undefined;}
  useLayoutEffect(() => {
    const ticket = {scope, live: true};
    active.current = ticket;
    setCommitted(ticket);
    return () => {
      ticket.live = false;
      if (active.current === ticket) active.current = undefined;
    };
  }, [scope]);
  const current = () => !!committed?.live && committed.scope === scope && active.current === committed && props.isCurrent();
  const allowed = () => current() && !props.busy;
  const action = (command?: () => void) => command && (() => {if (allowed()) command();});
  const retry = (id: string) => {
    if (allowed() && props.rows.some(row => row.id === id)) props.onRetry(id);
  };
  return {retry, downloads: action(props.onDownloads), settings: action(props.onSettings), join: action(props.onJoin),
    find: action(props.onFind), refresh: () => {if (allowed()) props.onRefresh();}, back: () => {if (allowed()) props.onBack();}};
}
