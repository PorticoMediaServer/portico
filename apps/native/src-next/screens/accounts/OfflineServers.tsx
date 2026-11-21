import React, {useLayoutEffect, useMemo, useRef, useState} from 'react';
import type {DirectoryRow} from './directory-chooser';
import {PhoneOfflineServers} from './PhoneOfflineServers';

type Props = {rows: readonly DirectoryRow[]; scope: object | undefined; revision: number; isCurrent: () => boolean;
  onDownloads?: () => void; username?: string; onSettings?: () => void; onJoin?: () => void; onFind?: () => void;
  busy: boolean; retryIn?: number; retryProgress?: number; activeId?: string; onRetry: (id: string) => void; onBack: () => void; onRefresh: () => void};
type Ticket = {live: boolean; scope: object};
/** Actual directory observations only. The parent owns one serialized connection and its existing captured-issuer retry timer;
 * this view never starts a session. */
/** Phones and tablets: every server not answering, through `PhoneOfflineServers`. A TV draws `pages/tv/accounts/OfflineServersPage`. */
export function OfflineServers(props: Props) {
  const scope = useMemo(() => ({owner: props.scope, revision: props.revision}), [props.scope, props.revision]);
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {active.current.live = false; active.current = undefined;}
  useLayoutEffect(() => {const ticket = {scope, live: true}; active.current = ticket; setCommitted(ticket);
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined;};}, [scope]);
  const current = () => !!committed?.live && committed.scope === scope && active.current === committed && props.isCurrent();
  const action = (command?: () => void) => () => {if (current() && !props.busy) command?.();};
  return <PhoneOfflineServers rows={props.rows} busy={props.busy} activeId={props.activeId} retryIn={props.retryIn} username={props.username}
    onRetry={id => {if (current() && !props.busy && props.rows.some(row => row.id === id)) props.onRetry(id);}}
    onDownloads={props.onDownloads ? action(props.onDownloads) : undefined}
    onSettings={props.onSettings ? action(props.onSettings) : undefined} onJoin={props.onJoin ? action(props.onJoin) : undefined} onFind={props.onFind ? action(props.onFind) : undefined} onRefresh={action(props.onRefresh)} onBack={action(props.onBack)}/>;
}
