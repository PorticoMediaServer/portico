import React, {useState, type ReactNode} from 'react';
import {View, type ViewStyle} from 'react-native';
import {Artwork} from './Artwork';
import {AmbientArt} from './Surfaces';
import type {AmbientArtSource, ArtworkSource} from './types';
import {useTheme} from '../theme';

/** One cover: its picture, and what is drawn when there is none or while it loads or if it fails (the generated cover). */
export type WholeCoverSource = Readonly<{key: string; source?: ArtworkSource | null; fallback: ReactNode}>;
/** The cover's corners and its shadow, as the page's shape names them. */
export type WholeCoverLook = Readonly<{radius: number; shadow: Readonly<Pick<ViewStyle, 'shadowOpacity' | 'shadowRadius' | 'shadowOffset' | 'elevation'>>}>;
export type WholeCoverProps = Readonly<{cover: WholeCoverSource; width: number; height: number; look: WholeCoverLook; priority?: boolean; elevation?: number;
  /** A glow from the cover's own art around it (the audiobook covers). */
  glow?: AmbientArtSource | null}>;

/** A cover never cropped (owner's rule), one piece for the cover heroes and the row and card thumbnails. */
/** The slot is a bound: once the picture's own size is known the cover takes its own shape inside it,
 *  centred, so nothing is cut and no bars are drawn; until then, and for the fallback, it fills the slot.
 *  The shadow sits on an outer view so iOS does not clip it with the rounded corners. */
export function WholeCover({cover, width, height, look, priority = false, elevation, glow}: WholeCoverProps) {
  const theme = useTheme(), source = cover.source ?? undefined;
  const [found, setFound] = useState<Readonly<{key: string; aspect: number}>>();
  const aspect = source && found?.key === source.cacheKey ? found.aspect : undefined;
  const box = aspect === undefined ? {width, height} : aspect < width / height ? {width: Math.round(height * aspect), height} : {width, height: Math.round(width / aspect)};
  // The glow follows the drawn cover's own shape, so a narrower cover never sits in a square panel.
  const glowBox = {width: Math.round(box.width * 1.14), height: Math.round(box.height * 1.14)};
  return <View style={{width, height, alignItems: 'center', justifyContent: 'center'}}>
    {glow ? <View pointerEvents="none" style={{position: 'absolute', left: (width - glowBox.width) / 2, top: (height - glowBox.height) / 2, ...glowBox, borderRadius: look.radius + 4, overflow: 'hidden'}}>
      <AmbientArt source={glow} treatment="cover"/>
    </View> : null}
    <View style={{...box, borderRadius: look.radius, shadowColor: theme.colors.shadow, ...look.shadow, ...(elevation === undefined ? {} : {elevation})}}>
      <View style={{...box, borderRadius: look.radius, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
        {/* The generated cover stays under the picture while it loads, and shows if it is refused or broken. */}
        {source ? <Artwork source={source} width={box.width} height={box.height} priority={priority} fallback={cover.fallback}
          onSize={natural => setFound({key: source.cacheKey, aspect: natural.width / natural.height})}/> : cover.fallback}
      </View>
    </View>
  </View>;
}
