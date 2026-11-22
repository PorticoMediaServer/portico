import React, {type ReactNode, type RefObject} from 'react';
import {FlatList, Text, View, type LayoutRectangle, type TextInput} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ReadingIndexedSearchHit} from '../../../../../packages/client-core/src/reading-indexed-search';
import {Artwork, type ArtworkSource} from '../artwork';
import {Focusable, TextField} from '../controls';
import {Icon} from '../Icon';
import {GridSkeleton} from '../states';
import {browseType} from '../browse/presentation';
import {TopBar} from '../layout/TopBar';
import {textLines, useTheme} from '../theme';

/** Search all books' page: the whole height, its head then its answers. */
export function ReadingSearchFrame({children}: {children: ReactNode}) {
  return <View testID="native-reading-search" style={{flex: 1}}>{children}</View>;
}

/** Search all books' head (root54): Back, the title, the search field and the line saying what is searched. */
export function ReadingSearchHead({title, back, value, onChange, onSubmit, placeholder, scope, inputRef}: {title: string;
  back: Readonly<{label: string; onPress: () => void}>; value: string; onChange: (value: string) => void; onSubmit: () => void;
  placeholder: string; scope: string; inputRef: RefObject<TextInput | null>}) {
  const theme = useTheme();
  return <View>
    <TopBar back={{label: back.label, onPress: back.onPress, testID: 'reading-search-back'}}/>
    <Text accessibilityRole="header" {...textLines(1)} style={[browseType(theme, 32, 800, -.035, 36.8), {color: theme.colors.text, marginHorizontal: 20}]}>{title}</Text>
    <View style={{marginHorizontal: 16, marginTop: 6}}>
      <TextField ref={inputRef} testID="reading-search-input" focusId="reading-search-input" variant="search" leadingIcon="i-search" value={value}
        cursorColor={theme.colors.accentStrong} selectionColor={theme.colors.accentStrong} onChangeText={onChange}
        accessibilityLabel={title} placeholder={placeholder} returnKeyType="search" autoCapitalize="none" autoCorrect={false}
        onSubmitEditing={onSubmit} fieldStyle={{height: 44, borderRadius: 12, paddingRight: 12}}/>
    </View>
    <Text style={[browseType(theme, 12.5, 500), {color: theme.colors.textTertiary, marginHorizontal: 20, marginTop: 8, marginBottom: 10}]}>{scope}</Text>
  </View>;
}

const hitKey = (hit: ReadingIndexedSearchHit): string => `${hit.itemId}:${hit.sourceId}:${hit.sectionIndex}:${hit.utf16Offset}`;

/** The answers: each hit's cover and title when the server gave them, and its excerpt with only the server's match span in
 * bold; a skeleton while the first page loads; `empty` and `footer` for the rest. */
export function ReadingSearchResults({listRef, hits, elapsed, viewport, onViewport, cover, onOpen, onEnd, empty, footer, extraData}: {
  listRef: RefObject<FlatList<ReadingIndexedSearchHit> | null>; hits: readonly ReadingIndexedSearchHit[] | undefined; elapsed: number;
  viewport: Readonly<{width: number; height: number}>; onViewport: (layout: LayoutRectangle) => void; cover: (hit: ReadingIndexedSearchHit) => ArtworkSource | undefined;
  onOpen: (hit: ReadingIndexedSearchHit) => void; onEnd: () => void; empty: ReactNode; footer: 'loading' | ReactNode; extraData: unknown}) {
  const insets = useSafeAreaInsets();
  return <FlatList ref={listRef} style={{flex: 1}} data={hits ?? []} keyExtractor={hitKey} keyboardShouldPersistTaps="handled"
    onLayout={event => onViewport(event.nativeEvent.layout)} extraData={extraData} contentContainerStyle={{paddingHorizontal: 12, paddingBottom: insets.bottom + 24}}
    renderItem={({item}) => <ReadingSearchHit hit={item} cover={cover(item)} onOpen={() => onOpen(item)}/>}
    onEndReached={onEnd}
    ListEmptyComponent={hits === undefined
      ? <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={viewport.height} width={viewport.width} totalCount={6} columns={1}/> : <>{empty}</>}
    ListFooterComponent={footer === 'loading' ? <GridSkeleton elapsedMs={0} scrollOffset={0} viewportHeight={viewport.height} width={viewport.width} totalCount={2} columns={1}/> : <>{footer}</>}/>;
}

/** The excerpt is drawn as three literal text runs, so the only emphasis is the span the server named and the shared parser
 * already bounded inside this excerpt. */
function ReadingSearchHit({hit, cover, onOpen}: {hit: ReadingIndexedSearchHit; cover?: ArtworkSource; onOpen: () => void}) {
  const theme = useTheme();
  const before = hit.excerpt.slice(0, hit.matchStart), match = hit.excerpt.slice(hit.matchStart, hit.matchStart + hit.matchLength), after = hit.excerpt.slice(hit.matchStart + hit.matchLength);
  return <Focusable testID={`reading-hit-${hitKey(hit)}`} accessibilityLabel={hit.title ?? hit.excerpt} accessibilityRole="button" onPress={onOpen}
    focusKind="row" focusRadius={14} style={{flexDirection: 'row', gap: 12, padding: 10, marginBottom: 6, borderRadius: 14, alignItems: 'flex-start'}}>
    {cover && <Artwork source={cover} width={44} height={62}/>}
    <View style={{flex: 1, gap: 4}}>
      {hit.title !== undefined && <Text {...textLines(1)} style={[browseType(theme, 15.5, 700), {color: theme.colors.text}]}>{hit.title}</Text>}
      <Text {...textLines(4)} style={[browseType(theme, 14, 400), {color: theme.colors.textSecondary}]}>{before}<Text style={{fontWeight: '700', color: theme.colors.accentStrong}}>{match}</Text>{after}</Text>
    </View>
    <Icon name="i-right" size={16} color={theme.colors.textTertiary}/>
  </Focusable>;
}
