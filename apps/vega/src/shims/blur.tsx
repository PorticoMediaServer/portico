import React, {forwardRef} from 'react';
import type {View, ViewProps} from '@amazon-devices/react-native-kepler';

// Q43's existing glassRecipe selects the drawn translucent fill on Vega TV.
// No native backdrop-blur module exists here. Fail if a caller crosses that
// platform boundary rather than pretending to render a blur.
export type BlurViewProps = ViewProps & {blurType?: string; blurAmount?: number; overlayColor?: string; reducedTransparencyFallbackColor?: string};
export const BlurView = forwardRef<View, BlurViewProps>(function BlurView(_props, _ref): React.ReactElement {
  throw new Error('Vega TV does not provide native backdrop blur');
});
