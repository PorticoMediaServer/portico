import React, {type ReactNode} from 'react';
import {frameSpec} from '@portico/design';
import {FocusPreviewHero} from '../layout/FocusPreviewHero';
import {Hero, type HeroProps} from '../layout/Hero';
import type {HeroCandidate} from '../layout/types';
import {nativeType, useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

/** What a screen may say about Home's hero: what to feature and how to draw its art and buttons. Never a size. */
export type HomeHeroProps<T extends HeroCandidate> = Pick<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'active' | 'visible' | 'heroFocused'
  | 'renderArtwork' | 'renderActions' | 'renderMetadata' | 'renderReason' | 'onArtworkError' | 'onArtworkReady' | 'testID'> & Readonly<{
  /** TV (owner, 18 Nov): the hero follows the focused card, so it has no spotlight of its own, no buttons and no pager. */
  focusedEntry?: T;
  /** TV: the key hints under the words, OK Resume and Hold OK More options (final3-tv-01). */
  hints?: (entry: T) => ReactNode;
}>;

/** Rule 14, the moderate hero, for `BrowseFrame`'s `hero`: the kit's Hero (its cross-fade, drift and reduced-motion
 * version unchanged) at the rule's fixed height and lockup size for this form. On TV it is the kit's focus-led hero. */
export function HomeHero<T extends HeroCandidate>({focusedEntry, hints, ...props}: HomeHeroProps<T>) {
  const theme = useTheme(), {form} = useFrameMetrics(), home = frameSpec.hero.home, size = home.lockup[form];
  const lockup = nativeType({size, lineHeight: size * frameSpec.hero.lockupLine, weight: 800, tracking: frameSpec.hero.lockupTracking}, theme.textScale);
  if (form === 'tv') {
    const {candidates: _candidates, activeIndex: _index, onIndexChange: _change, heroFocused: _focused, renderActions: _actions, ...surface} = props;
    return <FocusPreviewHero {...surface} focusedEntry={focusedEntry} renderHint={entry => hints?.(entry)} height={home.height.tv} lockupStyle={lockup} longLockupStyle={lockup}
      showOverview={home.synopsisLines.tv > 0} copyTop={home.copyTop.tv}/>;
  }
  return <Hero {...props} height={home.height[form]} lockupStyle={lockup} longLockupStyle={lockup} showOverview={home.synopsisLines[form] > 0}
    copyStyle={{bottom: home.copyBottom[form]}}/>;
}
