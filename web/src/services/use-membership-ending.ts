import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {membershipEndingIdentity, membershipEndingWasShown, recordMembershipEndingShown, type MembershipEndingNotice} from '@core/membership-ending.ts';
import {accountIdentity, sameAccount, type AccountIdentity, type ServicesAccount} from './session';

/** Reserve this event under the browser/account lock, then record it only from
 * the notice's actual committed layout. Retiring before that commit releases
 * the reservation without marking an abandoned fetch as shown. */
export function useMembershipEnding(account: ServicesAccount, identity: AccountIdentity, notice: MembershipEndingNotice | undefined, sourceCurrent: () => boolean) {
  const event = notice && membershipEndingIdentity(notice), {accountId, familyId} = identity;
  const opening = useMemo(() => ({notice, event, shownHere: false, accountId,
    current: () => {
      const session = account.service.getSnapshot().session;
      return Boolean(sourceCurrent() && session?.familyId && sameAccount(accountIdentity(session), {accountId, familyId}) && notice && Date.parse(notice.expiresAt) > Date.now());
    },
  }), [notice, event, account, accountId, familyId, sourceCurrent]);
  type Flight = {live: boolean; abort: AbortController; confirm?: (shown: boolean) => void};
  const mount = useRef<HTMLDivElement | null>(null);
  const active = useRef<Flight | null>(null), [visible, setVisible] = useState<{opening: typeof opening; flight: Flight}>();
  useLayoutEffect(() => {
    const flight: Flight = {live: true, abort: new AbortController()}; active.current = flight;
    const live = () => flight.live && active.current === flight && !flight.abort.signal.aborted && opening.current();
    if (opening.notice && live()) {
      if (opening.shownHere) setVisible({opening, flight});
      else if (navigator.locks) {
        void navigator.locks.request('portico.services.ending-shown.v1:' + opening.accountId, {signal: flight.abort.signal}, async () => {
          if (!live() || !opening.notice) return;
          const store = window.localStorage;
          if (membershipEndingWasShown(store, opening.accountId, opening.notice, Date.now())) return;
          // Keep the cross-tab lock until the layout below confirms DOM mount,
          // or cleanup explicitly releases an opening that never mounted.
          await new Promise<boolean>(resolve => {flight.confirm = resolve; setVisible({opening, flight});});
        }).catch(() => {if (live()) setVisible(undefined);});
      }
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    const deadline = () => {
      if (!flight.live || active.current !== flight || !opening.notice) return;
      const remaining = Date.parse(opening.notice.expiresAt) - Date.now();
      if (remaining <= 0) {setVisible(undefined); flight.confirm?.(false); flight.confirm = undefined;}
      else timer = setTimeout(deadline, Math.min(2147483647, remaining));
    };
    deadline();
    return () => {clearTimeout(timer); flight.live = false; if (active.current === flight) active.current = null; flight.abort.abort(); flight.confirm?.(false); flight.confirm = undefined;};
  }, [opening]);
  useLayoutEffect(() => {
    const flight = active.current;
    if (visible?.opening !== opening || visible.flight !== flight || !flight?.live || !flight.confirm) return;
    const confirm = flight.confirm; flight.confirm = undefined;
    let recorded = false;
    try {if (mount.current?.isConnected && opening.current() && opening.notice) recorded = recordMembershipEndingShown(window.localStorage, opening.accountId, opening.notice, Date.now());}
    catch { /* A failed or malformed durable store suppresses repetition. */ }
    if (recorded) opening.shownHere = true;
    else setVisible(undefined);
    confirm(recorded);
    // A confirm is installed only alongside a fresh visible object above, and
    // consumed once here. Unrelated commits cannot introduce another confirm.
  }, [visible, opening]);
  return {mount, visible: visible?.opening === opening && visible.flight === active.current && active.current?.live === true && opening.current()};
}
