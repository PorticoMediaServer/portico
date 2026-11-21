import React, {useState, type ReactNode} from 'react';
import {ScrollView, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {NativeReadPeriodicalState} from '../../screens/read/read-periodical-model';
import {AmbientArt, type AmbientArtSource} from '../artwork';
import {usePageWidth} from '../layout/page-inset';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {PeriodicalSkeleton} from '../reading/PeriodicalParts';
import {ReadingLoading} from '../reading/ReadingLoading';
import {SkeletonBlock} from '../states';
import {useTheme, type Theme} from '../theme';

type ArchiveLayout = Readonly<{theme: Theme; tablet: boolean; width: number; hero: ReactNode; bottom: number}>;

export function ReadingPeriodicalFrame({testID, back, magazine, ready, state, error, backdrop, hero, body, menu, onNext, nextError}: {
  testID: string; back: TopBarBack; magazine: boolean; ready: boolean; state: NativeReadPeriodicalState;
  error?: ReactNode; backdrop?: AmbientArtSource; hero: (layout: Pick<ArchiveLayout, 'theme' | 'tablet'>) => ReactNode;
  body: (layout: ArchiveLayout) => ReactNode; menu?: ReactNode; onNext: () => void; nextError?: ReactNode;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), pageWidth = usePageWidth();
  const tablet = theme.platform !== 'phone', [paneWidth, setPaneWidth] = useState(0);
  const width = Math.min(1180, paneWidth || pageWidth);
  const top = Math.max(96, insets.top + 56), bottom = insets.bottom + 48;
  const header = hero({theme, tablet}), archive = body({theme, tablet, width, hero: <View style={{paddingTop: top}}>{header}</View>, bottom});
  return <View testID={testID} style={{flex: 1}} onLayout={event => setPaneWidth(event.nativeEvent.layout.width)}>
    {tablet && backdrop && <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 760, overflow: 'hidden'}}>
      <AmbientArt source={backdrop} treatment="title" style={{position: 'absolute', inset: 0}}/>
    </View>}
    <View pointerEvents="box-none" style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: theme.layers.sticky}}>
      <TopBar overArtwork back={back}/>
    </View>
    {error ? <View style={{paddingTop: top}}>{error}</View>
      : <ReadingLoading ready={ready} style={{flex: 1}} skeleton={<PeriodicalSkeleton magazine={magazine} tablet={tablet} width={width} top={top}/>}>
        {magazine ? archive : <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{paddingTop: top, paddingBottom: bottom}}
          scrollEventThrottle={64} onScroll={event => {
            const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
            if (state.nextCursor && !state.pending && !state.error && contentSize.height - contentOffset.y - layoutMeasurement.height < 1000) onNext();
          }}>
          <View style={{width: '100%', maxWidth: 1180, alignSelf: 'center'}}>
            {header}{archive}
            {state.pending && <SkeletonBlock width={150} height={200} style={{marginHorizontal: theme.gutter, marginTop: theme.space.sm}}/>}
            {nextError}
          </View>
        </ScrollView>}
      </ReadingLoading>}
    {menu}
  </View>;
}
