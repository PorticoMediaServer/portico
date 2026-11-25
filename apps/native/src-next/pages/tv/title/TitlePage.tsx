import React, {useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt} from '../../../kit/artwork';
import {StateRow, TitleDetails, TitleFrame, TitleSection, TVEpisodeShelf, TVSeasonList, type EpisodeBandData} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {Menu} from '../../../kit/overlays';
import type {NativeTitleScreenProps} from '../../../pages/shared/player/useTitleScreen';
import {useTitle, type TitleView, type TVSeason} from '../../shared/title/useTitle';

const t = defaultI18n.t;
const hold = (label: string) => ({key: t('next.layout.holdOk'), label});

/** A film's or a show's title page on a TV (tv-detail frames 1 to 5); a season's and an episode's route open their show's
 * page on that season: `TitleFrame` filled from the title controller. Every
 * section is drawn and focus walks down them: a show's Episodes (the season column and the episodes with the band that
 * describes what holds focus), More like this's rows, then Details; the frame folds the hero as focus leaves the buttons.
 * A TV keeps nothing for offline, so the page offers no Download. */
export function TitlePage(props: NativeTitleScreenProps) {
  const view = useTitle({...props, download: undefined});
  // The band describes the season in focus or the episode in focus (detail-tv-5a, 5b); a season's options are its Hold OK menu.
  const [band, setBand] = useState<Readonly<{season: string} | {episode: string}>>();
  const [menu, setMenu] = useState<TVSeason>();
  if (view.retired) return null;
  const {about, people} = view.details, tv = view.episodes?.tv;
  // An episode's own route (a Search hit, Continue Watching) lands on that episode once its season lists it: focus starts on
  // its tile, so the band describes it and OK plays it (the touch page opens it in place; desk, 19 Nov). Until then, Play.
  const landing = view.entry.kind === 'episode' && tv?.tiles.some(tile => tile.key === view.entry.id) ? view.entry.id : undefined;
  return <>
    <TitleFrame testID="watch-title-screen" startFocus={landing} back={{label: t('action.back'), onPress: props.back, testID: 'watch-title-back'}}
      art={<AmbientArt treatment="title" source={view.art.source} foreground={view.art.foreground}/>}
      kicker={view.kicker} title={view.title} metadata={<HeroMetadata facts={view.facts}/>}
      // tv-detail 1 and 2 name it My List whether it is on or off; on, it carries the accent tick.
      primary={view.primary} actions={view.actions.map(action => action.testID === 'watch-title-my-list' ? {...action, label: t('next.controls.myList')} : action)}
      tagline={view.tagline} synopsis={view.synopsis} credits={view.credits} tabs={view.tabs} state={view.state}
      seasons={tv && {tab: 'episodes',
        column: <TVSeasonList focusKey={props.visit ? `${view.entry.id}.${props.visit}` : undefined} hint={hold(t('next.watch.seasonOptions'))} onEnd={tv.moreSeasons} items={tv.seasons.map(season => ({id: season.id, label: season.label,
          detail: season.progress ?? season.count, progress: season.fraction ?? (season.complete ? 1 : undefined), current: season.current,
          onFocus: () => {setBand({season: season.id}); tv.focusEpisode(undefined); tv.select(season.id);}, onLongPress: season.group ? undefined : () => setMenu(season)}))}/>,
        episodes: <TVEpisodeShelf focusKey={props.visit ? `${view.entry.id}.${props.visit}` : undefined} entry={landing ?? tv.nextUp} empty={t('title.noEpisodes')} onEnd={view.onEnd} band={bandData(view, band)}
          items={tv.tiles.map(tile => ({...tile, onFocus: () => {setBand({episode: tile.key}); tv.focusEpisode(tile.key);}, onPlay: () => tv.play(tile.key)}))}/>}}>
      {view.related.map(row => <TitleSection key={row.testID ?? row.title} tab="related"><PosterRow {...row}/></TitleSection>)}
      {/* detail-tv-4: About carries the whole synopsis and the tagline beside the facts. */}
      <TitleSection tab="details"><TitleDetails {...view.details}
        about={about || view.synopsis ? {title: about?.title ?? props.aboutLabel, rows: about?.rows ?? [], body: view.synopsis, note: view.tagline} : undefined} people={people && {...people, note: t('next.detail.castThenCrew'),
          // detail-tv-4 draws no See all: on TV the row holds everyone read so far and pages as focus reaches its end.
          groups: view.people.groups, seeAll: undefined, more: view.people.more && !view.people.more.loading ? view.people.more.onPress : undefined}}/></TitleSection>
    </TitleFrame>
    {tv && <Menu testID="watch-title-season-menu" open={!!menu} onOpenChange={open => {if (!open) setMenu(undefined);}} title={menu?.label ?? ''} items={menu ? [
      {id: 'play-season', icon: 'i-play', label: t('title.playSeason'), onSelect: () => tv.playSeason(menu.id)},
      {id: 'mark-season', icon: 'i-check', label: t(menu.complete ? 'title.markSeasonUnwatched' : 'title.markSeasonWatched'), onSelect: () => tv.markSeason(menu.id, !menu.complete)},
    ] : []}/>}
  </>;
}

/** The band's words: the season in focus (its count, year and overview), or the episode in focus as the controller has
 * opened it (its plate: code and facts, synopsis, where it was left, guest stars and credits). Before any focus, the
 * current season. */
function bandData(view: TitleView, band: Readonly<{season: string} | {episode: string}> | undefined): EpisodeBandData | undefined {
  const tv = view.episodes?.tv, opened = view.episodes?.list.opened;
  if (band && 'episode' in band) {
    if (!opened || opened.key !== band.episode) return undefined;
    const credits = opened.credits ?? [];
    const guests = credits.find(credit => credit.kind === 'guests' || (!credit.kind && credit.label === t('next.watch.guestStars')));
    const lines = credits.filter(credit => credit !== guests);
    const first = guests?.people.slice(0, 3) ?? [];
    return {kind: 'episode', title: opened.title, facts: [...(opened.code ? [{id: 'code', text: opened.code, role: 'identity' as const}] : []), ...opened.facts], synopsis: opened.synopsis,
      state: opened.primary?.progress !== undefined ? {label: [opened.primary.label, opened.primary.remaining].filter(Boolean).join(', '), progress: opened.primary.progress} : undefined,
      guests: guests ? {label: guests.label, people: first, more: guests.people.length > first.length ? t('next.watch.andMore', {count: guests.people.length - first.length}) : undefined} : undefined,
      lines: lines.map(line => ({label: line.label, names: defaultI18n.list(line.people.map(person => person.name))})), hint: hold(t('next.watch.episodeOptions')),
      error: opened.state ? <StateRow {...opened.state}/> : undefined};
  }
  const season = tv?.seasons.find(value => band && 'season' in band ? value.id === band.season : value.current);
  return season && {kind: 'season', title: season.label, facts: [season.year, season.count].filter((fact): fact is string => !!fact), overview: season.overview};
}
