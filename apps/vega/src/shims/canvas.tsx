import * as Vega from '@amazon-devices/react-native-kepler';
import React from 'react';

/**
 * The television design canvas.
 *
 * The Apple TV app lays out on 1920×1080 points, and every television measurement in the shared
 * screens assumes that. Vega lays a 1080p screen out as 960×540 at a pixel ratio of 2, so the same
 * numbers would draw everything twice as large. The app is therefore laid out on a canvas 1080
 * points high and scaled to the window: one canvas point is one pixel of a 1080p screen, as on
 * the Apple TV. The `react-native` shim reports the canvas wherever the shared code asks for the
 * window's size or pixel ratio.
 */
const DESIGN_HEIGHT = 1080;

export type CanvasMetrics = {width: number; height: number; scale: number; fontScale: number; factor: number};

/** The canvas for a window: its size in canvas points, its pixel ratio, and the window-per-canvas factor. */
export function canvasFor(window: {width: number; height: number; scale: number; fontScale: number}): CanvasMetrics {
  const factor = window.height > 0 ? window.height / DESIGN_HEIGHT : 1;
  return {width: Math.round(window.width / factor), height: DESIGN_HEIGHT, scale: window.scale * factor, fontScale: window.fontScale, factor};
}

export function currentCanvas(): CanvasMetrics {
  return canvasFor(Vega.Dimensions.get('window'));
}

/** Lays its children out on the design canvas and scales the result to fill the window. */
export function Canvas({children}: {children?: React.ReactNode}): React.ReactElement {
  const {width, height, factor} = canvasFor(Vega.useWindowDimensions());
  if (factor === 1) return <>{children}</>;
  // A scale is about the view's center; the translation puts the scaled canvas back at the origin.
  const transform = [{translateX: (-width * (1 - factor)) / 2}, {translateY: (-height * (1 - factor)) / 2}, {scale: factor}];
  return (
    <Vega.View style={{flex: 1}}>
      <Vega.View style={{position: 'absolute', left: 0, top: 0, width, height, transform}}>{children}</Vega.View>
    </Vega.View>
  );
}
