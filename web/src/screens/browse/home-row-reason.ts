import type {HomeRow} from '@core/home.ts';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import type {I18n} from '@i18n';

export type RowReasonPart = Readonly<{text: string; titleId?: string}>;
/** Emphasis decorates only the producer's actual title-list parameter. The
 * localized sentence, title spelling, punctuation and order stay byte-for-byte. */
export function homeRowReasonParts(row: Pick<HomeRow, 'why' | 'whyTitles'>, i18n: Pick<I18n, 't' | 'has'>): readonly RowReasonPart[] {
  const text = serverTextLabel(i18n, row.why), list = row.why?.params?.titles;
  const titles = row.whyTitles?.filter(title => title.name.length > 0) ?? [];
  if (!list || !titles.length) return [{text}];
  // Locate the actual catalogue parameter, not an earlier occurrence of the
  // same title in the sentence. Unknown/fallback copy remains unstyled.
  const marker = '\uE000home-row-titles\uE001';
  if (text.includes(marker)) return [{text}];
  const tagged = serverTextLabel({...i18n, t: (id, values) => i18n.t(id, {...values, titles: marker})}, row.why);
  const start = tagged.indexOf(marker);
  if (start < 0 || tagged.indexOf(marker, start + marker.length) >= 0
    || tagged.slice(0, start) + list + tagged.slice(start + marker.length) !== text) return [{text}];
  const parts: RowReasonPart[] = [{text: text.slice(0, start)}];
  let position = 0;
  while (position < list.length) {
    const match = titles.map(title => ({title, at: list.indexOf(title.name, position)})).filter(match => match.at >= position)
      .sort((a, b) => a.at - b.at || b.title.name.length - a.title.name.length)[0];
    if (!match) {parts.push({text: list.slice(position)});break;}
    if (match.at > position) parts.push({text: list.slice(position, match.at)});
    parts.push({text: match.title.name, titleId: match.title.id});position = match.at + match.title.name.length;
  }
  parts.push({text: text.slice(start + list.length)});
  return parts.map(part => part.text).join('') === text ? parts : [{text}];
}
