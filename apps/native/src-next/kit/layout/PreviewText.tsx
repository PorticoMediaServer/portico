import React, {useState} from 'react';
import {Text, View, type StyleProp, type TextStyle} from 'react-native';

export function PreviewText({text, lines, style}: {text: string; lines: number; style: StyleProp<TextStyle>}) {
  const [preview, setPreview] = useState<{text: string; lines: number; value: string}>();
  const value = preview?.text === text && preview.lines === lines ? preview.value : undefined;
  return <View>
    <Text textBreakStrategy="simple" style={style}>{value}</Text>
    <Text accessible={false} importantForAccessibility="no" textBreakStrategy="simple"
      style={[style, {position: 'absolute', left: 0, right: 0, opacity: 0}]}
      onTextLayout={({nativeEvent}) => {
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
          next = words.join(' ');
        }
        if (next !== value) setPreview({text, lines, value: next});
      }}>{text}</Text>
  </View>;
}
