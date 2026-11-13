import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FlatList, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {loadReadingFacetPage, type FacetRequest} from '../../../../../packages/client-core/src/query/reading-facets';
import {Focusable} from '../../kit/controls';
import {StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {ReadingLoading} from './ReadingLoading';

type Choice = Readonly<{value: string; label: string; count: number; seriesId?: string}>;
const page = 100;

/** A Read library's Authors or Series tab on the phone and tablet (phone-read frames): its people
 * by the author role or its series, each with how many titles it holds, read a page at a time
 * through client-core's facet reader. Choosing one opens that author's titles or that series. */
export function ReadingFacetList({libraryId, field, pivot, request, runtime, onChoose}: {
  libraryId: string; field: 'author' | 'series'; /** The library's grid pivot (books or comics). */ pivot: string; request: FacetRequest; runtime: NativeShellRuntime; onChoose: (choice: Choice) => void;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  const [values, setValues] = useState<readonly Choice[]>([]), [total, setTotal] = useState<number>(), [failed, setFailed] = useState<unknown>();
  const [loading, setLoading] = useState(true), life = useRef<AbortController | undefined>(undefined);
  const load = useCallback((offset: number) => {
    const own = new AbortController(); life.current?.abort(); life.current = own; setLoading(true); setFailed(undefined);
    void loadReadingFacetPage(request, {libraryId, field, pivot, limit: page, offset}, own.signal).then(next => {
      if (own.signal.aborted) return;
      setValues(before => [...(offset ? before : []), ...next.values]); setTotal(next.total); setLoading(false);
    }, error => {if (!own.signal.aborted) {setFailed(error); setLoading(false);}});
  }, [request, libraryId, field, pivot]);
  useEffect(() => {load(0); return () => life.current?.abort();}, [load]);
  if (failed && !values.length) return <StateView variant="inline" {...runtime.error(failed, () => load(0), 'library')}/>;
  return <ReadingLoading ready={!loading || values.length > 0} skeleton={null} style={{flex: 1}}>
    <FlatList data={values} keyExtractor={choice => choice.value} contentContainerStyle={{paddingHorizontal: theme.gutter, paddingBottom: insets.bottom + 40}}
      onEndReached={() => {if (!loading && total !== undefined && values.length < total) load(values.length);}}
      ListEmptyComponent={loading ? null : <Text style={[browseType(theme, 15, 500), {color: theme.colors.textTertiary, paddingVertical: 24}]}>{defaultI18n.t('title.nothingHere')}</Text>}
      renderItem={({item}) => <Focusable testID={`reading-${field}-${item.value}`} accessibilityRole="button" focusKind="row"
        accessibilityLabel={`${item.label}, ${defaultI18n.number(item.count)}`} onPress={() => onChoose(item)}
        style={{minHeight: 52, flexDirection: 'row', alignItems: 'center', gap: 12, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
        <Text numberOfLines={1} style={[browseType(theme, 15, 500), {color: theme.colors.text, flex: 1}]}>{item.label}</Text>
        <Text style={[browseType(theme, 14, 500), {color: theme.colors.textTertiary}]}>{defaultI18n.number(item.count)}</Text>
      </Focusable>}/>
  </ReadingLoading>;
}
