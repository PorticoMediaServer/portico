import {useEffect, useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {playbackFailure} from '@core/presentation/playback-failure.ts';
import {Button} from '../controls';
import {DetailsPanel, StateLayout, type StateAction} from './State';
import {OwnerTag, ServerMark} from '../shell';
import type {IconName} from '../Icon';
import styles from './playback-failure.module.css';
const t = defaultI18n.t;
/** The owner's part of a failure (states.html frames 8 and 10): plain sentences, the server's code and when, behind the
 * owner's mark; `copyText` is what Copy details copies. Only ever given for the owner. */
export type PlaybackFailureOwner = Readonly<{paragraphs: readonly string[]; codes: readonly string[]; time?: string; copyText: string}>;
/** Safe existing failure vocabulary; no raw engine diagnostics or transport ownership. */
export function PlaybackFailure({error, onRetry, onVersions, onReport, onClose, onError, closeLabel, children, heading, sentence, retryLabel, layout = 'player', phone = false, portrait = false, icon = 'i-warn', owner}: Readonly<{
  /** The owner's Details (frames 8 and 10): under the buttons on a desktop, behind Details on a phone, in a compact card. */ owner?: PlaybackFailureOwner;
  /** The upright phone player: only a strip of picture, so the words and buttons fit it without the icon. */ portrait?: boolean;
  /** The state's icon: a wait shows the clock (states.html, the busy conversion). */ icon?: IconName;
  /** The full player on a phone (states.html frame 10): the same words and buttons, sized for a thumb. */ phone?: boolean;
  /** `compact`: a card for a start that failed before its first frame (J-27); `player` fills the full player. */ layout?: 'player' | 'compact';
  error?: string; onRetry?: () => Promise<void>; onVersions?: () => void; onReport?: () => void; onClose: () => void; onError: (error: unknown) => void;
  /** The drawn owner variant names the setting this card closes to; absent keeps "Close". */ closeLabel?: string;
  /** The drawn action's own words, e.g. the busy-converting card's Try again now; absent keeps Try again. */ retryLabel?: string;
  /** The drawn state's own body content, e.g. the account's other playbacks; no layout changes. */ children?: ReactNode;
  /** A own heading/sentence for a card that is not the mapped playback failure, e.g. the
   * server's directed conversion wait; absent keeps the existing vocabulary. */
  heading?: ReactNode; sentence?: ReactNode;
}>) {
  const failure = playbackFailure(error), [busy, setBusy] = useState(false), [copied, setCopied] = useState(false), [details, setDetails] = useState(false);
  const running = useRef(false), alive = useRef(true);
  useEffect(() => {alive.current = true;return () => {alive.current = false;};}, []);
  const retry = async () => {if (running.current || !onRetry) return;running.current = true;setBusy(true);try {await onRetry();} catch (error) {if (alive.current) onError(error);} finally {running.current = false;if (alive.current) setBusy(false);}};
  const title = heading ?? t(failure.title), body = sentence ?? (failure.body ? t(failure.body) : failure.text);
  const ownerPanel = (compact: boolean) => owner && <DetailsPanel compact={compact} audience="owner" facts={[]} owner={{ownerMark: <ServerMark size={26}/>, ownerTag: <OwnerTag/>,
    paragraphs: owner.paragraphs, codes: owner.codes, ...(owner.time ? {time: owner.time} : {}),
    actions: [{id: 'copy', icon: 'i-copy', label: copied ? t('next.overlays.detailsCopied') : t('next.states.copyDetails'),
      onPress: () => {void navigator.clipboard?.writeText(owner.copyText).then(() => {if (alive.current) setCopied(true);}, () => {});}}]}}/>;
  if (layout === 'player') {
    // states.html frames 8 and 10: the picture stays, dimmed; the failure takes the place of the hero's words, as every
    // full-page state does: its icon, title and sentence, a white Try again where another try can fix it, then a glass
    // button and text links. The player's own top and bottom rows stay around it.
    const tryAgain: StateAction | undefined = !failure.permanent && onRetry ? {id: 'retry', label: retryLabel ?? t('next.states.tryAgain'), icon: 'i-retry', disabled: busy, loading: busy, onPress: () => {void retry();}} : undefined;
    const versions: StateAction | undefined = onVersions ? {id: 'versions', label: t('browse.field.versions'), icon: 'i-play', onPress: onVersions} : undefined;
    const close: StateAction = {id: 'close', label: closeLabel ?? t('action.close'), onPress: onClose};
    const report: StateAction | undefined = onReport ? {id: 'report', label: t('web.player.error.report'), icon: 'i-flag', onPress: onReport} : undefined;
    return <section className={styles.failure} data-layout="player" role="alert">
      <StateLayout variant={phone ? 'landscape' : 'player'} backdrop={null} icon={portrait ? undefined : icon} tone="warning" title={title} body={body}
        primary={tryAgain} secondary={versions ?? close} links={[...(versions ? [close] : []), ...(report ? [report] : [])]}
        {...(owner ? phone ? {details: {open: details, onOpenChange: setDetails, content: ownerPanel(true)}} : {afterActions: ownerPanel(false)} : {})}>{children}</StateLayout>
    </section>;
  }
  return <section className={styles.failure} data-layout={layout} role="alert">
    <h2>{title}</h2><p>{body}</p>
    {children}
    <div>{!failure.permanent && onRetry && <Button variant="primary" disabled={busy} loading={busy} onClick={() => {void retry();}}>{retryLabel ?? t('next.states.tryAgain')}</Button>}
      {onVersions && <Button variant="glass" onClick={onVersions}>{t('browse.field.versions')}</Button>}
      {onReport && <Button variant="glass" onClick={onReport}>{t('web.player.error.report')}</Button>}
      <Button variant="glass" onClick={onClose}>{closeLabel ?? t('action.close')}</Button></div>
    {ownerPanel(true)}
  </section>;
}
