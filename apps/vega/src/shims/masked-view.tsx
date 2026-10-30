import React, {forwardRef} from 'react';
import {StyleSheet, View} from '@amazon-devices/react-native-kepler';
import register from '@amazon-devices/react-native-kepler/Libraries/Utilities/registerGeneratedViewConfig';
import type MaskedView from '@amazon-devices/react-native-masked-view__masked-view';

// The MaskedView system JS can still require an RN2 entry point in an RN4 app.
// Register its existing native component in the app's current SDK registry, as the
// SDK's MaskedView spec does, without importing that incompatible system JS.
register('RNCMaskedView', {uiViewClassName: 'RNCMaskedView', validAttributes: {}});
const NativeMaskedView = 'RNCMaskedView' as unknown as typeof View;

/** Keep the SDK's mask-first native child contract; the Android mode is not a Vega prop. */
export default forwardRef<React.ComponentRef<typeof View>, React.ComponentProps<typeof MaskedView>>(function VegaMaskedView({androidRenderingMode: _androidMode, maskElement, children, ...props}, ref) {
  if (!React.isValidElement(maskElement)) return <View {...props} ref={ref}>{children}</View>;
  return <NativeMaskedView {...props} ref={ref}>
    <View pointerEvents="none" style={StyleSheet.absoluteFill}>{maskElement}</View>
    {children}
  </NativeMaskedView>;
});
