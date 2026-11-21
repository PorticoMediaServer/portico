import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';
import {registerBack} from '../../../kit/navigation/back';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';

export type TVNoServersProps = {busy: boolean; error?: React.ReactNode; isCurrent: () => boolean; onBack: () => void;
  onJoin: (invitation: string, address: string) => void; onFind: () => void; onRefresh: () => void; onSignOut: () => void;
  notice?: {title: string; body: string; onRetry?: () => void}};

/** connect 36 draws the Portico keyboard nine across: the address's letters and its punctuation, or the code's capitals and
 * digits (the code's last digit sits on the action row ahead of Space). */
export const addressKeys = ['abcdefghi', 'jklmnopqr', 'stuvwxyz.', '012345678', '9:/@-_'].map(row => [...row]);
export const invitationKeys = ['ABCDEFGHI', 'JKLMNOPQR', 'STUVWXYZ-', '123456789'].map(row => [...row]);
type Ticket = {live: boolean; scope: object};

/** A Portico Account with no servers on a TV (connect 35, 36, 38): the two doors, then Join with an invitation's address and
 * code typed on the TV keyboard. Joining still goes through the parent's invitation parser and account continuation. Every
 * command belongs to the opening it was made in (a ticket per `isCurrent`); Back closes the code entry first. */
export function useTVNoServers(props: TVNoServersProps) {
  const {isCurrent, onBack, busy} = props;
  const [joining, setJoining] = useState(false), [address, setAddress] = useState(''), [invitation, setInvitation] = useState('');
  const [field, setField] = useState<'address' | 'invitation'>('address'), [doors, setDoors] = useState(0);
  const entryRevision = useTVBusyEntryRevision(busy);
  const draft = useMemo(() => Object.freeze({joining, address, invitation, field}), [joining, address, invitation, field]);
  const latest = useRef(draft); latest.current = draft;
  const scope = useMemo(() => ({isCurrent}), [isCurrent]);
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {active.current.live = false; active.current = undefined;}
  useLayoutEffect(() => {const ticket = {live: true, scope}; active.current = ticket; setCommitted(ticket);
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined;};}, [scope]);
  const current = useCallback(() => !!committed?.live && committed.scope === scope && active.current === committed && isCurrent(), [committed, scope, isCurrent]);
  const owns = () => current() && latest.current === draft && !busy;
  const back = useCallback(() => {if (!current()) return; if (joining) {setInvitation(''); setJoining(false); setDoors(value => value + 1);} else onBack();}, [current, joining, onBack]);
  useEffect(() => registerBack(() => {if (!joining || !current()) return false; back(); return true;}, {scope: 'surface'}), [joining, current, back]);
  // The doors' entry is once per drawing of them (each return from the code entry): a busy turn that has nothing to do with them
  // cannot put focus back on Join.
  useDestinationEntryFocus({visible: !joining, intent: 'enter', revision: doors}, {regionId: 'accounts.empty.door', childId: 'join'});
  return {joining, address, invitation, field, entryRevision, current, back,
    keys: field === 'address' ? addressKeys : invitationKeys, typed: field === 'address' ? address : invitation,
    open: () => {if (owns()) setJoining(true);},
    focusField: (value: 'address' | 'invitation') => {if (current()) setField(value);},
    setAddress: (value: string) => {if (owns()) setAddress(value);},
    setInvitation: (value: string) => {if (owns()) setInvitation(value);},
    type: (value: string) => {if (!owns()) return; if (field === 'address') setAddress(value); else setInvitation(value);},
    join: () => {if (owns()) {setInvitation(''); props.onJoin(draft.invitation, draft.address);}},
    find: () => {if (current()) props.onFind();}, signOut: () => {if (current()) props.onSignOut();},
    retry: props.notice?.onRetry ? () => {if (current()) props.notice?.onRetry?.();} : undefined};
}
