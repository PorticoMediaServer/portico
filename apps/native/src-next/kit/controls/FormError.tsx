import React from 'react';
import {Text} from 'react-native';
import {useTheme} from '../theme';

/** The one error line every form and field draws (design/ui-v1 `.help.err`): `componentSpec.field.error`'s
 * type, colour and spacing. `field` sits under what it is about; `block` heads a form, above its fields;
 * `inline` is inside a container whose own gap spaces it (a field, a stack). */
export function FormError({children, placement = 'field', testID}: {children: React.ReactNode; placement?: 'field' | 'block' | 'inline'; testID?: string}) {
  const theme = useTheme(), spec = theme.spec.field.error;
  return <Text testID={testID} accessibilityRole="alert" accessibilityLiveRegion="polite"
    style={[theme.type[spec.role], {color: theme.colors[spec.color]},
      placement === 'field' ? {marginTop: spec.gap[theme.platform]} : placement === 'block' ? {marginBottom: spec.blockGap[theme.platform]} : undefined]}>{children}</Text>;
}
