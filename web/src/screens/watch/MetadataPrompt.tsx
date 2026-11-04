import {defaultI18n} from '@i18n';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../platform/playback';
import corner from './skip-prompt.module.css';
import styles from './undo-prompt.module.css';

/** PB-5: the picture plays on, but its options and offers couldn't be read twice, so the audio,
 * subtitle, quality and options panels can't be drawn. Said once, with Try again, in the player's
 * notice look (desktop-player .undo); nothing is drawn while the read is still being tried. */
export function WatchMetadataPrompt({runtime, snapshot, expanded, controlsVisible, layout}: {
  runtime: WebShellPlaybackRuntime; snapshot: WebPlaybackRuntimeSnapshot; expanded: boolean; controlsVisible: boolean;
  layout: 'desktop' | 'tablet' | 'phone';
}) {
  const fence = snapshot.fence;
  if (!expanded || snapshot.disposed || !fence || fence.kind !== 'queue' || snapshot.problem !== 'playback_metadata_failed') return null;
  return <div className={corner.corner} data-watch-metadata data-layout={layout} data-controls-visible={controlsVisible} role="status">
    <span className={styles.notice} data-layout={layout}>
      {defaultI18n.t('web.player.optionsFailed')}
      <button type="button" onClick={() => runtime.retryMetadata(fence)}>{defaultI18n.t('next.states.tryAgain')}</button>
    </span>
  </div>;
}
