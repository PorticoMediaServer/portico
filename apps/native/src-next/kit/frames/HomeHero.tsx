import React, {type ReactNode} from 'react';
import {frameSpec} from '@portico/design';
import {FocusPreviewHero} from '../layout/FocusPreviewHero';
import {Hero, HeroMetadata, HeroProgress, type HeroProps} from '../layout/Hero';
import type {HeroCandidate} from '../layout/types';
import {nativeType, useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

/** A featured title that is part-watched: its progress and the time left, drawn in the facts row (final3-tv-01). An episode's
 * number and name go in `reason`, the kicker over the title. */
export type HomeHeroEntry = HeroCandidate & Readonly<{progress?: number; remaining?: string}>;

/** What a screen may say about Home's hero: what to feature and how to draw its art and buttons. Never a size. */
export type HomeHeroProps<T extends HomeHeroEntry> = Pick<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'active' | 'visible' | 'heroFocused'
  | 'renderArtwork' | 'renderActions' | 'renderMetadata' | 'renderReason' | 'onArtworkError' | 'onArtworkReady' | 'testID'> & Readonly<{
  /** TV (owner, 18 Nov): the hero follows the focused card, so it has no spotlight of its own, no buttons and no pager. */
  focusedEntry?: T;
  /** TV: the key hints under the words, OK Resume and Hold OK More options (final3-tv-01). */
  hints?: (entry: T) => ReactNode;
}>;

/** Home's hero title for a form, at the frames' size (`frameSpec.hero.home.lockup`); the tablet's side layout uses it too. */
export function homeLockupStyle(form: keyof typeof frameSpec.hero.home.lockup, textScale: number) {
  const size = frameSpec.hero.home.lockup[form];
  return nativeType({size, lineHeight: size * frameSpec.hero.lockupLine, weight: 800, tracking: frameSpec.hero.lockupTracking}, textScale);
}

/** Rule 14, the moderate hero, for `BrowseFrame`'s `hero`: the kit's Hero (its cross-fade, drift and reduced-motion
 * version unchanged) at the rule's fixed height and lockup size for this form. On TV it is the kit's focus-led hero. */
export function HomeHero<T extends HomeHeroEntry>({focusedEntry, hints, renderMetadata, ...props}: HomeHeroProps<T>) {
  const theme = useTheme(), {form} = useFrameMetrics(), home = frameSpec.hero.home;
  const metadata = renderMetadata ?? ((entry: T) => <HeroMetadata runtimeChip centered={form === 'phone'} facts={entry.facts ?? []}>
    {entry.progress !== undefined && entry.remaining && <HeroProgress progress={entry.progress} remaining={entry.remaining}/>}</HeroMetadata>);
  const lockup = homeLockupStyle(form, theme.textScale);
  if (form === 'tv') {
    const {candidates: _candidates, activeIndex: _index, onIndexChange: _change, heroFocused: _focused, renderActions: _actions, ...surface} = props;
    return <FocusPreviewHero {...surface} renderMetadata={metadata} focusedEntry={focusedEntry} renderHint={entry => hints?.(entry)} height={home.height.tv} lockupStyle={lockup} longLockupStyle={lockup}
      showOverview={home.synopsisLines.tv > 0} copyTop={home.copyTop.tv}/>;
  }
  return <Hero {...props} renderMetadata={metadata} height={home.height[form]} lockupStyle={lockup} longLockupStyle={lockup} showOverview={home.synopsisLines[form] > 0}
    copyStyle={{bottom: home.copyBottom[form]}}/>;
}
