import React from 'react';
import {Text, View, type TextStyle} from 'react-native';
import {defaultI18n, type I18n} from '../../../../../packages/i18n/src';
import type {CardTextModel, FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {useTheme, type Theme} from '../theme';

/** A hex colour with the drawn alpha; `rgba(237,245,250,.28)` is the same paint. */
function withAlpha(color: string, value: number): string {
  const channel = Math.round(Math.min(1, Math.max(0, value)) * 255).toString(16).padStart(2, '0').toUpperCase();
  return `${color}${channel}`;
}

/** kit.css `.tag`: 3×9 padding, 1px rgba(237,245,250,.28), radius 6 (TV draws 3×12, 1.5px, radius 8). */
export function homeFactTag(theme: Theme) {
  const phone = theme.layoutMode === 'phone' && theme.platform === 'phone';
  return {
    paddingHorizontal: phone ? 9 : theme.isTV ? 12 : 8,
    paddingVertical: 3,
    borderWidth: theme.isTV ? 1.5 : 1,
    borderRadius: phone ? theme.baseRadii.tag : theme.isTV ? 8 : 6,
    borderColor: phone ? withAlpha(theme.colors.text, 0.28) : `${theme.colors.text}4D`,
  } as const;
}

/** kit.css `.meta strong` draws the 700 face; native supplies one file per weight. */
export function homeFactStrong(theme: Theme): string {
  return (theme.layoutMode === 'phone' && theme.platform === 'phone' ? theme.type.rowHeading : theme.type.bodyStrong).fontFamily ?? '';
}

/** tv.html 1 `.why`: the hero's reason line ("S1 E1  When You're Lost in the Darkness") is the accent eyebrow over the title,
 *  not a card caption; kit.css `.gap` between its parts (12, 16 on TV), 14 under it (16 on TV). It wraps in full. */
export function HomeHeroReason({parts}: {parts: readonly FactPart[]}) {
  const theme = useTheme();
  if (!parts.length) return null;
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', columnGap: theme.isTV ? 16 : 12, marginBottom: theme.isTV ? 16 : 14}}>
    {parts.map(part => <Text key={part.id} style={[theme.type.eyebrow, {includeFontPadding: false, color: theme.colors.accentStrong}]}>{part.text}</Text>)}
  </View>;
}

/** The display context stays canonical; continuation timing is a separate genuine projection. */
/** The hero's facts at a size of its own (tablet.html 2, desktop: 14 or 15 over 1.4), from the form that draws it. */
export type HomeHeroFactsSize = Readonly<{text: TextStyle; columnGap: number; rowGap: number}>;

export function HomeHeroFacts({facts, continuation, centered, size}: {facts: readonly FactPart[]; continuation?: CardTextModel; centered?: boolean; size?: HomeHeroFactsSize}) {
  const theme = useTheme();
  const remaining = continuation?.detail.find(part => part.id === 'remaining');
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: (centered ?? theme.layoutMode === 'phone') ? 'center' : 'flex-start', columnGap: size ? size.columnGap : theme.spec.hero.facts.gap[theme.column], rowGap: size ? size.rowGap : theme.spec.hero.facts.rowGap[theme.column]}}>
    {facts.map(fact => {
      const badge = fact.id === 'duration' || fact.id === 'season-count';
      const text = <Text style={[badge ? theme.type.tag : theme.type.meta, {...size?.text, includeFontPadding: false, color: badge || fact.id === 'year' ? theme.colors.text : theme.colors.textSecondary, ...(!badge && fact.id === 'year' ? {fontFamily: homeFactStrong(theme)} : {})}]}>{fact.text}</Text>;
      return badge ? <View key={fact.id} style={homeFactTag(theme)}>{text}</View> : <React.Fragment key={fact.id}>{text}</React.Fragment>;
    })}
    {theme.isTV && continuation?.progress !== undefined && remaining && <View style={{flexDirection: 'row', alignItems: 'center', gap: 14}}>
      <View accessible={false} style={{width: 160, height: 6, borderRadius: 6, overflow: 'hidden', backgroundColor: `${theme.colors.text}38`}}><View style={{width: `${continuation.progress * 100}%`, height: '100%', backgroundColor: theme.colors.accentStrong}}/></View>
      <Text style={[theme.type.meta, {includeFontPadding: false, color: theme.colors.text, fontFamily: theme.type.bodyStrong.fontFamily}]}>{remaining.text}</Text>
    </View>}
  </View>;
}

export function HomeRemoteHint({resume, text = defaultI18n}: {resume: boolean; text?: Pick<I18n, 't'>}) {
  const theme = useTheme(), t = text.t;
  const key = (label: string) => <View style={{height: 36, minWidth: 44, paddingHorizontal: 10, borderRadius: 9, borderWidth: 1.5, borderColor: `${theme.colors.text}4D`, alignItems: 'center', justifyContent: 'center'}}><Text style={[theme.type.badge, {includeFontPadding: false, color: theme.colors.textSecondary}]}>{label}</Text></View>;
  return <View style={{flexDirection: 'row', alignItems: 'center', columnGap: 68}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>{key(t('next.layout.ok'))}<Text style={[theme.type.captionStrong, {includeFontPadding: false, color: theme.colors.textTertiary}]}>{t(resume ? 'next.home.resume' : 'next.home.play')}</Text></View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>{key(t('next.layout.holdOk'))}<Text style={[theme.type.captionStrong, {includeFontPadding: false, color: theme.colors.textTertiary}]}>{t('next.layout.moreOptions')}</Text></View>
  </View>;
}
