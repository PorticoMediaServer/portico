import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import type {ChannelMarkProps} from '../artwork/types';
import {ChannelMark} from '../artwork';
import {Focusable} from '../controls';
import {useTheme} from '../theme';

type Tone = 'quiet' | 'healthy' | 'building' | 'warning' | 'danger';

/** One Library Channel: its mark or logo and name, how many titles it holds, a dot and word for how it's doing, how far
 * it is scheduled and what it plays. Pressing it opens its menu. */
export function LibraryChannelCard({name, mark, logo, titles, status, tone, through, summary, disabled, onPress, testID}: {name: string; mark: ChannelMarkProps['mark'];
  logo?: ChannelMarkProps['source']; titles: string; status: string; tone: Tone; through?: string; summary?: string; disabled?: boolean; onPress: () => void; testID: string}) {
  const theme = useTheme();
  const ink = {quiet: theme.colors.textTertiary, healthy: theme.colors.textSecondary, building: theme.colors.accentStrong, warning: theme.colors.amber, danger: theme.colors.record}[tone];
  return <View style={{padding: theme.space.sm - 4, borderRadius: 12, backgroundColor: theme.colors.raised}}>
    <Focusable testID={testID} focusKind="row" accessibilityRole="button" accessibilityLabel={name} disabled={disabled} onPress={onPress} style={{gap: theme.space.xs}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 4}}>
        <ChannelMark mark={mark} source={logo} size={44} sized/>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1}]}>{name}</Text>
      </View>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.space.sm - 4}}>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{titles}</Text>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>
          <View style={{width: 7, height: 7, borderRadius: 3.5, backgroundColor: tone === 'healthy' ? theme.colors.healthy : ink}}/>
          <Text style={[theme.type.captionStrong, {color: ink}]}>{status}</Text>
        </View>
      </View>
      {through !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{through}</Text>}
      {summary !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{summary}</Text>}
    </Focusable>
  </View>;
}

/** A sheet's grouped menu rows on one raised card. */
export function MenuGroup({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>{children}</View>;
}

/** A channel's health: the headline, then its facts in the quiet colour. */
export function HealthFacts({headline, facts}: {headline: string; facts: readonly string[]}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.xs}}>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{headline}</Text>
    {facts.map(fact => <Text key={fact} style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{fact}</Text>)}
  </View>;
}
