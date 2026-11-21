import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {registerBack} from '../../../kit/navigation/back';
import type {createRememberedReadingOwner} from '../../../shell/remembered-reading-owner';

export type RememberedReading = ReturnType<typeof createRememberedReadingOwner>;
export type RememberedReadingProps = Readonly<{reading: RememberedReading; noticeCopy: Readonly<{title: string; body: string}>; current: () => boolean; onBack: () => void}>;

/** A downloaded book or comic opened while the phone can't reach its server. This page borrows one exact root-owned
 * publication, never a shell session; reading settings answer with a notice instead. */
export function useRememberedReading({reading, current: rootCurrent, onBack}: RememberedReadingProps) {
  const active = useRef<{live: boolean} | undefined>(undefined);
  const [ticket] = useState(() => ({live: true}));
  const [notice, setNotice] = useState<object | undefined>();
  const noticeRef = useRef<object | undefined>(undefined);
  const snapshot = useSyncExternalStore(reading.owner.subscribe, reading.owner.snapshot, reading.owner.snapshot);
  const pageCurrent = () => ticket.live && active.current === ticket && rootCurrent() && ticket.live;
  const current = () => pageCurrent() && reading.owner.current() && pageCurrent();
  useLayoutEffect(() => {active.current = ticket;
    return () => {ticket.live = false; noticeRef.current = undefined; if (active.current === ticket) active.current = undefined; void reading.owner.dispose().catch(() => {});};}, [reading, ticket]);
  useEffect(() => {void Promise.resolve().then(() => reading.owner.ensure()).catch(() => {});}, [reading]);
  const openNotice = () => {if (!current() || noticeRef.current) return; const captured = {}; noticeRef.current = captured; setNotice(captured);};
  const closeNotice = (captured: object) => {if (!current() || noticeRef.current !== captured) return; noticeRef.current = undefined; setNotice(undefined);};
  const back = () => {if (!pageCurrent()) return; ticket.live = false; noticeRef.current = undefined; void reading.owner.dispose().catch(() => {}); onBack();};
  // Android Back closes the reader back to the list (a notice or the reader's own sheets, being overlays, answer first).
  const backRef = useRef(back); backRef.current = back;
  useEffect(() => registerBack(() => {backRef.current(); return true;}, {scope: 'surface'}), []);
  return {snapshot, notice, openNotice, closeNotice, back, loading: snapshot.phase === 'idle' || snapshot.phase === 'opening'};
}
