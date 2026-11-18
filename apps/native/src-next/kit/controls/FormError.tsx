import React from 'react';
import {Text, View} from 'react-native';
import {feedbackSpec as feedback} from '@portico/design';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {colorAlpha} from './control-model';

/** The one error every form and field draws: `componentSpec.field.error`'s type, colour and spacing.
 * `field` is the line under what it is about (design/ui-v1 `.help.err`); `inline` is that line inside a container
 * whose own gap spaces it. `block` heads a form, above its fields, as the mock-ups' message box (`.msg`, auth.html
 * frame 6): the warning glyph in the error colour beside the sentence, in the notice spec's error tint. */
export function FormError({children, placement = 'field', testID}: {children: React.ReactNode; placement?: 'field' | 'block' | 'inline'; testID?: string}) {
  const theme = useTheme(), spec = theme.spec.field.error;
  if (placement === 'block') {
    const box = feedback.notice, tone = box.tones.error, colour = theme.colors[tone.color as 'danger'], tv = theme.isTV;
    return <View testID={testID} accessibilityRole="alert" accessibilityLiveRegion="polite"
      style={{flexDirection: 'row', alignItems: 'flex-start', gap: box.gap[theme.column], padding: box.padding[theme.column],
        paddingHorizontal: box.padding[theme.column] + (tv ? 4 : 2), borderRadius: box.radius[theme.column], borderWidth: 1,
        backgroundColor: colorAlpha(colour, tone.tintAlpha), borderColor: colorAlpha(colour, tone.borderAlpha),
        marginBottom: spec.blockGap[theme.column], alignSelf: 'stretch'}}>
      <Icon name="i-warn" size={tv ? 28 : 20} color={colour}/>
      <Text style={[theme.type[box.text], {color: theme.colors.text, flex: 1, minWidth: 0}]}>{children}</Text>
    </View>;
  }
  return <Text testID={testID} accessibilityRole="alert" accessibilityLiveRegion="polite"
    style={[theme.type[spec.role], {color: theme.colors[spec.color]},
      placement === 'field' ? {marginTop: spec.gap[theme.column]} : undefined]}>{children}</Text>;
}
