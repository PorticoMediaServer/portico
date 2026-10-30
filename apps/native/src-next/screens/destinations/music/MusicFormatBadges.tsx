import React from 'react';
import {Text, View} from 'react-native';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {albumBadgeText} from '../../../../../../packages/client-core/src/music-badges';
import {formatSignalText} from '../../../../../../packages/client-core/src/music-signal';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {alpha, browseType} from '../../../kit/browse/presentation';
import {useTheme} from '../../../kit/theme';

/** music-plus album metadata: outlined notable formats, never on album cards
 * or the album's own tracks. The admitted entity already contains the aggregate. */
export function MusicFormatBadges({badges, size}: {badges: ContentEntry['badges']; size: 'phone' | 'tablet' | 'tv'}) {
 const theme = useTheme(), labels = albumBadgeText(badges).map(badge => typeof badge === 'string' ? badge : formatSignalText(badge, defaultI18n.t));
 if (!labels.length) return null;
 const fontSize = size === 'tv' ? 20 : size === 'tablet' ? 13 : 11;
 return <View style={{flexShrink: 0, flexDirection: 'row', justifyContent: size === 'phone' ? 'center' : 'flex-start', gap: size === 'tv' ? 10 : 6, marginTop: size === 'phone' ? 10 : 0}}>
  {labels.map(label => <View key={label} style={{borderWidth: 1, borderColor: alpha(theme.colors.text, .3), borderRadius: 4, paddingHorizontal: size === 'tv' ? 10 : 6, paddingVertical: 2}}>
   <Text style={[browseType(theme, fontSize, 600), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{label}</Text>
  </View>)}
 </View>;
}
