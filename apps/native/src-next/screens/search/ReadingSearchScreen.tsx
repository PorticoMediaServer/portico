import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {FlatList, Text, View, type TextInput} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ReadingIndexedSearchHit} from '../../../../../packages/client-core/src/reading-indexed-search';
import {Artwork, StandardBackdrop} from '../../kit/artwork';
import {Focusable, TextField} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {GridSkeleton, StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeReadingSearchPort, ReadingSearchOpening} from './reading-search';

/** Root54: the reader's own authored Search all books, drawn in the existing Search
 * shell. Only the canonical indexed-text answer is shown: the real excerpt plus the
 * validated match span, the title and cover only when the server supplied them, and
 * one bounded continuation. No total, locator, chapter, page or progress is implied. */
export function ReadingSearchScreen({port, opening, runtime, query, onBack}: Readonly<{
  port: NativeReadingSearchPort; opening: ReadingSearchOpening | undefined; runtime: NativeShellRuntime; query: string; onBack: () => void;
}>) {
  const t = runtime.text.t;
  const theme = useTheme(), insets = useSafeAreaInsets(), input = useRef<TextInput>(null), list = useRef<FlatList<ReadingIndexedSearchHit>>(null);
  const state = useSyncExternalStore(port.subscribe, port.getSnapshot);
  const [text, setText] = useState(query), [viewport, setViewport] = useState({width: 0, height: 0}), [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    if (state.phase !== 'loading') {setElapsed(0); return;}
    const start = Date.now(), timer = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    return () => clearTimeout(timer);
  }, [state.phase]);
  // The literal words are never rewritten. This local draft revision only stops a
  // queued Submit from an older render sending words the field no longer holds.
  const draft = useRef(0), revision = draft.current;
  const edit = (value: string) => {draft.current += 1; setText(value);};
  const submit = () => {
    if (draft.current !== revision) return;
    port.search(opening, text);
    list.current?.scrollToOffset({offset: 0, animated: false});
  };
  const rows = state.phase === 'loading' ? undefined : state.hits;
  return <View testID="native-reading-search" style={{flex: 1}}><StandardBackdrop/>
    <View style={{paddingTop: Math.max(56, insets.top), backgroundColor: theme.colors.ink}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, marginHorizontal: 12, height: 44}}>
        <Focusable testID="reading-search-back" accessibilityLabel={t('action.back')} onPress={onBack} hitSlop={10}
          style={{width: 38, height: 38, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-left" size={22} color={theme.colors.text}/></Focusable>
        <Text accessibilityRole="header" numberOfLines={1} style={[browseType(theme, 32, 800, -.035, 36.8), {color: theme.colors.text, flex: 1}]}>{t('next.reader.searchAll')}</Text>
      </View>
      <View style={{marginHorizontal: 16, marginTop: 6}}>
        <TextField ref={input} testID="reading-search-input" focusId="reading-search-input" variant="search" leadingIcon="i-search" value={text}
          cursorColor={theme.colors.accentStrong} selectionColor={theme.colors.accentStrong} onChangeText={edit}
          accessibilityLabel={t('next.reader.searchAll')} placeholder={t('web.search.placeholder')} returnKeyType="search" autoCapitalize="none" autoCorrect={false}
          onSubmitEditing={submit} fieldStyle={{height: 44, borderRadius: 12, paddingRight: 12}}/>
      </View>
      <Text style={[browseType(theme, 12.5, 500), {color: theme.colors.textTertiary, marginHorizontal: 20, marginTop: 8, marginBottom: 10}]}>{t('next.reader.indexedSearchScope')}</Text>
    </View>
    {state.phase === 'error' ? <StateView {...runtime.error(state.error, () => {if (state.query) port.search(opening, state.query, state.witness);})}/>
      : <FlatList ref={list} style={{flex: 1}} data={rows ?? []} keyExtractor={key} keyboardShouldPersistTaps="handled"
        onLayout={event => setViewport(event.nativeEvent.layout)} extraData={state} contentContainerStyle={{paddingHorizontal: 12, paddingBottom: insets.bottom + 24}}
        renderItem={({item}) => <HitRow hit={item} runtime={runtime} onOpen={() => port.open(opening, state.witness, item)}/>}
        onEndReached={() => {if (state.phase === 'ready' && !state.moreError) port.more(opening, state.witness);}}
        ListEmptyComponent={rows === undefined
          ? <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={viewport.height} width={viewport.width} totalCount={6} columns={1}/>
          : state.phase === 'ready' ? <StateView variant="search" title={t('next.reader.noIndexedMatches')} body={t('next.reader.indexedSearchScope')}/> : null}
        ListFooterComponent={state.phase === 'more' ? <GridSkeleton elapsedMs={0} scrollOffset={0} viewportHeight={viewport.height} width={viewport.width} totalCount={2} columns={1}/>
          : state.phase === 'ready' && state.moreError ? <StateView variant="search" {...runtime.error(state.moreError, () => port.retry(opening, state.witness))}/> : null}/>}
  </View>;
}

const key = (hit: ReadingIndexedSearchHit): string => `${hit.itemId}:${hit.sourceId}:${hit.sectionIndex}:${hit.utf16Offset}`;

/** The excerpt is drawn as three literal text runs, so the only emphasis is the
 * span the server named and the shared parser already bounded inside this excerpt. */
function HitRow({hit, runtime, onOpen}: Readonly<{hit: ReadingIndexedSearchHit; runtime: NativeShellRuntime; onOpen: () => void}>) {
  const t = runtime.text.t;
  const theme = useTheme(), cover = runtime.artwork(hit.coverUrl);
  const before = hit.excerpt.slice(0, hit.matchStart), match = hit.excerpt.slice(hit.matchStart, hit.matchStart + hit.matchLength), after = hit.excerpt.slice(hit.matchStart + hit.matchLength);
  return <Focusable testID={`reading-hit-${key(hit)}`} accessibilityLabel={hit.title ?? hit.excerpt} accessibilityRole="button" onPress={onOpen}
    focusKind="row" focusRadius={14} style={{flexDirection: 'row', gap: 12, padding: 10, marginBottom: 6, borderRadius: 14, alignItems: 'flex-start'}}>
    {cover && <Artwork source={cover} width={44} height={62}/>}
    <View style={{flex: 1, gap: 4}}>
      {hit.title !== undefined && <Text numberOfLines={1} style={[browseType(theme, 15.5, 700), {color: theme.colors.text}]}>{hit.title}</Text>}
      <Text numberOfLines={4} style={[browseType(theme, 14, 400), {color: theme.colors.textSecondary}]}>{before}<Text style={{fontWeight: '700', color: theme.colors.accentStrong}}>{match}</Text>{after}</Text>
    </View>
    <Icon name="i-right" size={16} color={theme.colors.textTertiary}/>
  </Focusable>;
}
