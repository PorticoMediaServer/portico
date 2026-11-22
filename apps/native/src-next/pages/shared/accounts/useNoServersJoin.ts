import {useLayoutEffect, useRef, useState} from 'react';
import {parseJoinLink} from '../../../../../../packages/client-core/src/join-link';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {qrScannerAvailable, scanQRCode} from '../../../platform/qr-scanner';

const ownerCurrent = (current: () => boolean) => {try {return current();} catch {return false;}};

/** Joining with an invitation from the empty directory: the address and code a person typed or scanned. Each edit,
 * busy change or owner change retires the drawn form, so a stale Continue or scan result never joins. Scanning only
 * fills the form; joining still needs Continue. Moved from the old screen as it was. */
export function useNoServersJoin({busy, isCurrent, onJoin}: {busy: boolean; isCurrent: () => boolean; onJoin: (invitation: string, address: string) => void}) {
  const t = defaultI18n.t;
  const [invitation, setInvitation] = useState(''), [address, setAddress] = useState('');
  const link = parseJoinLink(invitation);
  const [, redraw] = useState(0);
  const opening = useRef<{active: boolean; invitation: string; address: string; owner: () => boolean; busy: boolean; scan?: AbortController; failed: boolean} | undefined>(undefined);
  const latestBusy = useRef(busy); latestBusy.current = busy;
  if (!opening.current || !opening.current.active || opening.current.invitation !== invitation || opening.current.address !== address || !ownerCurrent(opening.current.owner) || opening.current.busy !== busy) {
    if (opening.current) {opening.current.active = false; opening.current.scan?.abort();}
    opening.current = {active: true, invitation, address, owner: isCurrent, busy, failed: false};
  }
  const ticket = opening.current;
  useLayoutEffect(() => {if (opening.current === ticket) ticket.active = true; return () => {ticket.active = false; ticket.scan?.abort();};}, [ticket]);
  const current = () => ticket.active && opening.current === ticket && !latestBusy.current && ownerCurrent(ticket.owner);
  const changeAddress = (raw: string) => {if (!current() || raw === ticket.address) return; ticket.active = false; ticket.scan?.abort(); setAddress(raw);};
  const changeInvitation = (raw: string) => {if (!current() || raw === ticket.invitation) return; ticket.active = false; ticket.scan?.abort(); setInvitation(raw);
    const target = parseJoinLink(raw); if (target) setAddress(target.server);};
  const join = () => {if (!current() || ticket.scan || !ticket.invitation.trim()) return; ticket.active = false; onJoin(ticket.invitation, ticket.address);};
  const scan = () => {
    if (!current() || ticket.scan || !qrScannerAvailable()) return;
    const controller = new AbortController(); ticket.scan = controller; ticket.failed = false; redraw(value => value + 1);
    void scanQRCode({signal: controller.signal, isCurrent: () => current() && ticket.scan === controller,
      title: t('accounts.move.scanQR'), cancelLabel: t('action.cancel')}).then(raw => {
      if (!current() || ticket.scan !== controller || raw === undefined) return;
      // Scanning edits the form only. Canonical proof/preview still requires Continue.
      ticket.active = false; controller.abort(); setInvitation(raw);
      const target = parseJoinLink(raw); if (target) setAddress(target.server);
      redraw(value => value + 1);
    }, failure => {
      if (current() && ticket.scan === controller && failure?.code !== 'qr_cancelled') ticket.failed = true;
    }).finally(() => {
      if (ticket.scan !== controller) return; ticket.scan = undefined;
      if (current()) redraw(value => value + 1);
    });
  };
  return {invitation, address: link?.server ?? address, addressLocked: !!link, changeAddress, changeInvitation, join, canJoin: !!invitation.trim(),
    scan, canScan: qrScannerAvailable(), scanning: !!ticket.scan, scanFailed: ticket.failed};
}
