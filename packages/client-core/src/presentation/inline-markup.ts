/** A safe, closed set of inline runs from catalog prose. It understands exactly
 * the three tags the authored descriptions use — bold, italic and a line break —
 * and nothing else: no attributes, no links, no nesting limit, no entity
 * decoding and no HTML parser. Any other angle-bracketed text stays literal, so
 * a description that never carried markup reads exactly as before. */
export type InlineMarkupRun =
  | Readonly<{kind: 'text'; text: string; strong: boolean; emphasis: boolean}>
  | Readonly<{kind: 'break'}>;

const tag = /^<(\/?)(b|strong|i|em|br)\s*\/?>$/i;

export function inlineMarkup(value: string): readonly InlineMarkupRun[] {
  const runs: InlineMarkupRun[] = [];
  let strong = 0, emphasis = 0, text = '';
  const flush = () => {
    if (text) runs.push(Object.freeze({kind: 'text' as const, text, strong: strong > 0, emphasis: emphasis > 0}));
    text = '';
  };
  for (let index = 0; index < value.length; index++) {
    const character = value[index]!;
    if (character === '<') {
      const close = value.indexOf('>', index + 1);
      const candidate = close === -1 ? '' : value.slice(index, close + 1);
      const match = tag.exec(candidate);
      if (match) {
        flush();
        const ending = match[1] === '/', name = (match[2] ?? '').toLowerCase();
        if (name === 'br') runs.push(Object.freeze({kind: 'break' as const}));
        else if (name === 'b' || name === 'strong') strong = Math.max(0, strong + (ending ? -1 : 1));
        else emphasis = Math.max(0, emphasis + (ending ? -1 : 1));
        index = close;
        continue;
      }
    }
    if (character === '\n') { flush(); runs.push(Object.freeze({kind: 'break' as const})); continue; }
    text += character;
  }
  flush();
  return Object.freeze(runs);
}
