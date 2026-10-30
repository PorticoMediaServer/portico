import type {CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {Icon} from '../../kit/Icon';
import {currentWatchPolicy, dispatchWatchMarker, type WatchPolicyBinding} from './policy';
import {WatchPromptGlyph} from './PromptGlyph';
import styles from './skip-prompt.module.css';
import motion from './prompt-motion.module.css';
import {promptScope, promptSnapStyle, usePromptMotion, usePromptPresence, usePromptReducedMotion} from './prompt-motion';
import {WatchPromptRetirement, useWatchPromptControls} from './prompt-focus';

/** desktop-player f2, phone-player r2 and tablet-play f1. No engine, skip clock or timer. */
export function WatchSkipPrompt({binding, snapshot, expanded, controlsVisible, layout, style}: {
  binding: WatchPolicyBinding; snapshot: WebPlaybackRuntimeSnapshot; expanded: boolean; controlsVisible: boolean;
  layout: 'desktop' | 'tablet' | 'phone'; style?: CSSProperties;
}) {
  const current = currentWatchPolicy(binding, snapshot), policy = current?.shown, live = current?.live;
  const skipPrompt = policy?.prompt?.kind === 'skip' ? policy.prompt : undefined;
  const liveSkip = live?.prompt?.kind === 'skip' ? live.prompt : undefined;
  const open = expanded && !!skipPrompt && !!liveSkip && !policy?.attention && !policy?.finished
    && !live?.attention && !live?.finished && liveSkip.marker.id === skipPrompt.marker.id;
  const reduced = usePromptReducedMotion();
  const drawnControls = useWatchPromptControls(controlsVisible);
  const displayed = open && skipPrompt ? {marker: skipPrompt.marker, snapshot, binding} : undefined;
  const presence = usePromptPresence(displayed, displayed?.marker.id, promptScope(snapshot, expanded, binding), reduced);
  const motionRef = usePromptMotion(presence.phase, presence.paintKey, reduced);
  const shown = presence.present;
  if (!shown) return null;
  const act = (kind: 'skip' | 'dismiss') => { void dispatchWatchMarker(shown.binding, shown.snapshot, {kind, markerId: shown.marker.id}).catch(shown.binding.reportError); };
  return <WatchPromptRetirement active={presence.phase === 'in'} paintKey={presence.paintKey} element={motionRef}><div key={presence.paintKey} ref={motionRef} className={`${styles.corner} ${motion.prompt}`} data-watch-skip data-layout={layout} data-controls-visible={drawnControls}
    data-prompt-phase={presence.phase} inert={presence.phase === 'out'} aria-hidden={presence.phase === 'out' || undefined}
    style={{...promptSnapStyle, ...style}}>
    {layout !== 'phone' && <button className={styles.dismiss} type="button" aria-label={defaultI18n.t('action.dismiss')} onClick={() => act('dismiss')}><WatchPromptGlyph kind="dismiss" className={styles.glyph} /></button>}
    <button className={styles.skip} type="button" onClick={() => act('skip')}>{defaultI18n.t('player.skip', {kind: shown.marker.kind})}{layout === 'phone' ? <Icon name="i-next" size={18} /> : <WatchPromptGlyph kind="next" className={styles.glyph} />}</button>
  </div></WatchPromptRetirement>;
}
