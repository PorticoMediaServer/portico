import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {accountMoveLink, type AccountMoveCode} from '@core/account-move.ts';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls/Button';
import {Dialog} from '../../kit/overlays/Dialog';
import styles from './move-codes-sheet.module.css';

const t = defaultI18n.t;
/** The parent owns the one-time receipt and clears it on close/authority retirement.
 * This leaf never makes or recovers codes. Explicit copy/QR uses only the
 * canonical fragment link; no secret goes to navigation, network or app storage. */
export function MoveCodesSheet({codes, serverName, address, isCurrent, onClose, onError}: {
  codes: readonly AccountMoveCode[]; serverName: string; address: string;
  isCurrent: () => boolean; onClose: () => void; onError: (error: unknown) => void;
}) {
  const scope = useMemo(() => ({codes: Object.freeze(codes.map(row => Object.freeze({...row}))),
    serverName, address, isCurrent, onClose, onError}), [codes, serverName, address, isCurrent, onClose, onError]);
  type Ticket = {scope: typeof scope; live: boolean; copying: boolean; qrRequest?: object};
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  const [publicQR, setPublicQR] = useState<{ticket: Ticket; path: string; size: number}>();
  const [qr, setQR] = useState<{ticket: Ticket; row: AccountMoveCode; path: string; size: number}>();
  if (active.current && (active.current.scope !== scope || !isCurrent())) {
    active.current.live = false; active.current = undefined;
  }
  useLayoutEffect(() => {
    const ticket: Ticket = {scope, live: true, copying: false}; active.current = ticket;
    setCommitted(ticket); setQR(undefined); setPublicQR(undefined);
    // Preserve the public server-address QR alongside explicit per-code QRs.
    void import('qrcode').then(encoder => {
      if (!ticket.live || active.current !== ticket || !scope.isCurrent()) return;
      const cells = encoder.create(scope.address, {errorCorrectionLevel: 'M'}).modules;
      let path = '';
      for (let y = 0; y < cells.size; y++) for (let x = 0; x < cells.size; x++) if (cells.get(y, x)) path += `M${x} ${y}h1v1h-1z`;
      if (ticket.live && active.current === ticket && scope.isCurrent()) setPublicQR({ticket, path, size: cells.size});
    }).catch(() => {if (ticket.live && active.current === ticket && scope.isCurrent()) scope.onError(new Error('move_codes_qr_failed'));});
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined;};
  }, [scope]);
  const current = (ticket = committed) => !!ticket?.live && active.current === ticket && ticket.scope === scope && scope.isCurrent();
  const copy = async (row?: AccountMoveCode) => {
    const ticket = committed;
    if (!current(ticket) || !ticket || ticket.copying || row && !scope.codes.includes(row)) return;
    ticket.copying = true;
    try {
      const text = row ? accountMoveLink(scope.address, row.code, row.serverId)
        : [scope.serverName, scope.address, ...scope.codes.map(value => `${value.username}\t${value.code}\t${defaultI18n.dateTime(value.expiresAt)}`)].join('\n');
      if (current(ticket)) await navigator.clipboard.writeText(text);
    } catch {if (current(ticket)) scope.onError(new Error('move_codes_copy_failed'));}
    finally {ticket.copying = false;}
  };
  const showQR = async (row: AccountMoveCode) => {
    const ticket = committed;
    if (!current(ticket) || !ticket || !scope.codes.includes(row)) return;
    const request = {}; ticket.qrRequest = request; setQR(undefined);
    const owns = () => current(ticket) && ticket.qrRequest === request;
    try {
      const link = accountMoveLink(scope.address, row.code, row.serverId);
      const encoder = await import('qrcode');
      if (!owns()) return;
      const cells = encoder.create(link, {errorCorrectionLevel: 'M'}).modules;
      let path = '';
      for (let y = 0; y < cells.size; y++) for (let x = 0; x < cells.size; x++) if (cells.get(y, x)) path += `M${x} ${y}h1v1h-1z`;
      if (owns()) setQR({ticket, row, path, size: cells.size});
    } catch {if (owns()) scope.onError(new Error('move_codes_qr_failed'));}
  };
  const close = () => {if (current() && committed) {committed.live = false; setQR(undefined); scope.onClose();}};
  if (!current()) return null;
  const publicWitness = publicQR && publicQR.ticket === committed ? publicQR : undefined;
  const receiptWitness = qr && qr.ticket === committed ? qr : undefined;
  return <Dialog open title={t('accounts.move.sheetTitle')} width={760} className={styles.sheet}
    onOpenChange={value => {if (!value) close();}} footer={<>
      <Button onClick={() => {void copy();}}>{t('accounts.move.copyList')}</Button>
      <Button onClick={() => {if (current()) window.print();}}>{t('accounts.move.print')}</Button>
      <Button variant="primary" onClick={close}>{t('accounts.move.done')}</Button>
    </>}>
    <article className={styles.paper}>
      <h2>{serverName}</h2><h3>{t('accounts.move.sheetHeading')}</h3>
      <p>{t('accounts.move.sheetHelp', {address})}</p>
      {publicWitness && <svg viewBox={`-4 -4 ${publicWitness.size + 8} ${publicWitness.size + 8}`} role="img" aria-label={t('qr.label')} shapeRendering="crispEdges"><path d={publicWitness.path}/></svg>}
      <table><thead><tr><th>{t('accounts.move.sheetUsername')}</th><th>{t('accounts.move.code')}</th><th>{t('accounts.move.validUntil')}</th></tr></thead>
        <tbody>{scope.codes.map(row => <tr key={row.accountId}><td>{row.username}</td><td className={styles.code}>{row.code}<div className={styles.codeActions}><Button size="small" onClick={() => {void copy(row);}}>{t('web.access.copyLink')}</Button><Button size="small" onClick={() => {void showQR(row);}}>{t('qr.label')}</Button></div></td><td>{defaultI18n.dateTime(row.expiresAt)}</td></tr>)}</tbody></table>
      {receiptWitness && <figure><figcaption>{receiptWitness.row.username}</figcaption><svg viewBox={`-4 -4 ${receiptWitness.size + 8} ${receiptWitness.size + 8}`} role="img" aria-label={t('qr.label')} shapeRendering="crispEdges"><path d={receiptWitness.path}/></svg></figure>}
    </article>
  </Dialog>;
}
