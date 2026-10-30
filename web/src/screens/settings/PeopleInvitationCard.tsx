import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {ServerInvitation} from '@core/direct-account.ts';
import {Button} from '../../kit/controls';
import wordmark from '../../shell/wordmark.svg';
import type {SettingsInvitations} from './invitations';
import {invitationCardFacts} from '@core/invitation-card.ts';
import {invitationPicture} from '../../kit/accounts/invitation-picture';
import {copyText} from '../../platform/copy-text';
import styles from '../../kit/accounts/InvitationCard.module.css';

const t = defaultI18n.t;
type QR = Readonly<{size: number; get: (row: number, column: number) => number}>;
type Card = Readonly<{ticket: object; link: string; path: string; qr: QR}>;

/** The long link remains its own capability, beside the shown-once short code.
 * QR encoding and picture export are local; neither sends the link elsewhere. */
export function PeopleInvitationCard({open, invitation, invitations, serverName, libraries, isCurrent, signIn}: {
  open: boolean; signIn?: 'local' | 'portico'; invitation: ServerInvitation; invitations: SettingsInvitations; serverName: string;
  libraries: readonly {id: string; name: string}[]; isCurrent: () => boolean;
}) {
  const facts = useMemo(() => invitationCardFacts(serverName, invitation, libraries), [serverName, invitation, libraries]);
  const scope = useMemo(() => ({open, invitation, invitations, facts, isCurrent}), [open, invitation, invitations, facts, isCurrent]);
  const active = useRef<{scope: typeof scope; request: AbortController} | undefined>(undefined);
  const [card, setCard] = useState<Card>(), [error, setError] = useState(false), [copied, setCopied] = useState(false), [retry, setRetry] = useState(0);
  const [expired, setExpired] = useState<typeof scope>();
  if (active.current && (active.current.scope !== scope || !isCurrent())) {active.current.request.abort(); active.current = undefined;}
  useLayoutEffect(() => {
    setCard(undefined); setError(false); setCopied(false);
    const expiresAt = Date.parse(facts.expiresAt);
    if (!open || !isCurrent() || invitation.state !== 'pending' || !(expiresAt > Date.now())) return;
    const ticket = {scope, request: new AbortController()}; active.current = ticket;
    const current = () => active.current === ticket && !ticket.request.signal.aborted && scope.isCurrent() && expiresAt > Date.now();
    let timer: ReturnType<typeof setTimeout>;
    const expire = () => {
      if (active.current !== ticket) return;
      const remaining = expiresAt - Date.now();
      if (remaining > 0) {timer = setTimeout(expire, Math.min(remaining, 2_147_483_647));return;}
      ticket.request.abort();active.current = undefined;setCard(undefined);setExpired(scope);
    };
    expire();
    void (async () => {
      try {
        const link = await invitations.link(invitation, {signal: ticket.request.signal, isCurrent: current});
        if (!current()) return;
        const encoder = await import('qrcode');
        if (!current()) return;
        const qr = encoder.create(link, {errorCorrectionLevel: 'M'}).modules;
        let path = '';
        for (let row = 0; row < qr.size; row++) for (let column = 0; column < qr.size; column++) if (qr.get(row, column)) path += `M${column} ${row}h1v1h-1z`;
        if (current()) setCard({ticket, link, path, qr});
      } catch {if (current()) setError(true);}
    })();
    return () => {clearTimeout(timer);ticket.request.abort(); if (active.current === ticket) active.current = undefined;};
  }, [scope, open, invitation, invitations, facts, isCurrent, retry]);
  const available = expired !== scope && invitation.state === 'pending' && Date.parse(facts.expiresAt) > Date.now();
  const current = (value: Card) => open && available && active.current?.scope === scope && value.ticket === active.current && !active.current.request.signal.aborted && isCurrent() && Date.parse(facts.expiresAt) > Date.now();
  const visible = card && current(card) ? card : undefined;
  const access = facts.allLibraries ? t('next.settings.allLibraries') : facts.libraryNames.length === facts.libraryCount && facts.libraryCount <= 3
    ? defaultI18n.list(facts.libraryNames) : t('next.settings.invitations.libraries', {count: facts.libraryCount});
  const expiry = t('api.credential.expires', {when: defaultI18n.dateTime(facts.expiresAt)});
  const copy = async (value: Card, source: HTMLElement) => {
    if (!current(value)) return;
    try {await copyText(value.link, source); if (current(value)) setCopied(true);}
    catch {if (current(value)) setError(true);}
  };
  const share = async (value: Card) => {
    if (!current(value) || typeof navigator.share !== 'function') return;
    try {await navigator.share({title: facts.serverName, url: value.link});}
    catch (reason) {if (current(value) && !(reason instanceof DOMException && reason.name === 'AbortError')) setError(true);}
  };
  const save = async (value: Card) => {
    if (!current(value)) return;
    try {
      const blob = await invitationPicture({qr: value.qr, link: value.link, expiry, serverName: facts.serverName, access});
      if (!current(value)) return;
      const url = URL.createObjectURL(blob), link = document.createElement('a');
      try {link.href = url; link.download = 'portico-invitation.png'; link.click();} finally {URL.revokeObjectURL(url);}
    } catch {if (current(value)) setError(true);}
  };
  return <div>
    {!available ? <p role="status">{t('accounts.invitation.expiredBody')}</p> : visible ? <>
      <article className={styles.card}>
        <div className={styles.top}><img src={wordmark} alt="Portico" /><span>{expiry}</span></div>
        <p className={styles.why}>{t('accounts.invitation.to')}</p><h2>{facts.serverName}</h2><p className={styles.libraries}>{access}</p>
        <div className={styles.qr}><svg viewBox={`-4 -4 ${visible.qr.size + 8} ${visible.qr.size + 8}`} role="img" aria-label={t('qr.label')} shapeRendering="crispEdges"><rect x={-4} y={-4} width={visible.qr.size + 8} height={visible.qr.size + 8} fill="white" /><path d={visible.path} fill="black" /></svg></div>
        <p className={styles.scan}>{t('accounts.invitation.scanHelp')}</p>
        <p className={styles.link}>{visible.link}</p>
        <p className={styles.fin}>{t('accounts.invitation.shareJoinHelp', {method: signIn ?? 'other'})}</p>
      </article>
      <div className={styles.actions}>
        <Button size="small" icon={copied ? 'i-check' : 'i-copy'} onClick={event => void copy(visible, event.currentTarget)}>{t(copied ? 'next.settings.invitations.linkCopied' : 'web.access.copyLink')}</Button>
        {typeof navigator.share === 'function' && <Button size="small" icon="i-share" onClick={() => void share(visible)}>{t('services.start.shareLink')}</Button>}
        <Button size="small" icon="i-download" onClick={() => void save(visible)}>{t('accounts.invitation.saveAsPicture')}</Button>
      </div>
    </> : !error ? <p role="status">{t('next.settings.loading')}</p> : null}
    {error && <div role="alert"><p>{t('next.states.pageFailedBody')}</p>{!visible && <Button size="small" onClick={() => setRetry(value => value + 1)}>{t('action.tryAgain')}</Button>}</div>}
  </div>;
}
