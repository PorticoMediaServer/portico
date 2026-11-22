import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../controls';
import {shellType} from '../shell/visual';
import {useTheme} from '../theme';
import type {TVServerFact} from '../../screens/settings-live/server/tv-server-values';

/** A TV server Settings page shown read-only: each fact a row you can move through (its label, value and detail), the page's
 * empty line when there are none, and Previous and Next when the facts come a page at a time. */
export function TVServerFacts({page, facts, empty, previous, next}: {
  page: string; facts: readonly TVServerFact[]; empty: React.ReactNode; previous?: () => void; next?: () => void;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  return <View>
    {facts.map(fact => <Focusable key={fact.id} focusId={`tv-server:${page}:${fact.id}`} accessibilityRole="text" focusKind="row"
      accessibilityLabel={[fact.label, fact.value, fact.detail].filter(Boolean).join('. ')}
      style={{padding: 22, marginBottom: 8, borderRadius: 16, backgroundColor: theme.colors.raised, gap: 8}}>
      <Text style={[shellType(24, 600), {color: theme.colors.text}]}>{fact.label}</Text>
      <Text style={[shellType(22), {color: theme.colors.textSecondary}]}>{fact.value}</Text>
      {fact.detail && <Text style={[shellType(20), {color: theme.colors.textTertiary}]}>{fact.detail}</Text>}
    </Focusable>)}
    {!facts.length && empty}
    {previous && <Button focusId={`tv-server:${page}:previous`} variant="glass" onPress={previous}>{t('web.pager.previous')}</Button>}
    {next && <Button focusId={`tv-server:${page}:next`} variant="glass" onPress={next}>{t('web.pager.next')}</Button>}
  </View>;
}
