import React, {useEffect, useRef, useState} from 'react';
import {Text, View, type StyleProp, type TextStyle} from 'react-native';

const ellipsis = '…';
/** A title's fitted text, by its cell's width and limits: a cell drawn again (the guide under an opened programme sheet, a row
 * scrolled back into view) starts from the fit it had. On tvOS a measuring text under an overlay may never report, and the
 * cell fell back to "…" for as long as the sheet stayed open (Apple TV, 21 Nov). Bounded: the oldest fits go first. */
type Fit = {text?: string; height?: number; width?: number};
const fits = new Map<string, Fit>(), fitsKept = 2000;
function remember(key: string, value: Fit) {
  const next = {...fits.get(key), ...value};
  fits.delete(key); fits.set(key, next);
  if (fits.size > fitsKept) fits.delete(fits.keys().next().value!);
}

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
  // The cell's last known width (a cell drawn again under an overlay may get no layout report).
  const cell = JSON.stringify([props.title, props.lines, props.maxHeight, props.whole]);
  const [width, setWidth] = useState(() => fits.get(cell)?.width ?? 0);
  const memo = JSON.stringify([width, props.title, props.lines, props.maxHeight, props.whole]);
  return <View style={{flex: 1, minWidth: 0}} onLayout={event => {const value = Math.floor(event.nativeEvent.layout.width); remember(cell, {width: value}); setWidth(value);}}>
    {width > 0 ? <FittedGuideText key={memo} {...props} width={width} memo={memo}/> : !props.whole && <Text style={props.style}>{ellipsis}</Text>}
  </View>;
}

function FittedGuideText({title, lines, style, width, maxHeight, whole, onHeight, memo}: GuideTextProps & {width: number; memo: string}) {
  const [trial, setTrial] = useState(title), [fit, setFitState] = useState<string | undefined>(() => fits.get(memo)?.text);
  const setFit = (value: string) => {remember(memo, {text: value}); setFitState(value);};
  // The fitted text's height, given again to a cell drawn again (its time line waits for it).
  const known = fits.get(memo)?.height;
  useEffect(() => {if (known !== undefined) onHeight?.(known);}, [known, onHeight]);
  // On tvOS a hidden measuring text mounted with a newly opened guide sometimes never reports its lines, and the cell
  // stayed "…" for good (every programme cell, Apple TV). If no report comes, the measuring text is drawn again.
  // A narrow cell measures several times (each shorter text is measured again), so the tries are per text.
  const [attempt, setAttempt] = useState({trial: title, count: 0}), heard = useRef(false);
  const tries = attempt.trial === trial ? attempt.count : 0;
  useEffect(() => {
    if (fit !== undefined) return;
    heard.current = false;
    // Three quick tries, then a slow one for as long as the cell is drawn: under an open sheet a measure may never report
    // until the sheet goes, and a cell that stopped asking stayed "…" after it closed.
    const timer = setTimeout(() => {if (!heard.current) setAttempt({trial, count: tries + 1});}, tries < 3 ? 400 : 1500);
    return () => clearTimeout(timer);
  }, [fit, tries, trial]);
  const fallback = whole ? '' : ellipsis;
  const value = fit ?? fallback;
  return <>
    {value !== '' && <Text textBreakStrategy="simple" android_hyphenationFrequency="none" style={style}
      onLayout={onHeight && (event => {remember(memo, {height: event.nativeEvent.layout.height}); onHeight(event.nativeEvent.layout.height);})}>{value}</Text>}
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
