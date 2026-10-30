import React from 'react';
import Svg, {type SvgProps} from 'react-native-svg';
import {colors} from '../../../../packages/design/src/index';
import {nextIcons, type IconName} from '../../../../packages/design/src/next-icons';
import {IconGlyph} from './IconGlyphs';

export type {IconName};
export type IconProps = Omit<SvgProps, 'children' | 'viewBox' | 'role' | 'accessible' | 'accessibilityRole' | 'accessibilityElementsHidden' | 'importantForAccessibility'> & {
  name: IconName;
  size?: number;
};

/** Labels come from the caller's i18n catalogue; otherwise the glyph is decorative. */
export function Icon({name, size = 24, width = size, height = size, color = colors.text, accessibilityLabel, ...props}: IconProps) {
  const labelled = Boolean(accessibilityLabel);
  return <Svg {...props} width={width} height={height} color={color} viewBox={nextIcons[name].viewBox}
    accessible={labelled} accessibilityLabel={accessibilityLabel} accessibilityRole={labelled ? 'image' : undefined}
    accessibilityElementsHidden={!labelled} importantForAccessibility={labelled ? 'yes' : 'no-hide-descendants'} focusable={false}>
    <IconGlyph name={name} />
  </Svg>;
}
