import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {Button} from '../controls';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {frameSpec} from '@portico/design';

export type PageHeaderProps = Readonly<{
  title: string;
  /** A full-screen page that was pushed: Back on its own line, named after the page it returns to. Not drawn on TV (the remote's Back). */
  back?: TopBarBack;
  /** Header actions (Filters, sort), beside the title. */
  trailing?: ReactNode;
}>;

/** Rule 3, the one header of a page that is not a title page and not inside a sheet: the safe area as padding, an
 * optional Back line, then the large page title, which wraps and is never cut. Frames draw it; screens only name the title. */
/** `list`: a list page stepped into (See all): on TV its back line is quiet and its title is the large list title (collections 48). */
export function PageHeader({title, back, trailing, showTVBack, list = false}: PageHeaderProps & {showTVBack?: boolean; list?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets();
  const tv = frame.form === 'tv';
  // A TV list page's head is placed from the screen's top as collections 48 draws it (the safe area is inside that 64).
  const top = back && !tv ? 0 : tv && list && back && showTVBack ? frameSpec.browse.listHeadTV.top : Math.max(frame.titleTop, insets.top + frame.headerGap);
  return <View style={{paddingTop: top, paddingBottom: frame.headerGap}}>
    {back && (!tv ? <TopBar back={back}/> : showTVBack && <View style={{paddingHorizontal: frame.gutter, marginBottom: list ? frameSpec.browse.listHeadTV.backGap : frame.headingGap, alignItems: 'flex-start'}}>
      <Button focusId="back" testID={back.testID} icon="i-left" variant="text" size={list ? 'small' : undefined} textStyle={list ? {color: theme.colors.textTertiary} : undefined} iconColor={list ? theme.colors.textTertiary : undefined} onPress={back.onPress}>{back.label}</Button>
    </View>)}
    <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: theme.space.sm, paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
      <Text accessibilityRole="header" textBreakStrategy="balanced" android_hyphenationFrequency="none" style={[tv && list ? theme.type.listTitle : theme.type.pageTitle, {flex: 1, color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
      {trailing}
    </View>
  </View>;
}
