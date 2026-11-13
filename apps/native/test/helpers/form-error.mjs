// The kit's FormError (`kit/controls/FormError.tsx`) for screen fixtures: the same alert Text it draws.
import * as React from 'react';
export function FormError({children, testID}) {return React.createElement('Text', {accessibilityRole: 'alert', accessibilityLiveRegion: 'polite', testID}, children);}
export const formErrorModule = {FormError};
