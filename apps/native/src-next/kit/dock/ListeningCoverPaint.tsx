import React from 'react';
import {StyleSheet, View} from 'react-native';
import Animated, {useAnimatedStyle, type SharedValue} from 'react-native-reanimated';
import {Artwork} from '../artwork';
import type {ArtworkSource} from '../artwork';
import {listeningCoverDrawable, listeningCoverTransform} from './listening-cover-model';
import type {DockRect} from './types';

export type ListeningCoverPaintProps = Readonly<{
  /** The actual offered artwork, with its own cache/auth headers untouched. */
  source: ArtworkSource;
  /** The measured full frame this cover is drawn from. */
  full: DockRect;
  /** The measured mini frame the same cover collapses into. */
  mini: DockRect;
  /** The owner's own shared progress: 0 at the full frame, 1 at the mini one. */
  progress: SharedValue<number>;
  visible?: boolean;
  onReady?: (source: ArtworkSource) => void;
  onError?: (source: ArtworkSource) => void;
  testID?: string;
}>;

/** One persistent cover image moved by transform only. It duplicates no control,
 * reads no player/model/focus/navigation state, holds no timer and owns no
 * controller: the owner supplies both measured frames and the shared progress that
 * its own motion policy already drives. Without real measured frames it draws
 * nothing at all rather than fabricating a source or target rectangle. */
export function ListeningCoverPaint({source, full, mini, progress, visible = true, onReady, onError, testID}: ListeningCoverPaintProps) {
  const placement = useAnimatedStyle(() => {
    const transform = listeningCoverTransform(full, mini, progress.value);
    return {
      opacity: transform ? 1 : 0,
      transform: transform
        ? [{translateX: transform.translateX}, {translateY: transform.translateY}, {scaleX: transform.scaleX}, {scaleY: transform.scaleY}]
        : [],
    };
  });
  if (!listeningCoverDrawable(full, mini)) return null;
  return <Animated.View testID={testID} pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
    style={[styles.box, {left: full.x, top: full.y, width: full.width, height: full.height}, placement]}>
    <View accessible={false} importantForAccessibility="no-hide-descendants" style={StyleSheet.absoluteFill}>
      <Artwork source={source} width={full.width} height={full.height} visible={visible} arrivalDurationMs={0} onReady={onReady} onError={onError}/>
    </View>
  </Animated.View>;
}

const styles = StyleSheet.create({box: {position: 'absolute', overflow: 'hidden', borderRadius: 14}});
