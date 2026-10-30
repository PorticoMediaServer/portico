import React from 'react';
import {Text, type TextStyle, type StyleProp} from 'react-native';
import {BottomSheet} from '../overlays/BottomSheet';
import {SurfaceHeader} from '../overlays/surface';
import {fontFamilies, useTheme, type Theme} from '../theme';
import type {BottomSheetProps, SurfaceProps} from '../overlays/types';

/** select.html's m-btns, es-foot and dsh actions override the common phone button. */
export function selectionFooterProps({platform, textScale}: Pick<Theme, 'platform' | 'textScale'>) {
  return platform === 'phone' ? {
    style: {height: 48, paddingHorizontal: 20, borderRadius: 10, gap: 8},
    textStyle: {fontSize: 15.5 * textScale},
  } : {style: undefined, textStyle: undefined};
}

export function SelectionText({children, size, weight = 500, secondary = false, numberOfLines, style}: {children: React.ReactNode; size?: number; weight?: 400 | 500 | 600 | 700 | 800; secondary?: boolean; numberOfLines?: number; style?: StyleProp<TextStyle>}) {
  const theme = useTheme();
  return <Text numberOfLines={numberOfLines} ellipsizeMode="tail" style={[{fontFamily: fontFamilies[weight], fontSize: size === undefined ? theme.isTV ? theme.type.body.fontSize : 14 * theme.textScale : size * theme.textScale, color: secondary ? theme.colors.textTertiary : theme.colors.text}, style]}>{children}</Text>;
}
export function SelectionSurface({fullHeight = false, compactHeader = false, scrollable = true, headerContext, headerContent, headerAction, footerPadding, ...props}: SurfaceProps & Pick<BottomSheetProps, 'footerPadding' | 'showFooterSeparator' | 'contentFooterGap' | 'headerContent'> & {fullHeight?: boolean; compactHeader?: boolean; scrollable?: boolean; headerContext?: React.ReactNode; headerAction?: React.ReactNode}) {
  const theme = useTheme();
  const {isTV} = theme;
  const titleType: NonNullable<SurfaceProps['titleType']> = props.titleType ?? {size: isTV ? 46 : fullHeight ? 24 : 20, weight: 800, tracking: fullHeight ? -.03 : -.02, lineHeight: isTV ? 'normal' : fullHeight ? 28.8 : 24};
  // Only the drawn compact phone .sh-head opts in; editor/delete headers keep their own recipe.
  const compact = compactHeader && theme.platform === 'phone' && !fullHeight ? <SurfaceHeader title={props.title} description={props.description} action={headerAction} titleType={titleType}
    style={{paddingTop: 4, paddingBottom: 10}} titleTextStyle={[{includeFontPadding: false}, props.titleTextStyle]}
    descriptionTextStyle={[{fontSize: 13.5 * theme.textScale, lineHeight: 19.575 * theme.textScale, marginTop: 4, color: theme.colors.textTertiary, includeFontPadding: false}, props.descriptionTextStyle]} /> : undefined;
  return <BottomSheet {...props} keyboardAvoiding width={isTV ? 680 : undefined} footer={props.footer} footerPadding={footerPadding ?? (isTV ? undefined : {top: 10, bottom: 30, keyboardBottom: 12})} showClose={false} headerAction={headerAction ?? <></>} scrollable={scrollable} headerContext={headerContext} headerContent={headerContent !== undefined ? headerContent : compact} titleType={titleType} descriptionTextStyle={props.descriptionTextStyle ?? {fontSize: (isTV ? 22 : fullHeight ? 13 : 13.5) * theme.textScale, lineHeight: (isTV ? 33 : fullHeight ? 18.85 : 19.575) * theme.textScale}} detents={fullHeight ? [800] : undefined} fitContent={!fullHeight} />;
}
