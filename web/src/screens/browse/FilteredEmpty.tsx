import {useEffect, useMemo, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {BrowseFieldCapability, BrowseNode} from '@core/browse.ts';
import type {BrowseToolbarModel} from '@portico/client-core/kit-model';
import {FilteredEmpty, type BrowseLabels, type RelaxSuggestion} from '../../kit/browse';
import type {BrowseController} from './controller';

const t = defaultI18n.t;
export type BrowseFilteredEmptyProps = Readonly<{
  controller: BrowseController;
  model: BrowseToolbarModel;
  labels: Pick<BrowseLabels, 'field'>;
  predicates: readonly BrowseNode[];
  library: string;
  onDrop: (predicates: readonly BrowseNode[]) => void;
  onClear: () => void;
}>;
/** One applied filter, named the way the chip names it, and how its absence reads. */
type Part = Readonly<{id: string; at: number; field: string; phrase: string; display: string; relaxed: string}>;
const lower = (value: unknown) => String(value).toLocaleLowerCase();
/**
 * Nothing matches: the frame names the filters that emptied the page ("Nothing in Movies is both
 * horror and from the 2010s") and offers one way out per filter, each counted over the real
 * server (`#empty` draws 6 and 11). Counts come from the same browse query the page ran, so a
 * suggestion never promises a page the server would not return.
 */
export function BrowseFilteredEmpty({controller, model, labels, predicates, library, onDrop, onClear}: BrowseFilteredEmptyProps) {
  const fields = useMemo(() => new Map(model.groups.flatMap(group => group.fields).map(field => [field.id, field])), [model]);
  const parts = useMemo<readonly Part[]>(() => predicates.flatMap((node, index) => {
    if (!('field' in node)) return [];
    const field: BrowseFieldCapability | undefined = fields.get(node.field === 'decade' ? 'decade' : node.field);
    const name = field ? labels.field(field) : node.field;
    const values = (Array.isArray(node.value) ? node.value : [node.value]).filter(value => value !== null && value !== undefined);
    if (!values.length) return [{id: String(index), at: index, field: node.field, phrase: name.toLocaleLowerCase(), display: name, relaxed: t('next.browse.emptyAnything')}];
    const decades = values.map(value => t('next.kit.filter.decade', {year: String(value)}));
    // The sentence names a value in running text; a suggestion keeps the drawn name ("Horror").
    const display = node.field === 'decade' ? t('next.browse.emptyFrom', {decade: decades.join(', ')}) : values.map(value => String(value)).join(', ');
    const phrase = node.field === 'decade' ? display : lower(display);
    const relaxed = node.field === 'decade' || node.field === 'year' ? t('next.browse.emptyAnyYear') : t('next.browse.emptyAnything');
    return [{id: String(index), at: index, field: node.field, phrase, display, relaxed}];
  }), [predicates, fields, labels]);
  const [suggestions, setSuggestions] = useState<readonly RelaxSuggestion[]>([]);
  const description = useMemo(() => !parts.length ? t('next.browse.emptyHelp') : t('next.browse.emptyNothingIn', {library,
    filters: parts.length === 2 ? t('next.browse.emptyBoth', {first: parts[0]!.phrase, second: parts[1]!.phrase}) : parts.map(part => part.phrase).join(', ')}), [library, parts]);
  const signature = JSON.stringify(predicates);
  useEffect(() => {
    if (!parts.length || parts.length > 7) { setSuggestions([]); return; }
    const stop = new AbortController();
    let current = true;
    // The frame offers the newest filter's way out first ("Horror from any year" before
    // "Anything from the 2010s"), so suggestions follow the chips in reverse order.
    const timer = setTimeout(() => {
      void Promise.all([...parts].reverse().map((part): Promise<RelaxSuggestion | undefined> => controller.count(predicates.filter((_node, at) => at !== part.at), stop.signal)
        .then(count => {
          const kept = parts.filter(other => other.id !== part.id).map(other => other.display).join(' and ');
          const relaxesYear = part.field === 'decade' || part.field === 'year';
          const label = relaxesYear ? kept || t('next.browse.emptyAnything') : kept ? `${t('next.browse.emptyAnything')} ${kept}` : t('next.browse.emptyAnything');
          return {id: part.id, label: relaxesYear ? `${label} ${part.relaxed}` : label, count: count.total,
            onSelect: () => onDrop(predicates.filter((_node, at) => at !== part.at))};
        })
        .catch(() => undefined))).then(items => {
        if (current && !stop.signal.aborted) setSuggestions(items.filter((item): item is RelaxSuggestion => item !== undefined && (item.count ?? 0) > 0));
      });
    }, 220);
    return () => {current = false; clearTimeout(timer); stop.abort();};
  }, [controller, parts, predicates, signature, onDrop]);
  return <FilteredEmpty description={description} suggestions={suggestions} onClear={onClear}/>;
}
