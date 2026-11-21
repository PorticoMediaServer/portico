import {useEffect, useState} from 'react';
import {LibraryContentService, type ContentEntry, type LibraryContentSnapshot} from '@core/library-content.ts';
import type {FactPart} from '@core/presentation/kit-model';
import {durationText} from '@core/presentation/kit-model/text.ts';
import {defaultI18n} from '@i18n';
import {AmbientArt} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {TitleFrame, PanelBlock, PanelHeading, AboutTable, NumberedRow, type TitleAction} from '../../kit/frames';
import {TitleFactsLine} from '../../kit/title/TitleParts';
import {useShellWidth} from '../../shell/viewport';
import {t, environment as env} from './text';
import {titlePageFacts} from './model';
import {runScreenCommand, screenLifetime, type ScreenContext} from './context';
import {ScreenProblem} from './ScreenProblem';

export type CollectionScreenProps = Readonly<{context: ScreenContext; libraryId: string; collectionId: string;
  /** Opens one film's own page. */
  onOpen: (entry: ContentEntry) => void; onBack?: () => void}>;
type Page = Readonly<{context: ScreenContext; libraryId: string; collectionId: string; life: ReturnType<typeof screenLifetime>; service: LibraryContentService; snapshot: LibraryContentSnapshot}>;

const year = (value: number) => defaultI18n.number(value, {useGrouping: false});

/** A collection (desktop-detail f8, phone-detail d3 frame 8) on the title frame: Collection over its name, the facts,
 * Play all and Shuffle, the films in order with each one's state and action, and About at the side. The films are the
 * collection's own read, in the server's order; a film opens its page. */
export function CollectionScreen({context, libraryId, collectionId, onOpen, onBack}: CollectionScreenProps) {
  const phone = useShellWidth() < 1024;
  const [page, setPage] = useState<Page>(), [retry, setRetry] = useState(0);
  useEffect(() => {
    const life = screenLifetime(context), service = new LibraryContentService({queries: context.queries, scope: context.scope});
    const publish = () => {const snapshot = service.getSnapshot(); if (life.current()) setPage({context, libraryId, collectionId, life, service, snapshot});};
    const off = service.subscribe(publish);
    publish();
    void service.select({libraryId, view: 'collection', entityId: collectionId}).catch(error => {if (life.current()) context.effects.reportError(error);});
    return () => {life.dispose(); off(); service.dispose();};
  }, [context, libraryId, collectionId, retry]);
  const active = page?.context === context && page.libraryId === libraryId && page.collectionId === collectionId && page.life.current() ? page : undefined;
  const snapshot = active?.snapshot, projection = snapshot?.projection, collection = projection?.entity;
  const back = onBack ? {label: t('next.states.back'), onPress: () => runScreenCommand(context, onBack)} : undefined;
  if (!active || !snapshot || !projection || !collection || collection.id !== collectionId || collection.kind !== 'collection') {
    if (snapshot?.phase === 'error' || snapshot?.phase === 'refresh-required') return <ScreenProblem context={context} error={snapshot.error} retry={() => setRetry(value => value + 1)}/>;
    return <TitleFrame back={back} title="" state={{kind: 'loading'}} />;
  }
  const {life, service} = active;
  const current = () => life.current() && service.getSnapshot().phase === 'ready' && service.getSnapshot().projection === projection;
  const films = snapshot.appended.flatMap(section => section.entries).filter(entry => entry.kind === 'movie');
  const playable = (film: ContentEntry) => !!film.playback && film.available !== false && !film.missing;
  const started = (film: ContentEntry) => !film.watched && (film.progressSeconds ?? 0) > 0;
  // Up next: the film in progress, or else the first one not yet watched once an earlier one has been (desktop-detail f8).
  const next = films.find(started) ?? (films.some(film => film.watched) ? films.find(film => !film.watched) : undefined);
  const watched = films.filter(film => film.watched).length;
  const years = films.flatMap(film => film.year && film.year > 0 ? [film.year] : []);
  const first = years.length ? Math.min(...years) : undefined, last = years.length ? Math.max(...years) : undefined;
  const yearsText = first === undefined || last === undefined ? undefined : first === last ? year(first) : t('next.detail.seasonYears', {first, last});
  const runtime = films.reduce((total, film) => total + (film.duration ?? 0), 0);
  const count = collection.count ?? films.length;
  const facts: FactPart[] = [
    {id: 'films', role: 'identity', text: t('next.browse.filmCount', {count})},
    ...(yearsText ? [{id: 'years', role: 'supplement' as const, text: yearsText}] : []),
    ...(runtime > 0 ? [{id: 'duration', role: 'supplement' as const, text: durationText(runtime, t) ?? ''}] : []),
    ...(watched > 0 ? [{id: 'watched', role: 'status' as const, text: t('title.seasonProgress', {watched, total: films.length})}] : []),
  ];
  const play = (film: ContentEntry) => runScreenCommand(context, () => {
    if (!current() || !playable(film) || !films.includes(film)) return;
    return context.effects.play({libraryId, itemId: film.playback!.itemId, startSeconds: film.watched ? 0 : film.playback!.startSeconds ?? 0,
      from: {container: {kind: 'collection', id: collectionId}}}, life.signal(), current);
  });
  const playAll = context.effects.playCollection, anyPlayable = films.some(playable);
  const actions: TitleAction[] = playAll ? [{icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, disabled: !anyPlayable,
    onPress: () => runScreenCommand(context, () => playAll(collectionId, true, life.signal(), current))}] : [];
  const backdrop = collection.backdropUrl ?? films.find(film => film.backdropUrl)?.backdropUrl;
  return <TitleFrame back={back}
    art={backdrop ? <AmbientArt treatment="title" source={context.artwork.ambient(backdrop)} foreground={context.artwork.foreground(backdrop, phone ? 800 : 1920, 16 / 9)} /> : undefined}
    kicker={{icon: 'i-layers', label: t('next.detail.collection')}} title={collection.title} metadata={<TitleFactsLine parts={facts} />}
    primary={playAll ? {icon: 'i-play', label: t('next.cards.playAll'), disabled: !anyPlayable, onPress: () => runScreenCommand(context, () => playAll(collectionId, false, life.signal(), current))} : undefined}
    actions={actions} synopsis={collection.overview}
    aside={<div><PanelHeading title={t('next.show.about')} /><AboutTable rows={[
      {label: t('next.detail.collectionFilms'), values: [{text: t('next.collection.filmsOnServer', {count})}]},
      ...(yearsText ? [{label: t('next.detail.collectionYears'), values: [{text: yearsText}]}] : []),
    ]} /></div>}>
    <PanelBlock label={t('next.detail.filmsInOrder')}>
      <PanelHeading title={t('next.detail.filmsInOrder')} />
      {films.length === 0 && snapshot.phase === 'ready' && <p>{t('next.collection.empty')}</p>}
      {films.map((film, index) => {
        const progress = started(film) && film.duration ? Math.min(1, (film.progressSeconds ?? 0) / film.duration) : undefined;
        const status = film.watched ? {label: t('next.detail.watched'), icon: 'i-check' as const} : film === next ? {label: t('title.nextUp'), accent: true} : {label: t('title.notStarted')};
        const label = film.watched ? t('next.detail.playAgain') : started(film) ? t('next.controls.resume') : t('next.controls.play');
        return <NumberedRow key={film.id} number={index + 1} title={film.title} onPress={() => runScreenCommand(context, () => {if (current()) onOpen(film);})}
          artwork={context.artwork.foreground(film.posterUrl, 200, 2 / 3)}
          facts={<TitleFactsLine parts={titlePageFacts({year: film.year, durationSeconds: film.duration, contentRating: film.contentRating}, env)} />}
          synopsis={film.overview} status={status} progress={progress}
          action={<Button size="compact" icon="i-play" disabled={!playable(film)} onClick={() => play(film)}>{label}</Button>} />;
      })}
      {snapshot.appended.filter(section => section.nextCursor).map(section => <Button key={section.id} variant="glass" disabled={snapshot.phase !== 'ready'}
        onClick={() => runScreenCommand(context, () => {if (current()) return service.next(section.id);})}>{t('next.settings.loadMore')}</Button>)}
    </PanelBlock>
  </TitleFrame>;
}
