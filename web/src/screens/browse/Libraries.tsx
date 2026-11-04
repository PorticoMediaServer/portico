import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {readLibrarySummaries} from '@core/library-summary.ts';
import {LibraryCard, LibraryRow} from '../../kit/cards';
import type {ShellArtwork} from '../../shell/artwork';
import {useShellWidth} from '../../shell/viewport';
import styles from './browse.module.css';
import {libraryCountText} from '@core/presentation/library-counts.ts';

export type LibrariesBindings = Readonly<{
  libraries: Awaited<ReturnType<typeof readLibrarySummaries>>;
  artwork: ShellArtwork;
  onOpen: (id: string) => void;
  /** One quiet line under the heading, such as counts that did not load. */
  notice?: ReactNode;
  /** phone-app 4d: Edit pins at the right of the Libraries title. */
  action?: ReactNode;
}>;
/** The overview uses bounded library summaries, never reads their title catalogues. */
export function LibrariesScreen({libraries, artwork, onOpen, notice, action}: LibrariesBindings) {
  const compact = useShellWidth() < 600;
  return <section className={styles.libraries}><div className={styles.librariesHead}><h1>{defaultI18n.t('next.browse.libraries')}</h1>{action}</div>{notice}<div className={styles.libraryCards} data-compact={compact}>
    {libraries.map((library, index) => {
      const model = {title: library.name, caption: library.summary.counts.map(count => ({id: count.kind, role: 'supplement' as const,
        text: libraryCountText(count)}))};
      const props = {model, visible: true, onNavigate: () => onOpen(library.id), shape: library.kind === 'music' || library.kind === 'audiobook' ? 'square' as const : 'poster' as const,
        covers: library.summary.coverUrls.flatMap((path, order) => {const cover = artwork.foreground(path, 140 * devicePixelRatio, library.kind === 'music' || library.kind === 'audiobook' ? 1 : 2 / 3, {group: 'libraries', order: index * 3 + order});return cover ? [cover] : [];})};
      return compact ? <LibraryRow key={library.id} {...props}/> : <LibraryCard key={library.id} {...props}/>;
    })}
  </div></section>;
}
