import React from 'react';
import {StatusBar, View, type ViewProps} from 'react-native';
import {useTheme} from '../theme';
import {AmbientArt, StandardBackdrop} from '../artwork/Surfaces';
import type {AmbientArtSource} from '../artwork/types';

type Kind = 'page' | 'audio' | 'player';

/** The one place the app's backdrop is drawn (owner, 17 Nov: ONE backdrop everywhere).
 *
 * A layer is anything that must hide what is beneath it: the app's root (boot, sign-in, the TV's own pages), a stack
 * screen, the TV route layer, a full-window page over the shell. Whoever mounts a layer puts it on `Layer`, which
 * covers what is beneath and draws the backdrop once. Screens, frames and pages never draw a backdrop or a page colour:
 * they are transparent on their layer. If a screen looks wrong without one, the fix is its host or its frame.
 *
 * The exceptions are these two and they live here (owner, 17 and 18 Nov):
 * - `audio`: the audio player. Its backdrop is a glow worked out from the playing item's artwork: the server's own
 *   small saturated wash of that artwork (`art`), drawn over the whole layer. With no artwork it is the one backdrop.
 * - `player`: the video player. Plain black, and the only layer that hides the system status bar.
 * A sheet, menu or dialog is not a layer: it is a plain kit surface.
 *
 * The callers are listed in test/next-one-backdrop.test.ts, which fails when the backdrop is drawn, or `Layer` used,
 * anywhere else. No motion, no variants, no other props. */
export function Layer({kind = 'page', art, style, children, ...view}: ViewProps & {kind?: Kind; /** `audio` only. */ art?: AmbientArtSource | null}) {
  const {colors} = useTheme();
  return <View {...view} style={[{flex: 1, backgroundColor: kind === 'player' ? '#000' : colors.ink}, style]}>
    <LayerStatusBar kind={kind}/>
    {kind === 'player' ? null : kind === 'audio' && art
      ? <View pointerEvents="none" style={{position: 'absolute', top: 0, right: 0, bottom: 0, left: 0, overflow: 'hidden'}}><AmbientArt source={art} treatment="music" style={{position: 'absolute', top: 0, right: 0, bottom: 0, left: 0}}/></View>
      : <StandardBackdrop/>}
    {children}
  </View>;
}

/** The system status bar is the layer's business: light content on every layer (they are all dark), hidden only on the
 * full-window video player, as the mock-ups draw it. The video picture is the player's own native surface, so its host
 * mounts this alone while it fills the window. */
export function LayerStatusBar({kind}: {kind: Kind}) {
  return kind === 'player' ? <StatusBar hidden animated/> : <StatusBar barStyle="light-content"/>;
}
