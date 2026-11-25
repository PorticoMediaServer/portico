import {defaultI18n} from '../../../../../../packages/i18n/src';
import React, {useCallback, useEffect, useRef, useState} from 'react';
import type {FlatList, ScrollView, TextInput} from 'react-native';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {SearchEntry} from '../../../../../../packages/client-core/src/search';
import {searchGroupLabel, topResultKindLabel} from '../../../../../../packages/client-core/src/presentation/search-model';
import {Artwork} from '../../../kit/artwork';
import {Parts} from '../../../kit/cards/primitives';
import {Button, Chip, TextField} from '../../../kit/controls';
import {SearchFrame} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {Banner} from '../../../kit/overlays';
import {SearchCancel, SearchClear, SearchHistory, TopResult} from '../../../kit/search';
import {SearchGenres, SearchLoading, SearchOnNow, SearchResultRow, SearchResultsGrid, SearchResultShelf, SearchSection} from '../../../kit/search/SearchResults';
import {StateView} from '../../../kit/states';
import type {NativeSearchScreenProps} from '../../shared/search/useSearch';
import {nativeSearchGenreTiles} from '../../../screens/search/genre-tiles';
import {nativeSearchChipCount, nativeSearchEntryKey} from '../../../screens/search/model';
import {useSearch} from '../../shared/search/useSearch';

export function SearchPage(props: NativeSearchScreenProps) {
  const {destination, runtime, explore, actions, selection} = props;
  const {active, state, typed, emptyAll, emptyIn, problem, groups, top, chips, chipsDrawn, landing, live, liveNow, showLive, t} = useSearch(props);
  const field = useRef<TextInput>(null), scroll = useRef<ScrollView>(null), grid = useRef<FlatList<SearchEntry>>(null);
  const [focused, setFocused] = useState(false), [elapsed, setElapsed] = useState(0);
  const loading = state.search.phase === 'loading' && !state.groups.length;
  const run = useCallback((work: Promise<void>) => {void work.catch(runtime.reportError);}, [runtime]);
  const {form} = useFrameMetrics();
  const focusedOnEntry = useRef(false);
  useEffect(() => {
    if (!active.visible) {focusedOnEntry.current = false; return;}
    if (form === 'phone' || focusedOnEntry.current) return;
    focusedOnEntry.current = true;
    const frame = requestAnimationFrame(() => field.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [active.visible, form]);
  const historyLoaded = useRef(false);
  useEffect(() => {
    if (!active.visible) return;
    if (!historyLoaded.current) {historyLoaded.current = true; run(destination.refreshHistory());}
    run(explore.load());
  }, [active.visible, destination, explore, run]);
  useEffect(() => selection?.register(state.search, state.groups), [selection, state.search, state.groups]);
  useEffect(() => runtime.registerScrollToTop({kind: 'search'}, () => {
    scroll.current?.scrollTo({y: 0, animated: false});
    grid.current?.scrollToOffset({offset: 0, animated: false});
  }), [runtime]);
  useEffect(() => {
    if (!loading) {setElapsed(0); return;}
    const timer = setTimeout(() => setElapsed(300), 300);
    return () => clearTimeout(timer);
  }, [loading]);

  const error = state.search.error ? <StateView {...runtime.error(state.search.error, () => run(destination.retry()), 'search')}/> : undefined;
  const emptyState = problem ? {title: problem}
    : emptyAll ? {testID: 'search-no-matches', title: t('next.states.noMatches', {query: state.text.trim()}), body: t('next.states.noMatchesBody')}
    : emptyIn ? {testID: 'search-none-in', title: t('web.search.nothingIn', {group: t(searchGroupLabel(emptyIn))}),
      action: <Button variant="text" size="small" onPress={() => destination.selectGroup('all')}>{t('web.search.everything')}</Button>} : undefined;
  const scopes = chipsDrawn ? chips.map(chip => {
    const group = groups.find(candidate => candidate.id === chip.id);
    const count = nativeSearchChipCount(chip.id, groups, state.totals);
    return <Chip key={chip.id} testID={`search-chip-${chip.id}`} selected={state.group === chip.id} count={count} retry={group?.status === 'error'}
      disabled={group?.status === 'success' && group.totalCount === 0} onPress={() => destination.selectGroup(chip.id)}>{t(chip.label)}</Chip>;
  }) : undefined;
  const recent = <SearchHistory title={t('web.search.recent')}
    clear={{label: t('web.search.clearRecent'), disabled: state.clearing, onPress: () => run(destination.clearHistory())}}
    items={state.recent.map(item => ({query: item.q, onPress: () => destination.useRecent(item.q),
      remove: {label: t('web.search.removeRecent', {query: item.q}), onPress: () => run(destination.removeRecent(item))}}))}/>;
  const genreTiles = landing.phase === 'ready' ? nativeSearchGenreTiles(emptyAll ? landing.backdropGenres ?? [] : landing.genres, emptyAll ? 'phone-empty' : 'phone') : [];
  // desktop-search: "All genres ›" at the end of Browse by genre opens every genre.
  const genres = genreTiles.length > 0 || landing.phase === 'error' ? <SearchSection title={t(emptyAll ? landing.emptyHeading : landing.initialHeading)}
    action={genreTiles.length > 0 ? {label: t('next.search.allGenres'), onPress: explore.openAll} : undefined}>
    {landing.phase === 'error'
      ? <Banner placement="inline" variant="notice" tone="warning" icon="i-alert" message={t('next.search.genresUnavailable')}
        action={{label: t('next.states.tryAgain'), onPress: () => run(explore.load())}}/>
      : <SearchGenres genres={genreTiles} backdrop={emptyAll} runtime={runtime} open={genre => explore.openGenre(genre)}/>}
  </SearchSection> : undefined;
  const wide = form === 'expanded';
  const topEntry = wide ? undefined : top?.entry;
  const topKey = top && topEntry ? nativeSearchEntryKey(top.group, topEntry) : undefined;
  const media = topEntry && topEntry.kind !== 'person' && topEntry.kind !== 'channel' ? topEntry as ContentEntry : undefined;
  const title = media?.displayTitle || topEntry?.title;
  const kind = top && topResultKindLabel(top.group);
  const selectedGroup = groups.find(group => group.id === state.group);
  const groupList = typed && state.group !== 'all' && !emptyIn && !problem
    ? <SearchResultsGrid entries={selectedGroup?.items ?? []} group={state.group} runtime={runtime} actions={actions} selection={selection}
      scrollRef={grid} footer={error || (loading ? <SearchLoading elapsed={elapsed}/> : undefined)}
      onMore={selectedGroup?.nextCursor && state.search.phase === 'ready' && active.visible ? () => run(destination.next(selectedGroup.id)) : undefined}/> : undefined;
  return <SearchFrame testID="native-search-screen" title={!typed && !focused ? t('web.shell.search') : undefined}
    field={<TextField ref={field} testID="search-input" focusId="search-input" variant="search" prominent leadingIcon="i-search"
      value={state.text} accessibilityLabel={t('web.shell.search')} placeholder={t('web.search.placeholder')}
      autoCapitalize="none" autoCorrect={false} returnKeyType="search"
      onChangeText={value => destination.setText(value)} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
      onSubmitEditing={() => run(destination.commit())}
      trailing={typed && <SearchClear testID="search-clear" label={t('web.search.clear')} onPress={() => destination.setText('')}/>}/>}
    cancel={typed || focused ? <SearchCancel testID="search-cancel" label={t('action.cancel')}
      onPress={() => {destination.setText(''); field.current?.blur(); setFocused(false);}}/> : undefined}
    scopes={scopes} notice={groupList ? undefined : error} none={emptyState} list={groupList} scrollRef={scroll}
    overlay={selection?.render(path => {const source = runtime.artwork(path); return source && <Artwork source={source}/>;})}>
    {!typed ? <>{recent}{genres}</> : emptyAll ? <>{genres}{recent}</> : null}
    {showLive && live.phase === 'ready' && live.cards.length > 0 && <SearchOnNow snapshot={live} runtime={runtime} now={liveNow}
      onGuide={() => runtime.onDestination({kind: 'live'})}/>}
    {top && topEntry && title && <TopResult testID="search-top-result" eyebrow={t('search.topResult')} title={title}
      facts={kind && <Parts parts={[{id: 'kind', text: t(kind), role: 'context'}]} fit="wrap"/>}
      artwork={runtime.artwork(media?.backdropUrl ?? topEntry.posterUrl)}
      selection={selection?.item(topEntry, top.group)} selectionLabel={t('card.select', {title})}
      menuOpen={actions.isMenuOpen?.(topEntry, top.group)}
      onMenu={actions.canMenu?.(topEntry, top.group) === false ? undefined : anchor => actions.menu(topEntry, top.group, anchor)}
      onPress={() => actions.open(topEntry, top.group)}
      play={media?.playback ? {label: t('card.play', {title}), title: t('entry.play'), onPress: () => actions.play(topEntry, top.group)} : undefined}/>}
    {state.group === 'all' && groups.map(group => {
      if (wide) {
        if (!group.items.length) return null;
        const paging = state.paging?.[group.id];
        return <SearchResultShelf key={group.id} group={group.id} title={t(searchGroupLabel(group.id))}
          count={group.totalCountExact ? defaultI18n.number(group.totalCount) : undefined} entries={group.items}
          runtime={runtime} actions={actions} selection={selection} loading={paging?.loading}
          failure={paging?.error === undefined ? undefined : runtime.error(paging.error, () => run(destination.retryGroup(group.id)), 'search')}
          onMore={group.nextCursor && active.visible && state.search.phase === 'ready' && !paging?.loading && !paging?.error
            ? () => run(destination.next(group.id)) : undefined}/>;
      }
      const entries = group.items.filter(entry => topKey === undefined || nativeSearchEntryKey(group.id, entry) !== topKey).slice(0, 3);
      if (!entries.length) return null;
      const more = !!group.nextCursor || group.totalCount > entries.length + (top?.group === group.id ? 1 : 0);
      return <SearchSection key={group.id} title={t(searchGroupLabel(group.id))}
        detail={more && group.totalCountExact ? t('web.search.someOf', {shown: entries.length, total: group.totalCount}) : undefined}
        action={more ? {label: t('web.search.seeAll'), onPress: () => destination.selectGroup(group.id)} : undefined}>
        {entries.map(entry => <SearchResultRow key={nativeSearchEntryKey(group.id, entry)} entry={entry} group={group.id}
          runtime={runtime} actions={actions} selection={selection?.item(entry, group.id)}/>)}
      </SearchSection>;
    })}
    {loading && <SearchLoading elapsed={elapsed}/>}
  </SearchFrame>;
}
