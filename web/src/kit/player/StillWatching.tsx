import {defaultI18n} from '@i18n';
import {MetadataFacts} from '../MetadataFacts';
import {Button} from '../controls/Button';
import {Dialog} from '../overlays';
import styles from './still-watching.module.css';

/** phone-player f6e, the drawn part: "Still watching?" naming the episode that waits, after the episodes set in
 * Settings. The kit modal owns the scrim, focus trap and input, so nothing under it takes a click. Nothing starts
 * until the viewer says so; the screen (`screens/watch/StillWatchingPrompt`) owns the policy and the actions. */
export function PlayerStillWatching({episode, title, busy = false, onContinue, onReplay, onClose}: Readonly<{
  /** "S1 E3", when the next one is an episode. */
  episode?: string; title: string; busy?: boolean;
  onContinue: () => void;
  /** Replay, only when it is offered. */
  onReplay?: () => void;
  onClose: () => void;
}>) {
  const t = defaultI18n.t;
  return <Dialog footerDismisses open onOpenChange={() => {}} title={t('player.post.stillWatching')} responsive={false} width={400}
    showCloseButton={false} dismissOnOutsidePress={false} dismissOnBack={false}
    className={styles.dialog} headerClassName={styles.header} bodyClassName={styles.body} footerClassName={styles.footer}
    footer={<>
      <Button variant="primary" size="small" disabled={busy} onClick={onContinue}>{t('player.post.stillWatchingAction')}</Button>
      {onReplay && <Button variant="glass" size="small" icon="i-restart" disabled={busy} onClick={onReplay}>{t('player.post.replay')}</Button>}
      <Button variant="glass" size="small" disabled={busy} onClick={onClose}>{t('player.post.close')}</Button>
    </>}>
    <p className={styles.next}><MetadataFacts parts={[t('player.post.upNext'), episode && <b key="episode">{episode}</b>, <b key="title">{title}</b>]} /></p>
    <p>{t('player.post.pausedAfterEpisodes')}</p>
  </Dialog>;
}
