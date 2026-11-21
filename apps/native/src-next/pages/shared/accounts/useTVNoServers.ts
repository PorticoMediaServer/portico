import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from 'react';
import {registerBack} from '../../../kit/navigation/back';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';

export type TVNoServersProps = {busy: boolean; error?: ReactNode; isCurrent: () => boolean; onBack: () => void;
  onJoin: (invitation: string, address: string) => void; onFind: () => void; onRefresh: () => void; onSignOut: () => void; notice?: {title: string; body: string; onRetry?: () => void}};

type Ticket = {live: boolean; scope: object};

export function useTVNoServers(props: TVNoServersProps) {
  const {isCurrent, onBack} = props;
  const [joining, setJoining] = useState(false), [address, setAddress] = useState(''), [invitation, setInvitation] = useState('');
  const [field, setField] = useState<'address' | 'invitation'>('address');
  const entryRevision = useTVBusyEntryRevision(props.busy);
  const draft = useMemo(() => Object.freeze({joining, address, invitation, field}), [joining, address, invitation, field]);
  const latest = useRef(draft); latest.current = draft;
  const scope = useMemo(() => ({isCurrent}), [isCurrent]);
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {active.current.live = false; active.current = undefined;}
  useLayoutEffect(() => {const ticket = {live: true, scope}; active.current = ticket; setCommitted(ticket);
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined;};}, [scope]);
  const current = useCallback(() => !!committed?.live && committed.scope === scope && active.current === committed && isCurrent(), [committed, scope, isCurrent]);
  const owns = () => current() && latest.current === draft && !props.busy;
  const back = useCallback(() => {if (!current()) return; if (joining) {setInvitation(''); setJoining(false);} else onBack();}, [current, joining, onBack]);
  useEffect(() => registerBack(() => {if (!joining || !current()) return false; back(); return true;}, {scope: 'surface'}), [joining, current, back]);
  return {
    joining, address, invitation, field, entryRevision, current, back,
    openJoin: () => {if (owns()) setJoining(true);},
    focusAddress: () => {if (current()) setField('address');},
    focusInvitation: () => {if (current()) setField('invitation');},
    changeAddress: (value: string) => {if (owns()) setAddress(value);},
    changeInvitation: (value: string) => {if (owns()) setInvitation(value);},
    join: () => {if (owns() && draft.invitation.trim()) {setInvitation(''); props.onJoin(draft.invitation, draft.address);}},
  };
}
