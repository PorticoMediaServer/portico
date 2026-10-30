import {useSyncExternalStore} from 'react';
import {presentError} from '@core/presentation/errors.ts';
import {serviceI18n} from '@core/presentation/service-text.ts';
import type {ShellArtwork} from '../../shell/artwork';
import {Button, TextField} from '../../kit/controls';
import {Artwork} from '../../kit/artwork';
import {SearchEmpty} from '../../kit/states/SearchEmpty';
import {Skeleton, StateView} from '../../kit/states';
import {useShellWidth} from '../../shell/viewport';
import {t} from '../detail/text';
import type {ReadingSearchController} from './reading-search-controller';
import searchStyles from './search.module.css';
import readerStyles from '../../kit/reader/search.module.css';
import styles from './reading-search.module.css';

/** Root54: a separate indexed-only excerpt page, using Search's field/shell
 * and the reader's escaped match-row treatment without claiming a location. */
export function ReadingSearchScreen({controller, artwork}: {controller: ReadingSearchController; artwork: ShellArtwork}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot), compact = useShellWidth() < 600;
  const page = snapshot.phase === 'ready' ? snapshot.page : undefined;
  const problem = snapshot.error ? presentError(snapshot.error, 'search', {i18n: serviceI18n()}) : undefined;
  return <section className={searchStyles.search}>
    <div className={searchStyles.wash} aria-hidden="true"/>
    {compact && <h1 className={searchStyles.phoneSearchTitle}>{t('web.search.title')}</h1>}
    <form className={searchStyles.field} role="search" onSubmit={event => {event.preventDefault();controller.refresh();}}>
      <TextField autoFocus variant={compact ? 'search' : 'large-search'} type="search" autoComplete="off" spellCheck={false}
        value={snapshot.query} aria-label={t('next.reader.search')} placeholder={t('next.reader.search')}
        onChange={event => controller.select(event.currentTarget.value)}
        trailing={snapshot.query ? <Button variant="icon" icon="i-x" iconOnly aria-label={t('web.search.clear')} onClick={() => controller.select('')}/> : undefined}/>
    </form>
    <p className={styles.scope}>{t('next.reader.indexedSearchScope')}</p>
    <div aria-busy={snapshot.phase === 'loading'}>
      {snapshot.phase === 'loading' && <Skeleton height={120}/>}
      {problem && !problem.silent && <StateView title={problem.title} body={problem.body}
        primary={problem.action === 'try-again' || problem.action === 'refresh'
          ? {id: 'refresh', label: problem.actionLabel ?? t('next.states.tryAgain'), onPress: controller.refresh} : undefined}/>}
      {page && !page.hits.length && <SearchEmpty title={t('next.reader.noIndexedMatches')}/>}
      {page && page.hits.length > 0 && <ul className={styles.hits}>{page.hits.map((hit, index) => {
        const cover = hit.coverUrl ? artwork.foreground(hit.coverUrl, 96, 2 / 3) : undefined;
        return <li key={index}><button type="button" className={`${readerStyles.hit} ${styles.hit}`} disabled={!controller.contains(page, hit)} onClick={() => controller.open(page, hit)}>
          {cover && <Artwork className={styles.cover} source={cover} alt=""/>}
          <span className={styles.copy}>{hit.title !== undefined && <b className={styles.title}>{hit.title}</b>}
            <p>{hit.excerpt.slice(0, hit.matchStart)}<mark>{hit.excerpt.slice(hit.matchStart, hit.matchStart + hit.matchLength)}</mark>{hit.excerpt.slice(hit.matchStart + hit.matchLength)}</p>
          </span>
        </button></li>;
      })}</ul>}
      {page?.next && <Button disabled={snapshot.opening} onClick={() => controller.next(page)}>{t('web.pager.next')}</Button>}
    </div>
  </section>;
}
