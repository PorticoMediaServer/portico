/** A safe, closed set of inline runs from catalog prose. It understands exactly
 * the tags descriptions use — bold, italic, a line break and a paragraph — and
 * nothing else: no attributes, no links and no HTML parser. Book descriptions
 * from Audible-style sources also carry entities (&amp;, &#8217;), so the
 * common named ones and numeric ones are decoded into text; any other
 * angle-bracketed or ampersand text stays literal, so a description that
 * never carried markup reads exactly as before. */
export type InlineMarkupRun =
  | Readonly<{kind: 'text'; text: string; strong: boolean; emphasis: boolean}>
  | Readonly<{kind: 'break'}>;

const tag = /^<(\/?)(b|strong|i|em|br|p)\s*\/?>$/i;
const named: Readonly<Record<string, string>> = {amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0', ndash: '\u2013', mdash: '\u2014', hellip: '\u2026', lsquo: '\u2018', rsquo: '\u2019', ldquo: '\u201c', rdquo: '\u201d'};
const entity = /&(?:#(\d{1,7})|#[xX]([0-9a-fA-F]{1,6})|([a-z]{2,6}));/g;
const decode = (text: string) => text.replace(entity, (whole, decimal?: string, hex?: string, name?: string) => {
  if (name !== undefined) return named[name] ?? whole;
  const code = decimal !== undefined ? Number(decimal) : parseInt(hex!, 16);
  return code > 0 && code <= 0x10ffff && !(code >= 0xd800 && code <= 0xdfff) ? String.fromCodePoint(code) : whole;
});

export function inlineMarkup(value: string): readonly InlineMarkupRun[] {
  const runs: InlineMarkupRun[] = [];
  let strong = 0, emphasis = 0, text = '';
  const flush = () => {
    if (text) runs.push(Object.freeze({kind: 'text' as const, text: decode(text), strong: strong > 0, emphasis: emphasis > 0}));
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
        // A paragraph ends with a blank line; one that opens mid-text starts on a new line.
        else if (name === 'p') {
          const last = runs.at(-1);
          if (ending) runs.push(Object.freeze({kind: 'break' as const}), Object.freeze({kind: 'break' as const}));
          else if (last && last.kind !== 'break') runs.push(Object.freeze({kind: 'break' as const}));
        }
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
  while (runs.at(-1)?.kind === 'break' && runs.length && /<\/p\s*>\s*$/i.test(value)) runs.pop();
  return Object.freeze(runs);
}
