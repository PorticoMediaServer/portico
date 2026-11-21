import React, {useEffect, useRef, useState} from 'react';
import {Text, View, type StyleProp, type TextStyle} from 'react-native';

export function PreviewText({text, lines, style}: {text: string; lines: number; style: StyleProp<TextStyle>}) {
  const [preview, setPreview] = useState<{text: string; lines: number; value: string}>();
  const value = preview?.text === text && preview.lines === lines ? preview.value : undefined;
  // The hidden measuring text sometimes never reports its lines on iOS (an iPad's Home hero kept a blank synopsis line and its
  // margin, 21 Nov), as GuideProgrammeTitle's did on tvOS: with no report it is drawn again, three times at most; after that
  // the preview takes no room rather than leaving an empty band.
  const key = `${lines}:${text}`, [attempt, setAttempt] = useState({key, count: 0}), heard = useRef(false);
  const tries = attempt.key === key ? attempt.count : 0;
  useEffect(() => {
    if (value !== undefined || tries >= 3) return;
    heard.current = false;
    const timer = setTimeout(() => {if (!heard.current) setAttempt({key, count: tries + 1});}, 400);
    return () => clearTimeout(timer);
  }, [value, tries, key]);
  if (value === undefined && tries >= 3) return null;
  return <View>
    <Text textBreakStrategy="simple" style={style}>{value}</Text>
    <Text key={tries} accessible={false} importantForAccessibility="no" textBreakStrategy="simple"
      style={[style, {position: 'absolute', left: 0, right: 0, opacity: 0}]}
      onTextLayout={({nativeEvent}) => {
        heard.current = true;
        const visible = nativeEvent.lines;
        let next = text;
        if (visible.length > lines) {
          let remaining = visible.slice(0, lines).reduce((count, line) => count + (line.text ?? '').replace(/\s/gu, '').length, 0);
          const words: string[] = [];
          for (const word of text.trim().split(/\s+/u)) {
            if (word.length > remaining) break;
            remaining -= word.length;
            words.push(word);
          }
          // The native line breaker finds boundaries in scripts that don't use spaces.
          next = words.join(' ') || visible.slice(0, lines).map(line => line.text ?? '').join('').trim();
        }
        if (next !== value) setPreview({text, lines, value: next});
      }}>{text}</Text>
  </View>;
}
