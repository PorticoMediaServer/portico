import React, {useState} from 'react';
import {Text, View, type StyleProp, type TextStyle} from 'react-native';

/** A time cell can be narrower than one word. Keep the full accessible title on the cell. */
export function GuideProgrammeTitle({title, lines, complete = false, style}: {title: string; lines: number; complete?: boolean; style: StyleProp<TextStyle>}) {
  const [width, setWidth] = useState(0);
  const [fit, setFit] = useState<{title: string; width: number; lines: number; value: string}>();
  const current = fit?.title === title && fit.width === width && fit.lines === lines ? fit : undefined;
  // The size belongs to the container. Remount the measurement when its width changes, even if the text wraps the same way.
  return <View style={{flex: 1, minWidth: 0}} onLayout={event => setWidth(Math.floor(event.nativeEvent.layout.width))}>
    {width > 0 && <Text key={width} textBreakStrategy="simple" android_hyphenationFrequency="none"
      style={[style, !current && {opacity: 0}]} onTextLayout={current ? undefined : event => {
        let end = 0;
        for (const line of event.nativeEvent.lines.slice(0, lines)) {
          const part = line.text.replace(/[\u2026\n]+$/, '');
          while (/\s/.test(title[end] ?? '')) end++;
          end = Math.min(title.length, end + part.length);
          if (end === title.length) break;
          if (!/\s/.test(title[end - 1] ?? '') && !/\s/.test(title[end] ?? '')) {
            while (end > 0 && !/\s/.test(title[end - 1]!)) end--;
            break;
          }
        }
        setFit({title, width, lines, value: complete && end !== title.length ? '' : title.slice(0, end).trimEnd()});
      }}>{current?.value ?? title}</Text>}
  </View>;
}
