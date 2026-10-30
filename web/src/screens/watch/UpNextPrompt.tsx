import {useMemo} from 'react';
import {defaultI18n} from '@i18n';
import {durationText} from '@core/presentation/kit-model/text.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {Artwork} from '../../kit/artwork';
import {WatchPromptGlyph} from './PromptGlyph';
import type {WatchBindings} from './types';
import {watchClock} from './model';
import {dispatchWatchMarker} from './policy';
import {dispatchWatchNext, watchUpNextOffer, type WatchNextBinding} from './up-next';
import styles from './up-next.module.css';
import motion from './prompt-motion.module.css';
import {promptScope, promptSnapStyle, usePromptMotion, usePromptPresence, usePromptReducedMotion} from './prompt-motion';
import {WatchPromptRetirement, useWatchPromptControls} from './prompt-focus';

/** desktop-player f4/f4s. Actual policy ticks and queue-window facts are the only inputs. */
export function WatchUpNextPrompt({binding, snapshot, artwork, environment, expanded, controlsVisible, layout}: {
  binding: WatchNextBinding; snapshot: WebPlaybackRuntimeSnapshot; artwork: WatchBindings['artwork']; environment: WatchBindings['environment'];
  expanded: boolean; controlsVisible: boolean; layout: 'desktop' | 'tablet' | 'phone';
}) {
  const offer = watchUpNextOffer(binding, snapshot), reduced = usePromptReducedMotion();
  const drawnControls = useWatchPromptControls(controlsVisible);
  const still = offer?.next.stillUrl;
  const offeredArt = useMemo(() => artwork.foreground(still, 176, 16 / 9, {group: 'playback', priority: 'high'}), [artwork, still]);
  const displayed = expanded && offer ? {offer, snapshot, binding, artwork: offeredArt, environment} : undefined;
  const key = displayed ? `${displayed.offer.prompt.offer.id}|${displayed.offer.next.entryId}|${displayed.offer.next.queueRevision}` : undefined;
  const presence = usePromptPresence(displayed, key, promptScope(snapshot, expanded, binding), reduced);
  const motionRef = usePromptMotion(presence.phase, presence.paintKey, reduced);
  const shown = presence.present, next = shown?.offer.next;
  // A missing still stays unknown. No backdrop, trickplay or fixture image stands in for it.
  // desktop-player f4/f4s and phone-player f6: the same offer, compact on a phone.
  if (!shown || !next) return null;
  const {offer: currentOffer, snapshot: shownSnapshot, binding: shownBinding, artwork: source, environment: shownEnvironment} = shown;
  const {prompt, countdown, progress} = currentOffer, marker = prompt.offer.marker, t = defaultI18n.t;
  const duration = durationText(next.durationMs === undefined ? undefined : next.durationMs / 1000, shownEnvironment.t);
  const episode = next.episode ? shownEnvironment.t('next.kit.episodeCode', {season: next.episode.seasonNumber, episode: next.episode.number}) : undefined;
  const when = countdown ? countdown.automatic ? t('player.upNext.playingIn', {seconds: countdown.seconds})
    : t(prompt.sceneAfterCredits ? 'player.upNext.startsAfterScene' : 'player.upNext.startsIn', {time: watchClock(countdown.seconds)}) : undefined;
  const act = (kind: 'play-now' | 'watch-credits') => {void dispatchWatchNext(shownBinding, shownSnapshot, kind).catch(shownBinding.reportError);};
  const skip = () => {if (marker) void dispatchWatchMarker(shownBinding, shownSnapshot, {kind: 'skip', markerId: marker.id}).catch(shownBinding.reportError);};
  return <WatchPromptRetirement active={presence.phase === 'in'} paintKey={presence.paintKey} element={motionRef}><div key={presence.paintKey} ref={motionRef} role="group" aria-label={t('player.upNext.duringCredits')} className={`${styles.card} ${motion.prompt}`} data-layout={layout} data-controls-visible={drawnControls} data-watch-up-next data-scene-after={prompt.sceneAfterCredits}
    data-prompt-phase={presence.phase} inert={presence.phase === 'out'} aria-hidden={presence.phase === 'out' || undefined} style={promptSnapStyle}>
    <div className={styles.row}>
      <div className={styles.still}><Artwork source={source} alt="" /></div>
      <div className={styles.content}><small>{t('player.upNext.duringCredits')}</small><b>{next.title}</b>
        {(episode || duration) && <div className={styles.facts}>{episode}{episode && duration && <i aria-hidden="true" />}{duration}</div>}
        {when && <div className={styles.when}>{when}</div>}
      </div>
    </div>
    {progress !== undefined && <div className={styles.meter} aria-hidden="true"><i style={{width: `${progress * 100}%`}} /></div>}
    <div className={styles.actions}>
      <button type="button" className={styles.play} onClick={() => act('play-now')}><WatchPromptGlyph kind="play" size={18} />{t('player.upNext.playNow')}</button>
      {prompt.sceneAfterCredits && marker && <button type="button" onClick={skip}><WatchPromptGlyph kind="next" size={18} />{t('player.skip', {kind: marker.kind})}</button>}
      <button type="button" onClick={() => act('watch-credits')}>{t('player.upNext.watchCredits')}</button>
    </div>
    {prompt.sceneAfterCredits && marker && <div className={styles.note}>{t('player.upNext.sceneAfterCredits')}</div>}
  </div></WatchPromptRetirement>;
}
