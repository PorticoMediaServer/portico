import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ArtworkSource} from '../../kit/artwork';
import {LibraryCard} from '../../kit/cards';
import {Focusable} from '../../kit/controls';
import {StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import type {NativeLibraryDirectory} from './directory';
import {StandardBackdrop} from '../../kit/artwork';

const pivots: Readonly<Record<string, string>> = {movie: 'movies', show: 'shows', episode: 'episodes', artist: 'artists', album: 'albums', song: 'songs', book: 'books', author: 'authors', series: 'series', ebook: 'books', comic: 'titles'};
/** Phone4d: the single authorized summary read supplies order, names, counts,
 * covers and ambient. The shell owns the real page and bottom navigation. */
export function NativeLibraryOverview({directory, runtime, activation, openLibrary, editPins}: {
  directory: NativeLibraryDirectory; runtime: NativeShellRuntime; activation: NativeDestinationActivationPort;
  openLibrary: (id: string) => void; editPins: () => void;
}) {
  const state = useSyncExternalStore(directory.subscribe, directory.getSnapshot), active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const theme = useTheme(), insets = useSafeAreaInsets(), {width} = useWindowDimensions(), scroll = useRef<ScrollView>(null), [surface, setSurface] = useState(width);
  useEffect(() => {if (active.visible) void directory.load(true).catch(runtime.reportError);}, [active.visible, active.revision, directory, runtime]);
  useEffect(() => runtime.registerScrollToTop({kind: 'libraries'}, () => scroll.current?.scrollTo({y: 0, animated: !theme.reduceMotion})), [runtime, theme.reduceMotion]);
  return <View testID="native-libraries-overview" pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible}
    importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}} onLayout={event => setSurface(event.nativeEvent.layout.width)}><StandardBackdrop/>
    <ScrollView ref={scroll} contentContainerStyle={{paddingTop: Math.max(58, insets.top), paddingBottom: 28}}>
      <View style={{flexDirection: 'row', alignItems: 'baseline', marginHorizontal: 20, marginBottom: 18}}>
        <Text accessibilityRole="header" style={[browseType(theme, 36, 800, -.035, 39), {color: theme.colors.text, flex: 1}]}>{defaultI18n.t('pins.libraries')}</Text>
        <Focusable testID="libraries-edit-pins" focusId="libraries-edit-pins" accessibilityLabel={defaultI18n.t('pins.edit')} onPress={editPins} hitSlop={12}>
          <Text style={[browseType(theme, 14, 600), {color: theme.colors.textTertiary}]}>{defaultI18n.t('pins.edit')}</Text>
        </Focusable>
      </View>
      {state.phase === 'error' && <StateView {...runtime.error(state.error, () => {void directory.load(true).catch(runtime.reportError);})}/>}
      <View style={{paddingHorizontal: 16, paddingTop: 4, gap: 12}}>
        {state.libraries.map(library => <LibraryCard key={library.id} testID={`libraries-open-${encodeURIComponent(library.id)}`} focusId={`library:${library.id}`}
          title={library.name} width={Math.max(0, surface - 32)} height={108} shape={library.kind === 'music' || library.kind === 'audiobook' ? 'square' : 'poster'}
          covers={library.summary.coverUrls.map(path => runtime.artwork(path)).filter((source): source is ArtworkSource => !!source)}
          glyph={[...library.name][0]}
          ambient={library.summary.latestArtwork ? runtime.ambient(library.summary.latestArtwork) : undefined}
          caption={library.summary.counts.filter(part => pivots[part.kind]).map(part => ({id: part.kind, role: 'value' as const,
            text: defaultI18n.t(part.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: part.count, pivot: pivots[part.kind]})}))}
          onNavigate={() => openLibrary(library.id)}/>)}
      </View>
      <Text style={[browseType(theme, 13, 400, 0, 19.5), {color: theme.colors.textTertiary, marginHorizontal: 24, marginTop: 18}]}>{defaultI18n.t('next.browse.native.libraryOrderHint')}</Text>
    </ScrollView>
  </View>;
}
