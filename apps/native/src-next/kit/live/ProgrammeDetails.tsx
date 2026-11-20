import React from 'react';
import {Text, View} from 'react-native';
import type {GuideProgram} from '../../../../../packages/client-core/src/guide';
import {programmeFactParts} from '../../../../../packages/client-core/src/presentation/kit-model';
import type {NativeShellRuntime} from '../../shell/runtime';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

export function ProgrammeFlags({programme, flags = programme?.flags, grid = false, text}: {programme?: GuideProgram; flags?: GuideProgram['flags']; grid?: boolean; text: NativeShellRuntime['text']}) {
  const theme = useTheme(), t = text.t;
  const gridFlag = grid && !theme.isTV;
  return <>
    {flags?.live && <View style={{flexDirection: 'row', gap: 6, alignItems: 'center', ...(gridFlag ? {backgroundColor: alpha(theme.colors.record, .18), borderRadius: 4, paddingHorizontal: 5, paddingVertical: 1} : {})}}>
      {!gridFlag && <View style={{width: theme.isTV ? 8 : 7, height: theme.isTV ? 8 : 7, borderRadius: 4, backgroundColor: theme.colors.record}}/>}
      <Text style={[theme.type.tag, {color: gridFlag ? '#f9b1b1' : theme.colors.record}]}>{t('guide.live')}</Text>
    </View>}
    {flags?.new && <Text style={[theme.type.tag, {paddingHorizontal: 5, paddingVertical: theme.isTV ? 0 : 1, borderRadius: 4, backgroundColor: theme.isTV ? theme.colors.accentStrong : theme.colors.accent, color: theme.colors.ink}]}>{t('guide.new')}</Text>}
    {flags?.premiere && <Text style={[theme.type.tag, {color: theme.isTV ? theme.colors.textSecondary : theme.colors.accentStrong, ...(!theme.isTV ? {borderWidth: 1.5, borderColor: theme.colors.accentStrong, borderRadius: 4, paddingHorizontal: 5} : {})}]}>{t('guide.premiere')}</Text>}
  </>;
}

export function ProgrammeDetails({programme, now, runtime}: {programme: GuideProgram; now: number; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t;
  const episode = programme.episode?.number !== undefined ? {mode: 'seasonal' as const, season: programme.episode.season, episode: programme.episode.number} : undefined;
  const categories = programme.categories?.filter(value => value.toLowerCase() !== 'live');
  const onNow = programme.start <= now && programme.end > now, fraction = Math.max(0, Math.min(1, (now - programme.start) / (programme.end - programme.start)));
  // TV keeps its single wrapped fact line; the touch panel follows tablet-play6 and the
  // phone sheet: one identity line, the frame's meta row, then the bar with what is left.
  if (theme.isTV) {
    const facts = programmeFactParts({startMs: programme.start, endMs: programme.end, subtitle: programme.subtitle, episode, year: programme.year, categories, rating: programme.rating}, runtime.text, now);
    const tags = facts.filter(fact => fact.id === 'content-rating' || fact.id.startsWith('category-')), row = facts.filter(fact => !tags.includes(fact) && fact.id !== 'started');
    return <View style={{gap: 22}}>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 20}}>{[...row, ...tags].map(fact => <Text key={fact.id} style={[fact.id === 'remaining' ? theme.type.bodyStrong : theme.type.body, {color: theme.colors.textSecondary}]}>{fact.text}</Text>)}</View>
      {onNow && <View style={{height: 5, borderRadius: 3, backgroundColor: theme.colors.line, overflow: 'hidden'}}><View style={{height: '100%', width: `${fraction * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
      <Text style={[theme.type.intro, {color: programme.description ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{programme.description || t('guide.programNoDescription')}</Text>
    </View>;
  }
  const identity = programmeFactParts({episode, subtitle: programme.subtitle}, runtime.text);
  const facts = programmeFactParts({startMs: programme.start, endMs: programme.end, year: programme.year, categories, rating: programme.rating,
    flags: {new: programme.flags?.new === true, premiere: programme.flags?.premiere === true}}, runtime.text, now);
  const when = facts.find(fact => fact.id === 'time-range'), year = facts.find(fact => fact.id === 'year'), rating = facts.find(fact => fact.id === 'content-rating');
  const categoryFacts = facts.filter(fact => fact.id.startsWith('category-'));
  const genres = categoryFacts.length ? categoryFacts.slice(1).reduce((list, fact) => t('next.kit.genres.append', {list, genre: fact.text}), categoryFacts[0]!.text) : '';
  const started = facts.find(fact => fact.id === 'started'), remaining = facts.find(fact => fact.id === 'remaining');
  return <View style={{gap: 16}}>
    {identity.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}>{identity.map(fact => <Text key={fact.id} style={[fact.id === 'episode' ? theme.type.captionStrong : theme.type.caption, {color: theme.colors.text}]}>{fact.text}</Text>)}</View>}
    <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 12}}>
      {when && <Text style={[theme.type.captionStrong, {color: theme.colors.text}]}>{when.text}</Text>}
      {genres !== '' && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{genres}</Text>}
      {year && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{year.text}</Text>}
      {rating && <Text style={[theme.type.tag, {color: theme.colors.textSecondary, borderRadius: 5, borderWidth: 1, borderColor: theme.colors.lineStrong, paddingVertical: 4, paddingHorizontal: 8}]}>{rating.text}</Text>}
      <ProgrammeFlags programme={programme} flags={{new: programme.flags?.new, premiere: programme.flags?.premiere}} text={runtime.text}/>
    </View>
    {(started || remaining) && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <View style={{flex: 1, height: 4, borderRadius: 3, backgroundColor: theme.colors.line, overflow: 'hidden'}}><View style={{height: '100%', width: `${fraction * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>
      {started && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{started.text}</Text>}
      {remaining && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{remaining.text}</Text>}
    </View>}
    <Text style={[theme.type.rowHelp, {color: programme.description ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{programme.description || t('guide.programNoDescription')}</Text>
  </View>;
}
