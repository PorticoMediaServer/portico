import {useEffect, useState} from 'react';
import {LibraryContentService, type ContentEntry, type LibraryContentSnapshot} from '@core/library-content.ts';
import type {FactPart} from '@core/presentation/kit-model';
import {durationText} from '@core/presentation/kit-model/text.ts';
import {defaultI18n} from '@i18n';
import {AmbientArt} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {TitleFrame, PanelBlock, PanelHeading, AboutTable, NumberedRow, OwnerCard, type TitleAction} from '../../kit/frames';
import {AddToCollection, RenameCollection, editCollection} from './CollectionOwner';
import {TitleFactsLine} from '../../kit/title/TitleParts';
import {useShellWidth} from '../../shell/viewport';
import {t, environment as env} from './text';
import {titlePageFacts} from './model';
import {runScreenCommand, screenLifetime, type ScreenContext} from './context';
import {ScreenProblem} from './ScreenProblem';
import {TitleRow} from './Detail';
import {entriesFromItems, readRelatedItems} from '@core/items.ts';
import styles from './collection-owner.module.css';

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
  /** The owner's edits: which dialog is open, and the films' order while Change order is on (their ids). */
  const [editing, setEditing] = useState<'rename' | 'add'>(), [ordering, setOrdering] = useState<readonly string[]>(), [saving, setSaving] = useState(false);
  useEffect(() => {
    const life = screenLifetime(context), service = new LibraryContentService({queries: context.queries, scope: context.scope});
    const publish = () => {const snapshot = service.getSnapshot(); if (life.current()) setPage({context, libraryId, collectionId, life, service, snapshot});};
    const off = service.subscribe(publish);
    publish();
    void service.select({libraryId, view: 'collection', entityId: collectionId}).catch(error => {if (life.current()) context.effects.reportError(error);});
    return () => {life.dispose(); off(); service.dispose();};
  }, [context, libraryId, collectionId, retry]);
  // desktop-detail f8: "More like The Lord of the Rings" under the page, the server's own ranking for this collection.
  const [related, setRelated] = useState<Readonly<{collectionId: string; entries: readonly ContentEntry[]}>>();
  useEffect(() => {
    const life = screenLifetime(context);
    void readRelatedItems(life.api, collectionId, life.signal(), {relation: 'more_like_this', limit: 15}).then(answer => {
      const group = answer.groups.find(group => group.relation === 'more_like_this');
      if (life.current()) setRelated({collectionId, entries: entriesFromItems(group?.items ?? []).filter(entry => entry.id !== collectionId)});
    }, () => {/* the row is a supplement: a failed read leaves it out */});
    return () => life.dispose();
  }, [context, collectionId, retry]);
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
  // desktop-detail f8: the owner's card, for the local owner who manages this server.
  const viewer = context.identity?.viewer, owner = viewer?.role === 'owner' && viewer.authority === 'local' && context.identity?.capabilities?.manageServer === true;
  // An edit lands, then the page reads the collection again in place (its name, its films, their order).
  const edit = async (change: Parameters<typeof editCollection>[2]) => {
    if (!current()) throw new DOMException('Collection page changed', 'AbortError');
    await editCollection(life.api, collectionId, change, life.signal());
    if (life.current()) await service.select({libraryId, view: 'collection', entityId: collectionId});
  };
  const report = (error: unknown) => {if (life.current()) context.effects.reportError(error);};
  const orderIds = ordering?.filter(id => films.some(film => film.id === id));
  const shown = orderIds ? orderIds.map(id => films.find(film => film.id === id)!) : films;
  const move = (index: number, by: -1 | 1) => setOrdering(before => {
    const ids = [...(before ?? films.map(film => film.id))], to = index + by;
    if (to < 0 || to >= ids.length) return before;
    [ids[index], ids[to]] = [ids[to]!, ids[index]!]; return ids;
  });
  const hasMore = snapshot.appended.some(section => Boolean(section.nextCursor));
  const canReorder = !hasMore && films.length > 1;
  const saveOrder = () => {
    if (!orderIds || saving || !canReorder || !current()) return;
    setSaving(true);
    void edit({order: orderIds}).then(() => {if (life.current()) setOrdering(undefined);}, report).finally(() => setSaving(false));
  };
  const playable = (film: ContentEntry) => !!film.playback && film.available !== false && !film.missing;
  const started = (film: ContentEntry) => !film.watched && (film.progressSeconds ?? 0) > 0;
  // Up next: the film in progress, or else the first one not yet watched once an earlier one has been (desktop-detail f8).
  const next = films.find(started) ?? (films.some(film => film.watched) ? films.find(film => !film.watched) : undefined);
  const sectionTotal = snapshot.appended.reduce((sum, section) => sum + (section.totalCount ?? section.entries.length), 0);
  const count = collection.count ?? (hasMore && sectionTotal > films.length ? sectionTotal : films.length);
  const watched = collection.watchedCount ?? (hasMore ? 0 : films.filter(film => film.watched).length);
  const years = films.flatMap(film => film.year && film.year > 0 ? [film.year] : []);
  const first = collection.yearMin ?? (years.length ? Math.min(...years) : undefined);
  const last = collection.yearMax ?? (years.length ? Math.max(...years) : undefined);
  const yearsText = first === undefined || last === undefined ? undefined : first === last ? year(first) : t('next.detail.seasonYears', {first, last});
  const runtime = films.reduce((total, film) => total + (film.duration ?? 0), 0);
  const facts: FactPart[] = [
    {id: 'films', role: 'identity', text: t('next.browse.filmCount', {count})},
    ...(yearsText ? [{id: 'years', role: 'supplement' as const, text: yearsText}] : []),
    ...(!hasMore && runtime > 0 ? [{id: 'duration', role: 'supplement' as const, text: durationText(runtime, t) ?? ''}] : []),
    ...(watched > 0 && count > 0 ? [{id: 'watched', role: 'status' as const, text: t('title.seasonProgress', {watched, total: count})}] : []),
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
  return <><TitleFrame back={back}
    art={backdrop ? <AmbientArt treatment="title" source={context.artwork.ambient(backdrop)} foreground={context.artwork.foreground(backdrop, phone ? 800 : 1920, 16 / 9)} /> : undefined}
    kicker={{icon: 'i-layers', label: t('next.detail.collection')}} title={collection.title} metadata={<TitleFactsLine parts={facts} />}
    primary={playAll ? {icon: 'i-play', label: t('next.cards.playAll'), disabled: !anyPlayable, onPress: () => runScreenCommand(context, () => playAll(collectionId, false, life.signal(), current))} : undefined}
    actions={actions} synopsis={collection.overview}
    aside={<div className={styles.aside}><PanelHeading title={t('next.show.about')} /><AboutTable rows={[
      {label: t('next.detail.collectionFilms'), values: [{text: t('next.collection.filmsOnServer', {count})}]},
      ...(yearsText ? [{label: t('next.detail.collectionYears'), values: [{text: yearsText}]}] : []),
    ]} />
      {owner && <OwnerCard title={t('next.collection.ownerTitle')} mark={t('next.overlays.ownerOnly')} body={t('next.collection.ownerBody')} actions={[
        {icon: 'i-edit', label: t('next.collection.rename'), onPress: () => {if (current()) setEditing('rename');}},
        {icon: 'i-plus', label: t('next.collection.addFilm'), onPress: () => {if (current()) setEditing('add');}},
        ...(canReorder ? [{icon: 'i-sort' as const, label: t('next.collection.changeOrder'), onPress: () => {if (current() && canReorder) setOrdering(films.map(film => film.id));}}] : []),
      ]} />}
    </div>}>
    <PanelBlock label={t('next.detail.filmsInOrder')}>
      <PanelHeading title={t('next.detail.filmsInOrder')} trailing={orderIds && <>
        <Button size="small" variant="ghost" disabled={saving} onClick={() => setOrdering(undefined)}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={saving} onClick={saveOrder}>{t('next.collection.saveOrder')}</Button></>} />
      {films.length === 0 && snapshot.phase === 'ready' && <p>{t('next.collection.empty')}</p>}
      {shown.map((film, index) => {
        const progress = started(film) && film.duration ? Math.min(1, (film.progressSeconds ?? 0) / film.duration) : undefined;
        const status = film.watched ? {label: t('next.detail.watched'), icon: 'i-check' as const} : film === next ? {label: t('title.nextUp'), accent: true} : {label: t('title.notStarted')};
        const label = film.watched ? t('next.detail.playAgain') : started(film) ? t('next.controls.resume') : t('next.controls.play');
        return <NumberedRow key={film.id} number={index + 1} title={film.title} onPress={() => runScreenCommand(context, () => {if (current()) onOpen(film);})}
          artwork={context.artwork.foreground(film.posterUrl, 200, 2 / 3)}
          facts={<TitleFactsLine parts={titlePageFacts({year: film.year, durationSeconds: film.duration, contentRating: film.contentRating}, env)} />}
          synopsis={film.overview} status={status} progress={progress}
          action={orderIds ? <span className={styles.order}>
              <Button size="small" variant="glass" iconOnly icon="i-up" aria-label={t('next.collection.moveUp', {title: film.title})} disabled={index === 0 || saving} onClick={() => move(index, -1)}/>
              <Button size="small" variant="glass" iconOnly icon="i-down" aria-label={t('next.collection.moveDown', {title: film.title})} disabled={index === shown.length - 1 || saving} onClick={() => move(index, 1)}/></span>
            : <Button size="compact" icon="i-play" disabled={!playable(film)} onClick={() => play(film)}>{label}</Button>} />;
      })}
      {snapshot.appended.filter(section => section.nextCursor).map(section => <Button key={section.id} variant="glass" disabled={snapshot.phase !== 'ready'}
        onClick={() => runScreenCommand(context, () => {if (current()) return service.next(section.id);})}>{t('next.settings.loadMore')}</Button>)}
    </PanelBlock>
    {related?.collectionId === collectionId && related.entries.length > 0 && !orderIds
      && <TitleRow heading={t('home.row.moreLike', {title: collection.title})} entries={related.entries} binding={{context}} guard={() => {if (!current()) throw new DOMException('Collection page changed', 'AbortError');}} />}
  </TitleFrame>
    {owner && <RenameCollection open={editing === 'rename'} name={collection.title} onOpenChange={open => setEditing(open ? 'rename' : undefined)}
      onSave={name => edit({title: name}).then(() => {if (life.current()) setEditing(undefined);}, report)} />}
    {owner && <AddToCollection open={editing === 'add'} api={life.api} libraryId={libraryId} held={new Set(films.map(film => film.id))} artwork={context.artwork}
      onOpenChange={open => setEditing(open ? 'add' : undefined)} onAdd={id => edit({add: [id]}).catch(error => {report(error); throw error;})} />}
  </>;
}
