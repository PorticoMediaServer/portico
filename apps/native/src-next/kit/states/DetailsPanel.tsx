import React from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import Svg, {Defs, RadialGradient, Rect, Stop} from 'react-native-svg';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {nativeType, useTheme} from '../theme';
import {ownerDetails, type DetailsAudience} from './model';
import type {StateAction} from './types';
import {StateActionButton} from './StateActionButton';

export type StateFact = Readonly<{
  id: string;
  label: React.ReactNode;
  value?: React.ReactNode;
  status?: Readonly<{label: React.ReactNode; tone?: 'neutral' | 'warning' | 'success'}>;
}>;
export type OwnerStateDetails = Readonly<{
  heading: React.ReactNode;
  ownerTag: React.ReactNode;
  ownerMark: React.ReactNode;
  description?: React.ReactNode;
  codesLabel?: React.ReactNode;
  codes?: readonly string[];
  time?: React.ReactNode;
  actions?: readonly StateAction[];
}>;
export type DetailsPanelProps = Readonly<{
  facts: readonly StateFact[];
  compact?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}> & DetailsAudience<OwnerStateDetails>;

/** Content only. A caller can put the same facts in a disclosure, side panel or sheet. */
export function DetailsPanel(props: DetailsPanelProps) {
  const {facts, compact = false, style, testID} = props;
  const theme = useTheme();
  const tv = theme.isTV;
  const owner = ownerDetails(props);
  const glass = glassRecipe();
  const size = tv ? 22 : compact ? 13 : 14;
  const labelStyle = nativeType({size, lineHeight: size * 1.5, weight: 600, tracking: 0}, theme.textScale);
  const ownerSize = tv ? 21 : compact ? 12.5 : 13.5;
  const ownerStyle = nativeType({size: ownerSize, lineHeight: ownerSize * 1.55, weight: 400, tracking: 0}, theme.textScale);
  return <View testID={testID} style={[{alignSelf: 'stretch', borderRadius: theme.baseRadii.overlay, overflow: 'hidden', backgroundColor: compact ? `${theme.colors.ink}80` : `${theme.colors.raised}D1`, borderWidth: 1, borderColor: `${theme.colors.text}14`}, style]}>
    {!compact && glass.nativeBlur && <BlurView pointerEvents="none" blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.raised} style={{position: 'absolute', inset: 0}} />}
    {facts.map((fact, index) => <View key={fact.id} style={{flexDirection: 'row', alignItems: 'baseline', gap: tv ? 24 : compact ? 12 : 16, paddingHorizontal: tv ? 26 : compact ? 12 : 18, paddingVertical: tv ? 16 : compact ? 8 : 11, borderTopWidth: index ? 1 : 0, borderColor: `${theme.colors.text}0F`}}>
      <Text style={[labelStyle, {color: theme.colors.textTertiary, width: tv ? 190 : compact ? 112 : 150}]}>{fact.label}</Text>
      {fact.value != null && <Text selectable style={[labelStyle, {color: theme.colors.text, flex: 1, flexShrink: 1}]}>{fact.value}</Text>}
      {fact.status && <Text style={[nativeType({size: tv ? 21 : 13, lineHeight: 'normal', weight: 700, tracking: 0}, theme.textScale), {color: fact.status.tone === 'warning' ? theme.colors.amber : fact.status.tone === 'success' ? theme.colors.healthy : theme.colors.textTertiary}]}>{fact.status.label}</Text>}
    </View>)}
    {owner && <View testID={testID ? `${testID}-owner` : undefined} style={{paddingHorizontal: tv ? 26 : compact ? 12 : 18, paddingTop: tv ? 22 : compact ? 10 : 14, paddingBottom: tv ? 24 : compact ? 12 : 16, backgroundColor: `${theme.colors.ink}B3`, borderTopWidth: 1, borderColor: `${theme.colors.accentStrong}29`}}>
      <Svg pointerEvents="none" accessible={false} width="100%" height="100%" viewBox="0 0 1000 1000" preserveAspectRatio="none" style={{position: 'absolute', inset: 0}}>
        <Defs><RadialGradient id="state-owner-glow" gradientUnits="userSpaceOnUse" cx={1000} cy={0} rx={1200} ry={1400}><Stop offset={0} stopColor={theme.colors.accentDeep} stopOpacity={.2} /><Stop offset={.62} stopColor={theme.colors.accentDeep} stopOpacity={0} /></RadialGradient></Defs>
        <Rect width={1000} height={1000} fill="url(#state-owner-glow)" />
      </Svg>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 14 : 10, marginBottom: tv ? 14 : compact ? 6 : 8}}>
        {owner.ownerMark}
        <Text style={[nativeType({size: tv ? 22 : compact ? 13 : 14, lineHeight: 'normal', weight: 800, tracking: 0}, theme.textScale), {color: theme.colors.text, flexShrink: 1}]}>{owner.heading}</Text>
        <View style={{marginLeft: 'auto'}}>{owner.ownerTag}</View>
      </View>
      {owner.description != null && <Text style={[ownerStyle, {color: theme.colors.textSecondary}]}>{owner.description}</Text>}
      {Boolean(owner.codes?.length) && <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: tv ? 12 : 6, marginTop: tv ? 10 : 6}}>
        {owner.codesLabel != null && <Text style={[ownerStyle, {color: theme.colors.textSecondary}]}>{owner.codesLabel}</Text>}
        {owner.codes?.map(code => <Text key={code} selectable style={[nativeType({size: ownerSize * .92, lineHeight: 'normal', weight: 700, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums'], borderRadius: theme.baseRadii.tag, backgroundColor: `${theme.colors.text}14`, paddingHorizontal: tv ? 10 : 7, paddingVertical: 1}]}>{code}</Text>)}
        {owner.time != null && <Text style={[ownerStyle, {color: theme.colors.textSecondary}]}>{owner.time}</Text>}
      </View>}
      {owner.actions && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: tv ? 26 : 18, marginTop: tv ? 16 : 10}}>
        {owner.actions.map(action => <StateActionButton testID={testID ? `${testID}-owner` : undefined} key={action.id} action={action} variant="text" size="xs" iconSize={tv ? 22 : 14}
          style={{height: tv ? 30 : 19, paddingHorizontal: 0}}
          textStyle={{fontSize: (tv ? 21 : compact ? 12.5 : 13.5) * theme.textScale}}
          hitSlop={tv ? {top: 17, bottom: 17, left: 8, right: 8} : {top: 13, bottom: 13, left: 8, right: 8}} />)}
      </View>}
    </View>}
  </View>;
}
