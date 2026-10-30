import React from 'react';
import {Image, type ImageProps} from 'react-native';

/** The one element artwork draws a picture with: React Native's Image. It is the seam for any future
 * change of image engine (the expo-image spike, P2-70 LIB-1, was measured and not adopted). */
export type PictureProps = ImageProps & {resizeMultiplier?: number};

// RN 0.83's ImageProps.js and Android native Image both support resizeMultiplier; its bundled
// Image.d.ts omits it. This aliases the same Image host.
const NativeImage = Image as React.ComponentType<PictureProps>;

export function Picture(props: PictureProps) {
  return <NativeImage {...props}/>;
}
