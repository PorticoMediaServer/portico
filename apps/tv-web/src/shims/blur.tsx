import React, {forwardRef} from 'react';
import type {View, ViewProps} from 'react-native';

// The TV glass rule (`platform/glass.ts`) draws a translucent fill on every non-Apple TV, and a
// TV browser's backdrop blur is too costly for these sets. Fail if a caller crosses that line
// rather than pretending to draw a blur (as on Fire TV).
export type BlurViewProps = ViewProps & {blurType?: string; blurAmount?: number; overlayColor?: string; reducedTransparencyFallbackColor?: string};
export const BlurView = forwardRef<View, BlurViewProps>(function BlurView(_props, _ref): React.ReactElement {
  throw new Error('TV browsers do not draw native backdrop blur');
});
