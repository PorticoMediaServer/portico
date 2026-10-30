import {useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode} from 'react';
import type {SearchEntry, SearchGroupResult} from '@core/search.ts';
import {searchGroupLabel} from '@core/presentation/search-model.ts';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {useShellWidth} from '../../shell/viewport';
import {PagedShelf} from '../browse/PagedShelf';
import {t} from '../detail/text';
import {ScreenProblem} from '../detail/ScreenProblem';
import type {SearchBindings} from './bindings';
import type {SearchSelection} from './selection';
import {displayedSearchEntries, type SearchShelfRange} from './selection-window';
import styles from './search.module.css';

const keyOf = (entry: SearchEntry) => JSON.stringify(['libraryId' in entry ? entry.libraryId : '', entry.kind, entry.id]);
const periodical = (entry: SearchEntry) => (entry.kind === 'ebook' || entry.kind === 'comic') && (entry.publicationType === 'magazine' || entry.publicationType === 'newspaper');

/** A group's offered cursor owns its one pending slot. Reading retains its two artwork
 * shapes, and Songs keeps four rows per horizontal column. Neither changes server order. */
export function AllResultsRow({result, binding, selection, excluded, size, onSeeAll, renderEntry}: {
  result: SearchGroupResult; binding: SearchBindings; selection?: SearchSelection; excluded?: SearchEntry; size: number;
  onSeeAll: () => void; renderEntry: (entry: SearchEntry, index: number, selectionVisible: boolean) => ReactNode;
}) {
  const viewport = useShellWidth(), compact = viewport < 1024, root = useRef<HTMLElement>(null);
  const [width, setWidth] = useState(0);
  const [ranges, setRanges] = useState<Readonly<Record<number, SearchShelfRange>>>({});
  const partNodes = useRef(new Map<number, HTMLDivElement>()), [intersecting, setIntersecting] = useState<ReadonlySet<number>>(new Set());
  const entries = result.items.filter(entry => entry !== excluded), indexed = entries.map((entry, index) => ({entry, index}));
  const books = indexed.filter(({entry}) => !periodical(entry)), papers = indexed.filter(({entry}) => periodical(entry));
  const parts = result.id === 'reading' && books.length && papers.length ? [books, papers] : [indexed];
  const rendered = entries.length > 0 || !!result.nextCursor;
  useLayoutEffect(() => {
    const element = root.current; if (!rendered || !element) return;
    const measure = () => setWidth(element.clientWidth);
    const observer = new ResizeObserver(measure); observer.observe(element); measure();
    return () => observer.disconnect();
  }, [rendered]);
  useLayoutEffect(() => {
    const element = root.current; if (!rendered || !element) return;
    // Shelf reports horizontal geometry; intersect its actual track with the page
    // viewport as well so vertically offscreen rows do not occupy selection slots.
    let nodes = new Map<HTMLElement, number>();
    setIntersecting(current => current.size ? new Set() : current);
    let active = true;
    const observer = new IntersectionObserver(records => {
      if (!active) return;
      setIntersecting(current => {
        const next = new Set(current);
        for (const record of records) {
          const index = nodes.get(record.target as HTMLElement); if (index === undefined) continue;
          if (record.isIntersecting) next.add(index); else next.delete(index);
        }
        return next.size === current.size && [...next].every(index => current.has(index)) ? current : next;
      });
    });
    const observe = () => {
      if (!active) return;
      const next = new Map([...partNodes.current].map(([index, node]) => [node.querySelector<HTMLElement>('[data-shelf-index]')?.parentElement ?? node, index]));
      const unchanged = new Set([...nodes].flatMap(([node, index]) => next.get(node) === index ? [index] : []));
      for (const node of nodes.keys()) if (!next.has(node)) observer.unobserve(node);
      for (const node of next.keys()) if (!nodes.has(node)) observer.observe(node);
      nodes = next;
      setIntersecting(current => [...current].every(index => unchanged.has(index)) ? current : new Set([...current].filter(index => unchanged.has(index))));
    };
    observe();
    // A Shelf focus-scope replacement can replace its track without remounting the
    // section. Keep the observer on that actual node, not its detached predecessor.
    const changes = new MutationObserver(observe); changes.observe(element, {childList: true, subtree: true});
    return () => {active = false; changes.disconnect(); observer.disconnect();};
  }, [parts.length, rendered]);
  const displayed = parts.flatMap((part, index) => displayedSearchEntries(part, ranges[index], intersecting.has(index)).map(({entry}) => entry));
  const displayedSet = new Set(displayed);
  const cap = result.id === 'movies' || result.id === 'shows' ? 7 : 4;
  const available = width || Math.max(1, viewport - (compact ? 40 : 128));
  const gap = compact ? 12 : 16, columns = Math.max(1, Math.round(7 / size));
  const cardWidth = result.id === 'movies' || result.id === 'shows' ? compact ? 118 : Math.max(1, (available - gap * (columns - 1)) / columns)
    : result.id === 'episodes' ? compact ? 262 : Math.max(1, (available - 48) / 4)
    : result.id === 'songs' ? available : result.id === 'people' || result.id === 'artists' ? 124
    : result.id === 'live-tv' ? Math.min(300, viewport * .84) : compact ? 132 : 150 * size;
  const page = binding.results.page(result.id), more = !!result.nextCursor;
  const nearEnd = parts.every((part, index) => part.length <= 10 || ranges[index]?.count === part.length && ranges[index].last >= part.length - 10);
  const next = () => {void binding.results.next(result).catch(binding.context.effects.reportError);};
  const actions = <>
    {selection?.canSelectGroup(result, displayed) && <Button variant="text" size="small" className={styles.selectGroup} onClick={() => {try {selection.selectGroup(result, displayed);} catch (error) {binding.context.effects.reportError(error);}}}>{t('next.selection.selectAll', {count: displayed.length})}</Button>}
    {(more || entries.length > cap) && <Button variant="text" size="small" onClick={onSeeAll}><span className={styles.seeAll}>{t('web.search.seeAll')}<Icon name="i-right" size={18} /></span></Button>}
  </>;
  if (!rendered) return null;
  return <section ref={root} className={`${styles.section} ${styles.pagedSection}`} data-group={result.id}
    style={{'--search-preview-count': Math.max(1, Math.min(cap, entries.length)), '--search-preview-width': `${result.id === 'people' || result.id === 'artists' ? 124 : result.id === 'live-tv' ? 300 : compact ? 132 : 150 * size}px`} as CSSProperties}>
    {parts.map((part, index) => <div key={index} ref={node => {if (node) partNodes.current.set(index, node); else partNodes.current.delete(index);}}><PagedShelf className={`${styles.pagedRow} ${index ? styles.followingRow : ''}`} heading={t(searchGroupLabel(result.id))}
      subtitle={!index && result.totalCountExact ? result.totalCount > entries.length ? t('web.search.someOf', {shown: entries.length, total: result.totalCount}) : result.totalCount : undefined}
      actions={index ? undefined : actions} items={part} getKey={({entry}) => keyOf(entry)} renderItem={({entry, index: position}, item) => renderEntry(entry, position, displayedSet.has(entry) || item.focused)}
      focusScope={binding.service.getSnapshot().query ?? binding} platform={viewport < 600 ? 'phone-web' : 'desktop'}
      cardWidth={cardWidth} artworkRatio={result.id === 'movies' || result.id === 'shows' ? 2 / 3 : result.id === 'episodes' || result.id === 'live-tv' ? 16 / 9 : result.id === 'songs' ? cardWidth / 58 : result.id === 'reading' ? part[0] && periodical(part[0].entry) ? 3 / 4 : 2 / 3 : 1}
      hasMore={more && index === parts.length - 1} continuation={result.nextCursor ?? ''} loading={page.loading || !!page.error || !nearEnd} onMore={next}
      onVisibleRange={(first, last) => setRanges(current => current[index]?.count === part.length && current[index].first === first && current[index].last === last ? current : {...current, [index]: {count: part.length, first, last}})} /></div>)}
    {page.error && <ScreenProblem embedded context={binding.context} error={page.error} retry={() => {void binding.results.retry(result).catch(binding.context.effects.reportError);}} />}
  </section>;
}
