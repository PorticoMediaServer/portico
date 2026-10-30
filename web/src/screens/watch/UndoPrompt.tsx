import type {CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {Icon} from '../../kit/Icon';
import {dispatchWatchMarker, watchUndoOffer, type WatchPolicyBinding} from './policy';
import corner from './skip-prompt.module.css';
import styles from './undo-prompt.module.css';
import motion from './prompt-motion.module.css';
import {promptScope, promptSnapStyle, usePromptMotion, usePromptPresence, usePromptReducedMotion} from './prompt-motion';
import {WatchPromptRetirement, useWatchPromptControls} from './prompt-focus';

/** desktop-player f2 automatic skip / phone-player 2b. The real policy supplies every tick. */
export function WatchUndoPrompt({binding, snapshot, expanded, controlsVisible, layout, style}: {
  binding: WatchPolicyBinding; snapshot: WebPlaybackRuntimeSnapshot; expanded: boolean; controlsVisible: boolean;
  layout: 'desktop' | 'tablet' | 'phone'; style?: CSSProperties;
}) {
  const offer = watchUndoOffer(binding, snapshot), reduced = usePromptReducedMotion();
  const drawnControls = useWatchPromptControls(controlsVisible);
  const displayed = expanded && offer ? {offer, snapshot, binding} : undefined;
  const presence = usePromptPresence(displayed, displayed?.offer.marker.id, promptScope(snapshot, expanded, binding), reduced);
  const motionRef = usePromptMotion(presence.phase, presence.paintKey, reduced);
  const shown = presence.present;
  if (!shown) return null;
  const undo = shown.offer;
  return <WatchPromptRetirement active={presence.phase === 'in'} paintKey={presence.paintKey} element={motionRef}><div key={presence.paintKey} ref={motionRef} className={`${corner.corner} ${motion.prompt}`} data-watch-undo data-layout={layout} data-controls-visible={drawnControls}
    data-prompt-phase={presence.phase} inert={presence.phase === 'out'} aria-hidden={presence.phase === 'out' || undefined}
    style={{...promptSnapStyle, ...style}} role="status">
    <span className={styles.notice} data-layout={layout}>
      {defaultI18n.t('player.skipped', {kind: undo.marker.kind})}
      <button type="button" onClick={() => { void dispatchWatchMarker(shown.binding, shown.snapshot, {kind: 'undo', markerId: undo.marker.id}).catch(shown.binding.reportError); }}>
        <Icon name="i-back" size={18} />{defaultI18n.t('action.undo')}
      </button>
      <span className={styles.remaining} aria-hidden="true" style={{width: `${undo.fraction * 100}%`}} />
    </span>
  </div></WatchPromptRetirement>;
}
