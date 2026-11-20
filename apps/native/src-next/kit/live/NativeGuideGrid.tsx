import React, {type ComponentProps, type Ref, useId} from 'react';
import {Text, View, VirtualizedList} from 'react-native';
import {GestureDetector} from 'react-native-gesture-handler';
import Animated from 'react-native-reanimated';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {Focusable} from '../controls';
import {FocusRegion} from '../focus';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {guideSpec} from './Guide';
import {GuideRuler} from './GuideRuler';

type Shift = ComponentProps<typeof Animated.View>['style'];

export function NativeGuideGrid({gesture, rows, listRef, region, ruler, marker, docked}: {
  gesture: ComponentProps<typeof GestureDetector>['gesture'];
  rows: ComponentProps<typeof VirtualizedList<number>>;
  listRef: Ref<VirtualizedList<number>>;
  region: ComponentProps<typeof FocusRegion>;
  ruler?: ComponentProps<typeof GuideRuler>;
  marker?: Readonly<{channelWidth: number; nowAt: number; clock?: string; shift: Shift; pill: Shift; past?: Shift}>;
  docked?: Readonly<{earlier: Shift; later: Shift; label: string; earlierLabel: string; laterLabel: string; onPress: () => void}>;
}) {
  const theme = useTheme(), shadeId = useId().replace(/:/g, '');
  return <GestureDetector gesture={gesture}><View style={{flex: 1}}>
    {ruler && <GuideRuler {...ruler}/>}
    <FocusRegion {...region} style={{flex: 1}}><VirtualizedList {...rows} ref={listRef}/></FocusRegion>
    {marker && <>
      {marker.past && <Animated.View pointerEvents="none" style={[{position: 'absolute', left: marker.channelWidth, top: guideSpec.ruler, bottom: 0}, marker.past]}>
        <Svg width="100%" height="100%" accessible={false}><Defs><LinearGradient id={shadeId} x1="0" y1="0" x2="1" y2="0">
          <Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.38}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.12}/>
        </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${shadeId})`}/></Svg>
      </Animated.View>}
      <View pointerEvents="none" style={{position: 'absolute', left: marker.channelWidth, right: 0, top: 0, bottom: theme.isTV ? theme.targets.touch : 0, overflow: 'hidden'}}>
        <Animated.View style={[{position: 'absolute', left: marker.nowAt, top: 0, bottom: 0, width: theme.space.hairline * 2, backgroundColor: `${theme.colors.accentStrong}88`}, marker.shift]}>
          <Animated.View style={[{position: 'absolute', top: 0, width: guideSpec.nowPill / 2, minHeight: theme.type.rowHelp.lineHeight,
            alignItems: 'center', justifyContent: 'center', borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.accentStrong}, marker.pill]}>
            <Text style={[theme.type.rowHelp, {color: theme.colors.ink, fontFamily: theme.type.groupLabel.fontFamily}]}>{marker.clock}</Text>
          </Animated.View>
        </Animated.View>
      </View>
    </>}
    {docked && ruler && (['earlier', 'later'] as const).map(direction => <Animated.View key={direction}
      style={[{position: 'absolute', top: theme.space.hairline * 3,
        ...(direction === 'earlier' ? {left: ruler.channelWidth + theme.space.xs} : {right: theme.space.xs})}, docked[direction]]}>
      <Focusable testID={`live-docked-now-${direction}`} accessibilityRole="button" accessibilityLabel={direction === 'earlier' ? docked.earlierLabel : docked.laterLabel}
        onPress={docked.onPress} style={{minHeight: theme.type.rowLabel.lineHeight, paddingHorizontal: theme.space.sm, borderRadius: theme.baseRadii.control,
          backgroundColor: theme.colors.accentStrong, flexDirection: 'row', alignItems: 'center'}}>
        {direction === 'earlier' && <Icon name="i-left" size={theme.type.rowHelp.fontSize} color={theme.colors.ink}/>}
        <Text style={[theme.type.rowHelp, {color: theme.colors.ink}]}>{docked.label}</Text>
        {direction === 'later' && <Icon name="i-right" size={theme.type.rowHelp.fontSize} color={theme.colors.ink}/>}
      </Focusable>
    </Animated.View>)}
  </View></GestureDetector>;
}
