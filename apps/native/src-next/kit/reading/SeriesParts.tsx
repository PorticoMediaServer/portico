import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ReadingItem} from '../../../../../packages/client-core/src';
import type {NativeShellRuntime} from '../../shell/runtime';
import {missingArt} from '../artwork';
import {Button} from '../controls';
import {Icon} from '../Icon';
import {CoverHero} from '../layout/CoverHero';
import {useTheme} from '../theme';

const t = defaultI18n.t;

export function SeriesHero({title, author, facts, volumes, direction, progression, readable, readLabel, onRead, runtime, onHeight}: {
  title: string; author?: string; facts: readonly string[]; volumes: readonly ReadingItem[]; direction?: 'ltr' | 'rtl';
  progression?: number; readable: boolean; readLabel: string; onRead: () => void; runtime: NativeShellRuntime; onHeight: (height: number) => void;
}) {
  const theme = useTheme(), column = theme.platform === 'phone' || theme.platform === 'tablet';
  const covers = volumes.length ? volumes.map(item => ({key: item.id, source: runtime.artwork(item.coverUrl),
    fallback: missingArt({title: item.title, author: item.author?.value, width: 128, height: 192, shape: 'poster'})}))
    : [{key: 'series', fallback: missingArt({title, author, width: 128, height: 192, shape: 'poster'})}];
  return <CoverHero layout={column ? 'stack' : 'row'} shape="fan" title={title} onHeight={onHeight} covers={covers} facts={facts}
    marks={direction && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
      <Icon name={direction === 'rtl' ? 'i-rtl' : 'i-ltr'} size={theme.space.sm} color={theme.colors.textSecondary}/>
      <Text style={theme.type.rowHelp}>{t(direction === 'rtl' ? 'next.reader.directionRtl' : 'next.reader.directionLtr')}</Text>
    </View>}
    actions={<Button icon="i-book" disabled={!readable} onPress={onRead}>{readLabel}</Button>}
    under={progression !== undefined && <View style={{alignSelf: 'stretch', height: 4, borderRadius: 2, backgroundColor: theme.colors.line}}>
      <View style={{width: `${progression * 100}%`, height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/>
    </View>}/>;
}

