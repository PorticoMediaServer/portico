import React, {useRef, type ReactNode} from 'react';
import {ScrollView, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {savedNearEnd} from '../../screens/saved/paging';
import {FocusRegion} from '../focus';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {useTheme} from '../theme';

export function SavedResourceFrame({testID, visible, region, rail, back, barTitle, barAction, backdrop, onNearEnd,
  columns, rowStride, dragging, children, footer, sheets}: {
  testID: string; visible: boolean; region: string; rail: string; back: TopBarBack; barTitle?: string;
  barAction?: ReactNode; backdrop?: ReactNode; onNearEnd: () => void; columns: number; rowStride: number;
  dragging: boolean; children: ReactNode; footer?: ReactNode; sheets?: ReactNode;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  const viewport = useRef({offset: 0, height: 0, extent: 0});
  const nearEnd = () => {
    const {offset, height, extent} = viewport.current;
    if (savedNearEnd(offset, height, extent, rowStride, columns)) onNearEnd();
  };
  return <View testID={testID} pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
    importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}}>
    {!theme.isTV && backdrop && <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 760, overflow: 'hidden'}}>
      {backdrop}
    </View>}
    <FocusRegion id={region} entry="saved-play" orientation="vertical" enabled={visible} edges={{left: rail}} style={{flex: 1}}>
      {!theme.isTV && <View pointerEvents="box-none" style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: 10}}>
        <TopBar overArtwork back={back} title={barTitle} trailing={barAction}/>
      </View>}
      <ScrollView scrollEnabled={!dragging} scrollEventThrottle={80}
        onLayout={event => {viewport.current.height = event.nativeEvent.layout.height; nearEnd();}}
        onContentSizeChange={(_width, height) => {viewport.current.extent = height; nearEnd();}}
        onScroll={event => {viewport.current.offset = event.nativeEvent.contentOffset.y; nearEnd();}}
        contentContainerStyle={{paddingTop: insets.top + (theme.isTV ? 96 : 52), paddingBottom: theme.space.lg + insets.bottom}}>
        {children}
      </ScrollView>
    </FocusRegion>
    {footer}
    {sheets}
  </View>;
}
