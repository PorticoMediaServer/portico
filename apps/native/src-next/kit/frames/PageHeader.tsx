import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

export type PageHeaderProps = Readonly<{
  title: string;
  /** A full-screen page that was pushed: Back on its own line, named after the page it returns to. Not drawn on TV (the remote's Back). */
  back?: TopBarBack;
  /** Header actions (Filters, sort), beside the title. */
  trailing?: ReactNode;
}>;

/** Rule 3, the one header of a page that is not a title page and not inside a sheet: the safe area as padding, an
 * optional Back line, then the large page title, which wraps and is never cut. Frames draw it; screens only name the title. */
export function PageHeader({title, back, trailing}: PageHeaderProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets();
  const tv = frame.form === 'tv';
  return <View style={{paddingTop: back && !tv ? 0 : Math.max(frame.titleTop, insets.top + frame.headerGap), paddingBottom: frame.headerGap}}>
    {back && !tv && <TopBar back={back}/>}
    <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: theme.space.sm, paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
      <Text accessibilityRole="header" textBreakStrategy="balanced" android_hyphenationFrequency="none" style={[frame.type.pageTitle, {flex: 1, color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
      {trailing}
    </View>
  </View>;
}
