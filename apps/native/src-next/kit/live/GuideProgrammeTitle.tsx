import React, {useEffect, useRef, useState} from 'react';
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
      {...props} width={width}/> : !props.whole && <Text style={props.style}>{ellipsis}</Text>}
  </View>;
}

function FittedGuideText({title, lines, style, width, maxHeight, whole, onHeight}: GuideTextProps & {width: number}) {
  const [trial, setTrial] = useState(title), [fit, setFit] = useState<string>();
  // On tvOS a hidden measuring text mounted with a newly opened guide sometimes never reports its lines, and the cell
  // stayed "…" for good (every programme cell, Apple TV). If no report comes, the measuring text is drawn again.
  // A narrow cell measures several times (each shorter text is measured again), so the three tries are per text.
  const [attempt, setAttempt] = useState({trial: title, count: 0}), heard = useRef(false);
  const tries = attempt.trial === trial ? attempt.count : 0;
  useEffect(() => {
    if (fit !== undefined || tries >= 3) return;
    heard.current = false;
    const timer = setTimeout(() => {if (!heard.current) setAttempt({trial, count: tries + 1});}, 400);
    return () => clearTimeout(timer);
  }, [fit, tries, trial]);
  const fallback = whole ? '' : ellipsis;
  const value = fit ?? fallback;
  return <>
    {value !== '' && <Text textBreakStrategy="simple" android_hyphenationFrequency="none" style={style}
      onLayout={onHeight && (event => onHeight(event.nativeEvent.layout.height))}>{value}</Text>}
    {fit === undefined && <Text key={`${tries}:${trial}`} accessible={false} importantForAccessibility="no"
      textBreakStrategy="simple" android_hyphenationFrequency="none"
      style={[style, {position: 'absolute', left: 0, right: 0, opacity: 0}]} onTextLayout={event => {
        heard.current = true;
        let end = 0, height = 0;
        const measured = event.nativeEvent.lines;
        for (const line of measured.slice(0, lines)) {
          height += line.height;
          // iOS counts a line's trailing space in its width ("Stress Test Book - A Deliberately " measured 250 in a 249 box on
          // an iPad, and the title was cut to "Stres…"); a line the breaker ended at a space fits, so only a line with no break
          // in it (one word wider than the box) is too wide.
          if ((line.width > width && !/\s$/.test(line.text)) || (maxHeight !== undefined && height > maxHeight)) break;
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
        if (shortened) {setTrial(shortened + ellipsis); return;}
        // No whole word fits (owner, 20 Nov): cut the first word itself, one letter at a time, so the cell still shows
        // part of its title ("Peb…"); a bare "…" only when not even one letter fits.
        const word = clipped.split(/\s/)[0] ?? '', letters = Array.from(word);
        const keep = letters.length - 1;
        setTrial(keep > 0 ? letters.slice(0, keep).join('').replace(/[:–—-]+$/, '') + ellipsis : ellipsis);
      }}>{trial}</Text>}
  </>;
}
