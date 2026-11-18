import React from 'react';
import {Text, View} from 'react-native';
import {CoverFan} from '../artwork';
import type {ArtworkSource} from '../artwork/types';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

/** What still works while the server can't be reached (states 4a): "Your downloads still play" with a fan of their
 * covers and the first title. Moved from the restored shell's recovery page; a page state places it before its buttons.
 * The web kit's part of the same name draws the same row. */
export function DownloadsStillPlay({covers, title, detail, onPress, testID}: {covers: readonly ArtworkSource[]; title: string; detail: string; onPress: () => void; testID?: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={title} focusRadius={14} onPress={onPress}
    style={{minHeight: 74, width: '100%', marginBottom: 14, paddingHorizontal: 14, justifyContent: 'center', borderRadius: 14,
      backgroundColor: `${theme.colors.raised}D9`, borderWidth: 1, borderColor: `${theme.colors.text}14`}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <View style={{width: 64, height: 50}}>{covers.length ? <CoverFan covers={covers} shape="poster" width={64} height={50}/> : null}</View>
      <View style={{flex: 1}}>
        <Text numberOfLines={1} style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}</Text>
        <Text numberOfLines={1} style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{detail}</Text>
      </View>
      <Icon name="i-right" size={20} color={theme.colors.textTertiary}/>
    </View>
  </Focusable>;
}
