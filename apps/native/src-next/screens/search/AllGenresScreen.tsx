import React, {useCallback, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {FlatList, Text, View, type ViewToken} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {Focusable} from '../../kit/controls';
import {MenuRow} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {GridSkeleton, StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeSearchGenreList} from './explore';
import type {NativeSearchGenre} from './types';
import {StandardBackdrop} from '../../kit/artwork';

/** The same canonical genre read, including genres without tile artwork. This
 * list follows the returned cursor and never reranks or expands a library. */
export function AllGenresScreen({focusScope, explore, runtime, visible, current, onBack}: Readonly<{
  focusScope?: string; explore: NativeSearchGenreList; runtime: NativeShellRuntime; visible: boolean; current: () => boolean; onBack: () => void;
}>) {
  const t = runtime.text.t;
  const theme = useTheme(), insets = useSafeAreaInsets(), state = useSyncExternalStore(explore.subscribe, explore.getSnapshot);
  const scroll = useRef(0), direction = useRef<'next' | 'previous' | undefined>(undefined);
  const viewable = useCallback(({viewableItems}: {viewableItems: ViewToken<NativeSearchGenre>[]}) => {
    explore.setVisible(viewableItems.filter(item => item.isViewable).map(item => item.item.id));
  }, [explore]);
  const [viewport, setViewport] = useState({width: 0, height: 0}), [elapsed, setElapsed] = useState(0);
  useEffect(() => {if (visible && current()) void explore.load();}, [current, explore, visible]);
  useEffect(() => {
    if (state.phase !== 'loading') {setElapsed(0); return;}
    const start = Date.now(), timer = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    return () => clearTimeout(timer);
  }, [state.phase]);
  return <View testID="native-search-all-genres" style={{flex: 1}}><StandardBackdrop/>
    <View style={{paddingTop: insets.top, marginBottom: 8}}>
      <View style={{height: 44, paddingHorizontal: 12, justifyContent: 'center'}}><Focusable testID="search-all-genres-back" accessibilityLabel={t('action.back')}
        onPress={onBack} style={{width: 38, height: 38, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-left" size={22} color={theme.colors.text}/></Focusable></View>
      <Text accessibilityRole="header" style={[browseType(theme, 32, 800, -.035, 36.8), {color: theme.colors.text, marginHorizontal: 20}]}>{t('next.search.browseByGenre')}</Text>
    </View>
    {state.phase === 'error' && !state.genres.length ? <StateView {...runtime.error(state.error, () => {if (current()) void explore.load();})}/>
      : <FlatList style={{flex: 1}} onLayout={event => setViewport(event.nativeEvent.layout)} data={state.genres} maintainVisibleContentPosition={{minIndexForVisible: 0}}
        initialNumToRender={20} maxToRenderPerBatch={20} windowSize={5} onViewableItemsChanged={viewable} keyExtractor={genre => genre.id} keyboardShouldPersistTaps="handled" contentContainerStyle={{paddingHorizontal: 12, paddingBottom: insets.bottom + 24}}
        renderItem={({item}) => <MenuRow label={item.name} description={t('next.selection.titles', {count: item.count})} variant="compact"
          focusId={`${focusScope ?? 'search-all'}:genre:${item.id}`} onFocus={() => explore.setFocused(item.id, true)} onBlur={() => explore.setFocused(item.id, false)}
          onPress={() => {if (current()) explore.openGenre(item);}}/>}
        onScroll={event => {
          const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent, previous = scroll.current;
          scroll.current = contentOffset.y;
          direction.current = contentOffset.y > previous ? 'next' : contentOffset.y < previous ? 'previous' : undefined;
          if (!current() || state.phase !== 'ready') return;
          if (direction.current === 'next' && contentOffset.y + layoutMeasurement.height >= contentSize.height - 1) void explore.next();
          else if (direction.current === 'previous' && contentOffset.y <= 1) void explore.previous();
        }} scrollEventThrottle={16} onStartReachedThreshold={.5} onEndReachedThreshold={.5}
        onStartReached={() => {if (current() && state.phase === 'ready' && direction.current === 'previous') void explore.previous();}}
        onEndReached={() => {if (current() && state.phase === 'ready' && direction.current === 'next') void explore.next();}}
        ListFooterComponent={state.phase === 'error' ? <StateView {...runtime.error(state.error, () => {if (current()) void explore.load();})}/> : null}
        ListEmptyComponent={state.phase === 'loading' ? <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={viewport.height} width={viewport.width} totalCount={6} columns={2}/> : null}/>}
  </View>;
}
