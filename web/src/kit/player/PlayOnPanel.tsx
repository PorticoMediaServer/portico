import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import {BottomSheet, Popover} from '../overlays';
import type {PlayOnController, PlayOnLinkPreview, PlayOnLinkPort, PlayOnReceiver, PlayOnReceiversPort} from '../../platform/playback/play-on';
import type {CastPlayerTranslate, PlayOnPanelProps} from './types';
import styles from './cast-player.module.css';

const EMPTY = Object.freeze({phase: 'idle' as const, items: Object.freeze([]) as readonly PlayOnReceiver[]});
const noReceivers = () => EMPTY;
const PLATFORMS: Readonly<Record<string, string>> = {appletv: 'Apple TV', tvos: 'Apple TV', ios: 'iPhone', ipados: 'iPad', androidtv: 'Android TV', android: 'Android', web: 'Web'};

type LinkState = Readonly<{phase: 'closed' | 'code' | 'busy' | 'preview' | 'done' | 'failed'; code: string; preview?: PlayOnLinkPreview}>;
const closedLink: LinkState = {phase: 'closed', code: ''};
/** One rendered opening. Its id, not a ref read at invocation, identifies it, and its
 * stage is the only live authority for a decision: a preview that is not the review
 * stage can never be approved. */
type Opening = {id: number; stage: 'code' | 'review' | 'busy' | 'done' | 'failed'; preview?: PlayOnLinkPreview};

function Receivers({port, t, onSend, sending, failed}: {port: PlayOnReceiversPort; t: CastPlayerTranslate; onSend(id: string): void; sending?: string; failed?: string}) {
  const snapshot = useSyncExternalStore(port.subscribe, port.getSnapshot, noReceivers);
  useEffect(() => { port.refresh(); }, [port]);
  if (snapshot.items.length === 0) return null;
  return <>
    <p className={styles.menuHeading}>{t('next.cast.onThisNetwork')}</p>
    <div className={styles.deviceGroup}>{snapshot.items.map(receiver => <button key={receiver.id} type="button" role="menuitem" className={styles.deviceRow}
      disabled={sending !== undefined} onClick={() => onSend(receiver.id)}>
      <Icon name="i-tvdev" size={20} /><span>{receiver.name}{receiver.detail && <small>{receiver.detail}</small>}</span>{sending === receiver.id ? <small>{t('next.cast.receiverBusy')}</small> : <Icon name="i-right" size={18} />}
    </button>)}</div>
    {failed && <p className={styles.footnote} role="status">{t('next.cast.receiverFailed', {device: snapshot.items.find(receiver => receiver.id === failed)?.name ?? ''})}</p>}
  </>;
}

/** One opening's code flow. Every handler carries the opening it was rendered for and
 * the exact preview it showed; nothing is derived from a ref at invocation. */
function LinkTv({t, state, onCode, onLookup, onDecide, onRetry, onClose}: {
  t: CastPlayerTranslate; state: LinkState;
  onCode(value: string): void; onLookup(): void; onDecide(preview: PlayOnLinkPreview, decision: 'approve' | 'deny'): void;
  onRetry(): void; onClose(): void;
}) {
  const busy = state.phase === 'busy';
  if (state.phase === 'closed') return null;
  if (state.phase === 'failed') return <div className={styles.linkForm}><p className={styles.footnote} role="status">{t('next.cast.linkTvFailed')}</p>
    <button type="button" className={styles.done} onClick={onRetry}>{t('next.cast.linkTvRetry')}</button></div>;
  if (state.phase === 'done') return <div className={styles.linkForm}><p className={styles.footnote} role="status">{t('next.cast.linkTvApproved')}</p>
    <button type="button" className={styles.done} onClick={onClose}>{t('next.controls.done')}</button></div>;
  if (state.preview) return <div className={styles.linkForm} role="group" aria-label={t('next.cast.linkTv')}>
    <p className={styles.footnote}><b>{t('next.cast.linkTvReview', {device: state.preview.deviceName})}</b>
      {state.preview.platform && <small>{PLATFORMS[state.preview.platform] ?? state.preview.platform}</small>}</p>
    <div className={styles.linkActions}><button type="button" className={styles.deviceRow} disabled={busy} onClick={() => state.preview && onDecide(state.preview, 'approve')}><Icon name="i-check" size={18} /><span>{t('next.cast.linkTvApprove')}</span></button>
      <button type="button" className={styles.deviceRow} disabled={busy} onClick={() => state.preview && onDecide(state.preview, 'deny')}><Icon name="i-x" size={18} /><span>{t('next.cast.linkTvDeny')}</span></button></div>
  </div>;
  return <form className={styles.linkForm} onSubmit={event => { event.preventDefault(); onLookup(); }}>
    <label className={styles.linkLabel} htmlFor="link-tv-code">{t('next.cast.linkTvCode')}</label>
    <div className={styles.linkRow}>
      <input id="link-tv-code" className={styles.linkInput} value={state.code} maxLength={10} autoComplete="off" autoCapitalize="characters" spellCheck={false}
        disabled={busy} onChange={event => onCode(event.currentTarget.value.toUpperCase())} />
      <button type="submit" className={styles.done} disabled={busy}>{busy ? t('next.cast.linkTvConnecting') : t('next.cast.linkTvConnect')}</button>
    </div>
    <p className={styles.footnote}>{t('next.cast.linkTvHint')}</p>
  </form>;
}

/** The drawn Play on button. It opens the authoritative panel, which owns every guard. */
export function PlayOnButton({controller, available, buttonRef, onOpen, t = defaultI18n.t}: Readonly<{
  controller: PlayOnController; available: boolean; buttonRef?: RefObject<HTMLButtonElement | null>; onOpen?: () => void; t?: CastPlayerTranslate;
}>) {
  if (!available) return null;
  return <button type="button" ref={buttonRef} className={styles.playOnButton} aria-label={t('next.dock.playOnDevice')} aria-haspopup="menu"
    onClick={() => { onOpen?.(); controller.openPlayOn(); }}><Icon name="i-cast" size={24} /></button>;
}

/** Desktop f1s and the phone sheet, with the binding browser rows, the Portico televisions
 * the server names, and the code that signs a television in. */
export function PlayOnPanel({controller, snapshot, phone, anchorRef, reportError, receivers, linkTv, t = defaultI18n.t}: PlayOnPanelProps) {
  const open = snapshot.panel === 'play-on' && !snapshot.disposed;
  const [sending, setSending] = useState<string | undefined>(undefined), [failed, setFailed] = useState<string | undefined>(undefined);
  const [link, setLink] = useState<LinkState>(closedLink);
  const opening = useRef<Opening | null>(null), seq = useRef(0), flight = useRef<AbortController | null>(null);
  // Retires the rendered opening, aborts its request and clears its display. Called
  // synchronously on close, on a controller or port replacement, and on unmount.
  const retire = useCallback((id?: number) => {
    if (id !== undefined && opening.current?.id !== id) return false;
    seq.current++; opening.current = null; flight.current?.abort(); flight.current = null; setLink(closedLink); return true;
  }, []);
  useLayoutEffect(() => { retire(); }, [controller, linkTv, retire]);
  useLayoutEffect(() => { if (!open) retire(); }, [open, retire]);
  useLayoutEffect(() => () => { retire(); }, [retire]);
  useEffect(() => { if (open) receivers?.refresh(); }, [open, receivers]);
  if (!snapshot.cast.available && !snapshot.airPlayAvailable && !receivers) return null;
  const close = () => { retire(); controller.closePanel(); };
  const send = (id: string) => {
    if (!receivers) return;
    setSending(id); setFailed(undefined);
    void receivers.send(id).then(taken => { setSending(undefined); if (taken) close(); else setFailed(id); })
      .catch(error => { setSending(undefined); setFailed(id); reportError(error); });
  };
  const beginLink = () => {
    retire();
    const id = ++seq.current;
    opening.current = {id, stage: 'code'};
    setLink({phase: 'code', code: ''});
    return id;
  };
  const live = (id: number) => opening.current?.id === id ? opening.current : null;
  const settle = (id: number, next: LinkState) => { const current = live(id); if (current && current.stage !== 'done') setLink(next); };
  // The rendered id is captured by this render's handlers, so a retained old control
  // can only ever act on the opening it was drawn for.
  const renderedId = opening.current?.id ?? 0;
  const preview = (id: number, code: string) => {
    const current = live(id);
    if (!linkTv || !current || current.stage === 'done' || flight.current) return;
    if (code.length < 4) { current.preview = undefined; current.stage = 'failed'; settle(id, {phase: 'failed', code}); return; }
    const life = new AbortController();
    current.preview = undefined;
    current.stage = 'code';
    flight.current = life;
    settle(id, {phase: 'busy', code});
    void linkTv.preview(code, {signal: life.signal}).then(result => {
      if (life.signal.aborted) return;
      const live = opening.current;
      if (!live || live.id !== id || live.stage !== 'code') return;
      live.preview = result;
      live.stage = 'review';
      setLink({phase: 'preview', code, preview: result});
    }).catch(() => {
      if (life.signal.aborted) return;
      const live = opening.current;
      if (!live || live.id !== id) return;
      live.preview = undefined; live.stage = 'failed';
      setLink({phase: 'failed', code});
    }).finally(() => { if (flight.current === life) flight.current = null; });
  };
  const decide = (id: number, shown: PlayOnLinkPreview, code: string, decision: 'approve' | 'deny') => {
    const current = live(id);
    // Only the review stage this control was drawn for may decide, and the decision is
    // claimed synchronously so no second control can spend the same consent.
    if (!linkTv || !current || current.stage !== 'review' || current.preview !== shown || flight.current) return;
    const life = new AbortController();
    current.stage = 'busy';
    flight.current = life;
    setLink({phase: 'busy', code, preview: shown});
    void linkTv.decide(shown.requestId, code, decision, {signal: life.signal}).then(() => {
      if (life.signal.aborted) return;
      const live = opening.current;
      if (!live || live.id !== id || live.preview !== shown) return;
      if (decision === 'approve') { live.stage = 'done'; setLink({phase: 'done', code, preview: shown}); return; }
      retire(id); controller.closePanel();
    }).catch(error => {
      if (life.signal.aborted) return;
      const live = opening.current;
      if (!live || live.id !== id || live.preview !== shown) return;
      live.stage = 'failed';
      setLink({phase: 'failed', code});
      reportError(error);
    }).finally(() => { if (flight.current === life) flight.current = null; });
  };
  const rows = <>
    <div className={styles.deviceGroup}>
      {snapshot.airPlayAvailable && <button type="button" role={phone ? undefined : 'menuitem'} className={styles.deviceRow} onClick={() => controller.startAirPlay()}>
        <Icon name="i-airplay" size={20} /><span>{t('playOn.airplay')}<small>{t('playOn.airplaySubtitle')}</small></span></button>}
      {snapshot.cast.available && <button type="button" role={phone ? undefined : 'menuitem'} className={styles.deviceRow} onClick={() => { void controller.startCast().catch(reportError); }}>
        <Icon name="i-cast" size={20} /><span>{t('playOn.googleCast')}<small>{t('next.cast.browserDevices')}</small></span></button>}
    </div>
    {receivers && <Receivers port={receivers} t={t} onSend={send} sending={sending} failed={failed} />}
    {linkTv && link.phase === 'closed' && <div className={styles.deviceGroup}>
      <button type="button" role={phone ? undefined : 'menuitem'} className={styles.deviceRow} onClick={beginLink}>
        <Icon name="i-tv" size={20} /><span>{t('next.cast.linkTv')}<small>{t('next.cast.linkTvHint')}</small></span><Icon name="i-right" size={18} /></button>
    </div>}
    {linkTv && <LinkTv t={t} state={link}
      onCode={value => { const current = live(renderedId); if (!current || current.stage === 'done') return; current.preview = undefined; current.stage = 'code'; settle(renderedId, {phase: 'code', code: value}); }}
      onLookup={() => preview(renderedId, link.code.trim().toUpperCase())}
      onDecide={(shown, decision) => decide(renderedId, shown, link.code.trim().toUpperCase(), decision)}
      onRetry={() => { const current = live(renderedId); if (!current || current.stage === 'done') return; current.preview = undefined; current.stage = 'code'; settle(renderedId, {phase: 'code', code: link.code}); }}
      onClose={() => { if (retire(renderedId)) controller.closePanel(); }} />}
    <div className={styles.separator} role="separator" /><p className={styles.footnote}>{t('next.cast.browserFootnote')}</p>
  </>;
  if (phone) return <BottomSheet open={open} onOpenChange={close} title={t('next.dock.playOnDevice')} fitContent returnFocusRef={anchorRef} showCloseButton={false}
    className={styles.playOnSheet} bodyClassName={styles.sheetBody}>
    <button type="button" className={styles.done} onClick={close}>{t('next.controls.done')}</button>{rows}
  </BottomSheet>;
  return <Popover open={open} onOpenChange={close} title={t('next.dock.playOnDevice')} anchorRef={anchorRef} returnFocusRef={anchorRef} width={300}
    role={link.phase === 'closed' ? 'menu' : 'dialog'} className={styles.playOnPanel} sideOffset={12} onContentKeyDown={event => {
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
      const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
      const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
      const target = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (at + (event.key === 'ArrowUp' ? -1 : 1) + buttons.length) % buttons.length;
      event.preventDefault(); buttons[target]?.focus();
    }}>
    <div className={styles.menuHeading}>{t('next.dock.playOnDevice')}</div>{rows}
  </Popover>;
}
