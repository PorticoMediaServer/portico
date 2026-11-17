import React from 'react';
import {frameSpec} from '@portico/design';
import {Hero, type HeroProps} from '../layout/Hero';
import type {HeroCandidate} from '../layout/types';
import {nativeType, useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

/** What a screen may say about Home's hero: what to feature and how to draw its art and buttons. Never a size. */
export type HomeHeroProps<T extends HeroCandidate> = Pick<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'active' | 'visible' | 'heroFocused'
  | 'renderArtwork' | 'renderActions' | 'renderMetadata' | 'renderReason' | 'onArtworkError' | 'onArtworkReady' | 'testID'>;

/** Rule 14, the moderate hero, for `BrowseFrame`'s `hero`: the kit's Hero (its cross-fade, drift and reduced-motion
 * version unchanged) at the rule's fixed height and lockup size for this form. */
export function HomeHero<T extends HeroCandidate>(props: HomeHeroProps<T>) {
  const theme = useTheme(), {form} = useFrameMetrics(), home = frameSpec.hero.home, size = home.lockup[form];
  const lockup = nativeType({size, lineHeight: size * frameSpec.hero.lockupLine, weight: 800, tracking: frameSpec.hero.lockupTracking}, theme.textScale);
  return <Hero {...props} height={home.height[form]} lockupStyle={lockup} longLockupStyle={lockup} showOverview={home.synopsisLines[form] > 0}
    copyTop={form === 'tv' ? home.copyTop.tv : undefined} copyStyle={form === 'tv' ? undefined : {bottom: home.copyBottom[form]}}/>;
}
