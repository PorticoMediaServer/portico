import React, {useId, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Chip} from '../controls';
import {useTheme} from '../theme';
import {alpha, browseType} from './presentation';

const t = defaultI18n.t;
export type RelaxSuggestion = Readonly<{id: string; label: string; count?: number; countIsLowerBound?: boolean; onSelect: () => void}>;

export function RelaxSuggestions({suggestions, label}: {suggestions: readonly RelaxSuggestion[]; label?: ReactNode}) {
  const theme = useTheme();
  if (!suggestions.length) return null;
  return <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'center', flexWrap: 'wrap', gap: 10, marginTop: 30}}>
    {label && <Text style={[browseType(theme, theme.isTV ? 20 : 14, 500), {color: theme.colors.textTertiary, marginRight: 4}]}>{label}</Text>}
    {suggestions.map(suggestion => <Chip key={suggestion.id} variant="quick" count={suggestion.count === undefined ? undefined : `${defaultI18n.number(suggestion.count)}${suggestion.countIsLowerBound ? t('next.browse.native.countPlus') : ''}`} onPress={suggestion.onSelect}>{suggestion.label}</Chip>)}
  </View>;
}

/** Suggestions and the exact constraint sentence are supplied from the bounded browse model. */
export function FilteredEmpty({description, onClearFilters, suggestions = [], suggestionsLabel, testID}: {description?: ReactNode; onClearFilters: () => void; suggestions?: readonly RelaxSuggestion[]; suggestionsLabel?: ReactNode; testID?: string}) {
  const theme = useTheme(), id = useId().replace(/:/g, '');
  return <View testID={testID} style={{alignItems: 'center', paddingTop: 70, paddingHorizontal: theme.gutter}}>
    <View accessible={false} importantForAccessibility="no-hide-descendants" style={{width: 220, height: 150, marginBottom: 30}}>
      {[-1, 1, 0].map(position => {
        const centre = position === 0, width = centre ? 92 : 84, height = centre ? 138 : 126, gradient = `${id}-${position}`;
        return <View key={position} style={{position: 'absolute', bottom: 0, left: 110 - width / 2, width, height, opacity: centre ? 1 : .55, transform: [{translateX: position * 62}, {rotate: `${position * 9}deg`}], borderRadius: 10, overflow: 'hidden'}}>
          <Svg width={width} height={height}><Defs><LinearGradient id={gradient} x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor={centre ? theme.colors.raisedBright : theme.colors.raisedHigh}/><Stop offset="1" stopColor={centre ? theme.colors.raisedHigh : theme.colors.raised}/></LinearGradient></Defs><Rect width={width} height={height} rx={10} fill={`url(#${gradient})`} stroke={alpha(theme.colors.text, .08)}/></Svg>
        </View>;
      })}
    </View>
    <Text accessibilityRole="header" style={[browseType(theme, theme.isTV ? 44 : theme.platform === 'phone' ? 26 : 34, 800, -.03), {color: theme.colors.text, textAlign: 'center', marginBottom: 10}]}>{t('next.browse.native.filterEmptyTitle')}</Text>
    <Text style={[browseType(theme, theme.isTV ? 24 : 16.5, 400, 0, theme.isTV ? 36 : 25.575), {color: theme.colors.textSecondary, textAlign: 'center', maxWidth: theme.isTV ? 680 : 460, marginBottom: 26}]}>{description ?? t('next.browse.native.filterEmptyBody')}</Text>
    <Button variant="primary" size="hero" onPress={onClearFilters} style={{height: theme.isTV ? 72 : 50}}>{t('next.browse.native.clearFilters')}</Button>
    <RelaxSuggestions suggestions={suggestions} label={suggestionsLabel}/>
  </View>;
}
