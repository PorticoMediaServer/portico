import {useEffect, useSyncExternalStore} from 'react';
import {preferenceValue} from '@core/preferences.ts';
import type {SearchEntry} from '@core/search.ts';
import type {TopSearchResult} from '@core/presentation/search-model.ts';
import {topResultKindLabel} from '@core/presentation/search-model.ts';
import {titlePageFacts} from '../detail/model';
import {episodeCode} from '@core/presentation/card-caption.ts';
import {AmbientArt} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {SearchResultHero, TitleFactsLine} from '../../kit/title/TitleParts';
import {Artwork} from '../../kit/artwork';
import {TitleActions} from '../detail/Detail';
import {t, environment} from '../detail/text';
import type {SearchBindings} from './bindings';
import detailStyles from '../detail/detail.module.css';
import {runScreenCommand} from '../detail/context';
import styles from './search.module.css';

export function TopResult({binding, result, fallbackArtwork, compact, onOpen}: {binding: SearchBindings; result: TopSearchResult; fallbackArtwork?: string; compact?: boolean; onOpen?: () => void}) {
  const entry = result.entry, title = binding.topTitle, show = binding.topShow;
  const titleState = useSyncExternalStore(title.service.subscribe, title.service.getSnapshot, title.service.getSnapshot);
  const showState = useSyncExternalStore(show.service.subscribe, show.service.getSnapshot, show.service.getSnapshot);
  const primary = useSyncExternalStore(show.primary.service.subscribe, show.primary.service.getSnapshot, show.primary.service.getSnapshot);
  const personal = useSyncExternalStore(show.personal.subscribe, show.personal.getSnapshot, show.personal.getSnapshot);
  const busy = useSyncExternalStore(show.personal.subscribe, show.personal.getBusy, show.personal.getBusy);
  const preferences = useSyncExternalStore(binding.context.preferences.subscribe, binding.context.preferences.getSnapshot, binding.context.preferences.getSnapshot);
  useEffect(() => {
    if ('libraryId' in entry && entry.libraryId) {
      const task = entry.kind === 'show' ? show.select({libraryId: entry.libraryId, showId: entry.id}) : entry.kind === 'movie' ? title.select({libraryId: entry.libraryId, itemId: entry.id}) : undefined;
      void task?.catch(binding.context.effects.reportError);
    }
    return () => { title.service.cancel(); show.service.cancel(); show.primary.service.cancel(); };
  }, [binding, entry, title, show]);
  const movie = entry.kind === 'movie' && titleState.data?.item.id === entry.id && titleState.phase === 'ready' ? titleState.data : undefined;
  const series = entry.kind === 'show' && showState.data?.show.id === entry.id && showState.phase === 'ready' ? showState.data : undefined;
  const media = 'libraryId' in entry ? entry : undefined;
  const source = media?.backdropUrl || series?.show.backdropUrl || movie?.item.backdropUrl || entry.posterUrl || fallbackArtwork;
  const facts = titlePageFacts(movie ? {year: movie.item.year, durationSeconds: movie.item.duration, genres: movie.metadata.genres.map(value => value.name), contentRating: movie.facts?.contentRating}
    : series ? {year: series.show.year, seasonCount: series.seasonTotalCount, genres: series.show.genres}
      : media ? {year: media.year, seasonCount: media.seasonCount, genres: media.genres, artist: media.artist?.name, author: media.author, durationSeconds: media.duration} : {}, environment);
  const kind = topResultKindLabel(result.group), allFacts = kind ? [{id: 'kind', role: 'context' as const, text: t(kind)}, ...facts] : facts;
  const next = series?.nextUp, code = next && episodeCode(next, t), action = primary.data?.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
  const showPlay = () => runScreenCommand(binding.context, () => { if (series && primary.data && action) { show.current(series); return show.primary.play(primary.data, action.id); } });
  // Frame 4's phone top result: one card with the title's own background, the artist's
  // circle when it has one, its real facts, and a Play only where a title really plays.
  if (compact) {
    const movieAction = movie?.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
    // The frame's artist card has its own Play. The binding's own captured authority decides
    // whether this exact answer can play the artist; the command re-checks it on the gesture.
    const artistPlay = entry.kind === 'artist' && binding.canPlayArtist(entry)
      ? () => runScreenCommand(binding.context, () => binding.playArtist(entry)) : undefined;
    const play = movie && movieAction ? () => runScreenCommand(binding.context, () => { if (movie && movieAction) { title.current(movie); return title.play(movie, movieAction.id); } })
      : series && action ? showPlay : artistPlay;
    const phoneTitle = media?.displayTitle ?? entry.title, artist = entry.kind === 'artist';
    const initials = entry.title.split(/\s+/).filter(Boolean).slice(0, 2).map(word => Array.from(word)[0]).join('');
    const background = source ? binding.context.artwork.foreground(source, 640, 3 / 2) : undefined;
    const ring = artist && source ? binding.context.artwork.foreground(source, 240, 1) : undefined;
    return <section className={styles.phoneTop} aria-label={t('search.topResult')}>
      {background && <Artwork className={styles.phoneTopBg} source={background} alt="" />}
      <button type="button" className={styles.phoneTopOpen} aria-label={phoneTitle} onClick={onOpen ?? (() => runScreenCommand(binding.context, () => binding.open(entry)))} />
      {artist && <span className={styles.phoneTopRing}>{ring ? <Artwork source={ring} alt="" /> : initials}</span>}
      <span className={styles.phoneTopCopy}><small>{t('search.topResult')}</small><b>{phoneTitle}</b><TitleFactsLine parts={allFacts} /></span>
      {play && <Button className={styles.phoneTopPlay} variant="primary" icon="i-play" iconOnly aria-label={t('next.controls.play')} onClick={play} />}
    </section>;
  }
  return <SearchResultHero title={media?.displayTitle ?? entry.title} eyebrow={t('search.topResult')} facts={<TitleFactsLine parts={allFacts} />} synopsis={movie?.item.overview ?? series?.show.overview ?? media?.overview}
    artwork={<AmbientArt treatment="title" source={preferenceValue<boolean>(preferences, 'appearance.showBackdrops') ? binding.context.artwork.ambient(source) : undefined} foreground={preferenceValue<boolean>(preferences, 'appearance.showBackdrops') ? binding.context.artwork.foreground(source, 1600, 16 / 9) : undefined} foregroundFrameStyle={{left: '30%', width: '70%', height: '100%'}} position="50% 6%" />}
    actions={movie ? <TitleActions binding={title} data={movie} topResult /> : series ? <div className={detailStyles.actions}>
      {action && <Button variant="primary" icon="i-play" onClick={showPlay}>{code ? t((next?.progressSeconds ?? 0) > 0 ? 'title.resumeEpisode' : 'title.playEpisode', {code}) : t('next.controls.play')}</Button>}
      {personal && <Button disabled={busy} icon={personal.watchlisted ? 'i-check' : 'i-plus'} aria-pressed={personal.watchlisted} onClick={() => { void show.setMyList(series, !personal.watchlisted).catch(binding.context.effects.reportError); }}>{t('next.controls.myList')}</Button>}
    </div> : <div className={detailStyles.actions}><Button onClick={() => binding.open(entry as SearchEntry)}>{t('next.layout.details')}</Button></div>} />;
}
