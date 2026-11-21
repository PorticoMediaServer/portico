import React from 'react';
import {Text, View} from 'react-native';
import {Artwork, type ArtworkSource} from '../../artwork';
import {Progress} from '../../cards/primitives';
import {Focusable} from '../../controls';
import {PanelBlock} from '../../frames';
import {Icon, type IconName} from '../../Icon';
import type {ServerFact} from '../../shell';
import {useTheme} from '../../theme';

export function TVDashboardFacts({facts}: {facts: readonly ServerFact[]}) {
  const theme = useTheme();
  if (!facts.length) return null;
  return <PanelBlock><View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.controlGap}}>
    {facts.map(fact => <View key={fact.id} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
      {fact.tone && fact.tone !== 'muted' && <View style={{width: theme.space.xs, height: theme.space.xs,
        borderRadius: theme.space.xs, backgroundColor: fact.tone === 'warning' ? theme.colors.amber : theme.colors.healthy}}/>}
      <Text style={[fact.tone === 'normal' ? theme.type.rowLabel : theme.type.rowHelp,
        {color: fact.tone === 'normal' ? theme.colors.text : theme.colors.textSecondary}]}>{fact.text}</Text>
    </View>)}
  </View></PanelBlock>;
}

export function TVDashboardRow({id, testID, title, label, secondary, facts, image, progress, icon, first, onFocus, onBlur}: {
  id: string; testID: string; title: string; label?: string; secondary?: readonly string[]; facts?: readonly string[];
  image?: ArtworkSource; progress?: number; icon?: IconName; first?: boolean; onFocus?: () => void; onBlur?: () => void;
}) {
  const theme = useTheme();
  return <Focusable focusId={id} testID={testID} focusKind="row" accessibilityRole="text" accessibilityLabel={label ?? title}
    onFocus={onFocus} onBlur={onBlur}
    style={{flexDirection: 'row', gap: theme.space.controlGap, alignItems: 'center', padding: theme.space.md,
      borderTopColor: theme.colors.lineSoft, borderTopWidth: first ? 0 : theme.space.hairline}}>
    {image && <Artwork source={image} width={130} height={76} style={{borderRadius: theme.radii.artwork}}/>}
    {icon && <Icon name={icon} size={theme.type.rowLabel.fontSize} color={theme.colors.textSecondary}/>}
    <View style={{flex: 1, minWidth: 0, gap: theme.space.xs}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{title}</Text>
      {secondary?.length ? <TVDashboardDetail parts={secondary}/> : null}
      {facts?.length ? <TVDashboardDetail parts={facts}/> : null}
    </View>
    {progress !== undefined && <Progress value={progress} style={{width: '15%'}}/>}
  </Focusable>;
}

function TVDashboardDetail({parts}: {parts: readonly string[]}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.controlGap}}>
    {parts.map((word, index) => <Text key={index} style={[theme.type.rowHelp, {flexShrink: 1, color: theme.colors.textSecondary}]}>{word}</Text>)}
  </View>;
}
