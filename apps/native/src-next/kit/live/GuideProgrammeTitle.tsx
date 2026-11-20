import React, {useState} from 'react';
import {Text, View, type StyleProp, type TextStyle} from 'react-native';

const ellipsis = '…';

type GuideTextProps = {
  title: string;
  lines: number;
  style: StyleProp<TextStyle>;
  maxHeight?: number;
  whole?: boolean;
  onHeight?: (height: number) => void;
};

/** Layout may split a word even with hyphenation turned off. Keep only whole words
 * and measure the shortened text with its ellipsis before showing it. */
export function GuideProgrammeTitle(props: GuideTextProps) {
  const [width, setWidth] = useState(0);
  return <View style={{flex: 1, minWidth: 0}} onLayout={event => setWidth(Math.floor(event.nativeEvent.layout.width))}>
    {width > 0 ? <FittedGuideText key={JSON.stringify([width, props.title, props.lines, props.maxHeight, props.whole])}
      {...props} width={width}/> : <Measuring {...props}/>}
  </View>;
}

function FittedGuideText({title, lines, style, width, maxHeight, whole, onHeight}: GuideTextProps & {width: number}) {
  const [trial, setTrial] = useState(title), [fit, setFit] = useState<string>();
  const fallback = whole ? '' : ellipsis;
  const value = fit ?? fallback;
  return <>
    {fit === undefined ? <Measuring title={title} lines={lines} style={style} whole={whole} onHeight={onHeight}/> : value !== '' && <Text textBreakStrategy="simple" android_hyphenationFrequency="none" style={style}
      onLayout={onHeight && (event => onHeight(event.nativeEvent.layout.height))}>{value}</Text>}
    {fit === undefined && <Text key={trial} accessible={false} importantForAccessibility="no"
      textBreakStrategy="simple" android_hyphenationFrequency="none"
      style={[style, {position: 'absolute', left: 0, right: 0, opacity: 0}]} onTextLayout={event => {
        let end = 0, height = 0;
        const measured = event.nativeEvent.lines;
        for (const line of measured.slice(0, lines)) {
          height += line.height;
          if (line.width > width || (maxHeight !== undefined && height > maxHeight)) break;
          while (/\s/.test(trial[end] ?? '')) end++;
          end += line.text.replace(/[\r\n]+$/, '').length;
          if (end < trial.length && !/\s/.test(trial[end] ?? '') && !/\s/.test(trial[end - 1] ?? '')) break;
        }
        if (end >= trial.length && measured.length <= lines && trial) {setFit(trial); return;}
        if (whole || trial === ellipsis) {setFit(fallback); return;}
        // If an ellipsis was already tried, discard the last offered word so
        // the next pass always has less text than this one.
        const clipped = trial !== title ? trial.slice(0, -ellipsis.length).trimEnd() : trial;
        let cut = Math.min(end, clipped.length);
        if (cut === clipped.length || !/\s/.test(clipped[cut] ?? '')) {
          while (cut > 0 && !/\s/.test(clipped[cut - 1]!)) cut--;
        }
        const shortened = clipped.slice(0, cut).trimEnd().replace(/[\s:–—-]+$/, '');
        setTrial(shortened ? shortened + ellipsis : ellipsis);
      }}>{trial}</Text>}
  </>;
}

/** While the whole-word fit is measured (several layout passes, seconds in a full guide), the title shows clipped to its
 * first line (one line, so no word is broken across lines), so a cell is never a bare "…". A `whole` title (all or nothing) stays empty until it is known to fit. */
function Measuring({title, lines, style, whole, onHeight}: Pick<GuideTextProps, 'title' | 'lines' | 'style' | 'whole' | 'onHeight'>) {
  return whole ? null : <Text numberOfLines={1} ellipsizeMode="tail" style={style}
    onLayout={onHeight && (event => onHeight(event.nativeEvent.layout.height))}>{title}</Text>;
}
