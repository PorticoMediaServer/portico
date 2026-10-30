import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {readLibrarySummaries} from '@core/library-summary.ts';
import {LibraryCard, LibraryRow} from '../../kit/cards';
import type {ShellArtwork} from '../../shell/artwork';
import {useShellWidth} from '../../shell/viewport';
import styles from './browse.module.css';

export type LibrariesBindings = Readonly<{
  libraries: Awaited<ReturnType<typeof readLibrarySummaries>>;
  artwork: ShellArtwork;
  onOpen: (id: string) => void;
  /** One quiet line under the heading, such as counts that did not load. */
  notice?: ReactNode;
}>;
const pivots: Readonly<Record<string, string>> = {movie: 'movies', show: 'shows', artist: 'artists', album: 'albums', song: 'songs', book: 'books'};
/** The overview uses bounded library summaries, never reads their title catalogues. */
export function LibrariesScreen({libraries, artwork, onOpen, notice}: LibrariesBindings) {
  const compact = useShellWidth() < 600;
  return <section className={styles.libraries}><h1>{defaultI18n.t('next.browse.libraries')}</h1>{notice}<div className={styles.libraryCards} data-compact={compact}>
    {libraries.map((library, index) => {
      const model = {title: library.name, caption: library.summary.counts.map(count => ({id: count.kind, role: 'supplement' as const,
        text: defaultI18n.t(count.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: count.count, pivot: pivots[count.kind] ?? count.kind})}))};
      const props = {model, visible: true, onNavigate: () => onOpen(library.id), shape: library.kind === 'music' || library.kind === 'audiobook' ? 'square' as const : 'poster' as const,
        covers: library.summary.coverUrls.flatMap((path, order) => {const cover = artwork.foreground(path, 140 * devicePixelRatio, library.kind === 'music' || library.kind === 'audiobook' ? 1 : 2 / 3, {group: 'libraries', order: index * 3 + order});return cover ? [cover] : [];})};
      return compact ? <LibraryRow key={library.id} {...props}/> : <LibraryCard key={library.id} {...props}/>;
    })}
  </div></section>;
}
