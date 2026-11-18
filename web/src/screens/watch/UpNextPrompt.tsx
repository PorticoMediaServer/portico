import {useMemo} from 'react';
import {defaultI18n} from '@i18n';
import {durationText} from '@core/presentation/kit-model/text.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {Artwork} from '../../kit/artwork';
import type {WatchBindings} from './types';
import {watchClock} from './model';
import {dispatchWatchMarker} from './policy';
import {dispatchWatchNext, watchUpNextOffer, type WatchNextBinding} from './up-next';
import {PlayerUpNext} from '../../kit/player/PlayerUpNext';
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
  const key = displayed ? `${displayed.offer.prompt?.offer.id ?? 'end'}|${displayed.offer.next.entryId}|${displayed.offer.next.queueRevision}` : undefined;
  const presence = usePromptPresence(displayed, key, promptScope(snapshot, expanded, binding), reduced);
  const motionRef = usePromptMotion(presence.phase, presence.paintKey, reduced);
  const shown = presence.present, next = shown?.offer.next;
  // A missing still stays unknown. No backdrop, trickplay or fixture image stands in for it.
  // desktop-player f4/f4s and phone-player f6: the same offer, compact on a phone.
  if (!shown || !next) return null;
  const {offer: currentOffer, snapshot: shownSnapshot, binding: shownBinding, artwork: source, environment: shownEnvironment} = shown;
  // The end of an episode (prompt null) offers the next one after the countdown; there are no credits left to watch.
  const {prompt, countdown, progress} = currentOffer, marker = prompt?.offer.marker, scene = !!prompt?.sceneAfterCredits, t = defaultI18n.t;
  const duration = durationText(next.durationMs === undefined ? undefined : next.durationMs / 1000, shownEnvironment.t);
  const episode = next.episode ? shownEnvironment.t('next.kit.episodeCode', {season: next.episode.seasonNumber, episode: next.episode.number}) : undefined;
  const when = countdown ? countdown.automatic ? t('player.upNext.playingIn', {seconds: Math.ceil(countdown.seconds)})
    : t(scene ? 'player.upNext.startsAfterScene' : 'player.upNext.startsIn', {time: watchClock(countdown.seconds)}) : undefined;
  const act = (kind: 'play-now' | 'watch-credits' | 'not-now') => {void dispatchWatchNext(shownBinding, shownSnapshot, kind).catch(shownBinding.reportError);};
  const skip = () => {if (marker) void dispatchWatchMarker(shownBinding, shownSnapshot, {kind: 'skip', markerId: marker.id}).catch(shownBinding.reportError);};
  return <WatchPromptRetirement active={presence.phase === 'in'} paintKey={presence.paintKey} element={motionRef}><PlayerUpNext key={presence.paintKey} ref={motionRef} className={motion.prompt} layout={layout} data-controls-visible={drawnControls} data-watch-up-next data-scene-after={scene} data-up-next-end={!prompt || undefined}
    data-prompt-phase={presence.phase} inert={presence.phase === 'out'} aria-hidden={presence.phase === 'out' || undefined} style={promptSnapStyle}
    heading={t('player.upNext.duringCredits')} still={<Artwork source={source} alt="" />} title={next.title} facts={[episode, duration].filter((fact): fact is string => !!fact)} when={when} progress={progress}
    note={scene && marker ? t('player.upNext.sceneAfterCredits') : undefined}
    actions={[{id: 'play-now', label: t('player.upNext.playNow'), primary: true, glyph: 'play', onPress: () => act('play-now')},
      ...(scene && marker ? [{id: 'skip', label: t('player.skip', {kind: marker.kind}), glyph: 'next' as const, onPress: skip}] : []),
      prompt ? {id: 'watch-credits', label: t('player.upNext.watchCredits'), onPress: () => act('watch-credits')} : {id: 'not-now', label: t('player.upNext.notNow'), onPress: () => act('not-now')}]} /></WatchPromptRetirement>;
}
