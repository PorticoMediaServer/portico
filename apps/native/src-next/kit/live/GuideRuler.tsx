import React from 'react';
import {Text, View} from 'react-native';
import {useAnimatedStyle, type SharedValue} from 'react-native-reanimated';
import Animated from 'react-native-reanimated';
import {useTheme} from '../theme';
import {guideSpec} from './Guide';

export type GuideTimeMark = Readonly<{at: number; left: number; label: string}>;

export function GuideRuler({marks, nowAt, nowVisible, paneWidth, channelWidth, leading, shift}: {
  marks: readonly GuideTimeMark[];
  nowAt: number;
  nowVisible: boolean;
  paneWidth: number;
  channelWidth: number;
  leading?: React.ReactNode;
  shift: SharedValue<number>;
}) {
  const theme = useTheme();
  const tv = theme.isTV;
  const height = guideSpec.ruler + (tv ? theme.space.sm : 0);
  return <View style={{height, flexDirection: 'row', marginLeft: leading ? 0 : channelWidth}}>
    {leading && <View style={{width: channelWidth, paddingLeft: theme.space.xs}}>{leading}</View>}
    <View style={{flex: 1, overflow: 'hidden'}}>
      {marks.map(mark => <RulerMark key={mark.at} mark={mark} nowAt={nowAt} nowVisible={nowVisible} paneWidth={paneWidth} shift={shift}/>)}
    </View>
  </View>;
}

function RulerMark({mark, nowAt, nowVisible, paneWidth, shift}: {
  mark: GuideTimeMark; nowAt: number; nowVisible: boolean; paneWidth: number; shift: SharedValue<number>;
}) {
  const theme = useTheme(), tv = theme.isTV;
  const [labelWidth, setLabelWidth] = React.useState(0);
  const pillWidth = guideSpec.nowPill / 2, gap = theme.space.xs;
  const style = useAnimatedStyle(() => {
    const now = nowAt + shift.value;
    const left = mark.left + shift.value;
    const markerStart = Math.max(0, Math.min(now - pillWidth / 2, paneWidth - pillWidth));
    const nearNow = nowVisible && now >= 0 && now < paneWidth
      && left < markerStart + pillWidth + gap && left + labelWidth + gap * 2 > markerStart;
    return {transform: [{translateX: shift.value}], opacity: nearNow ? 0 : 1};
  }, [nowAt, mark.left, nowVisible, paneWidth, labelWidth, gap, pillWidth]);
  const layout = {position: 'absolute' as const, left: mark.left, height: guideSpec.ruler, borderLeftWidth: theme.space.hairline,
    borderLeftColor: theme.colors.line, paddingLeft: gap};
  const label = <Text onLayout={event => setLabelWidth(event.nativeEvent.layout.width)}
    style={[tv ? theme.type.rowHelp : theme.type.caption, {color: theme.colors.textSecondary}]}>{mark.label}</Text>;
  if (tv) {
    const markerStart = Math.max(0, Math.min(nowAt - pillWidth / 2, paneWidth - pillWidth));
    const nearNow = nowVisible && mark.left < markerStart + pillWidth + gap && mark.left + labelWidth + gap * 2 > markerStart;
    return mark.left >= 0 ? <View style={[layout, nearNow && {opacity: 0}]}>{label}</View> : null;
  }
  return <Animated.View style={[layout, style]}>{label}</Animated.View>;
}
