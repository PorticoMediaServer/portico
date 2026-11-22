import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {ScrollView, View} from 'react-native';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {episodeLabel, type TextEnvironment} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useTVPanelInteractive} from '../../../kit/overlays/TVPanelMotion';
import {useTheme} from '../../../kit/theme';
import type {NativeTitleController, NativeTitlePageLease} from '../../../screens/watch/title-controller';

/** collections 37/38: the episode list inside the player (sideways) and the season
 * sheet the upright tab shares. Only the workspace's own seasons and episodes are
 * drawn; the playing episode carries its level mark, progress and Playing label, and
 * the next one says Up next. Choosing another episode stays in the same playback. */
export type NativeWatchEpisodesProps = Readonly<{
  controller: NativeTitleController;
  currentId: string;
  playing: boolean;
  text: TextEnvironment;
  artwork: (path?: string) => import('../../../kit/artwork').ArtworkSource | undefined;
  onPlay: (entry: ContentEntry) => void;
  onClose: () => void;
  /** foldable4/5: the episodes that follow the playing one, with no panel chrome. */
  pane?: boolean;
}>

const numbering = (entry: ContentEntry) => entry.absoluteNumber !== undefined ? {mode: 'absolute' as const, number: entry.absoluteNumber}
  : entry.dateBased && entry.airDate ? {mode: 'date' as const, day: entry.airDate}
    : entry.seasonNumber === 0 ? {mode: 'special' as const} : {mode: 'seasonal' as const, season: entry.seasonNumber, episode: entry.episodeNumber};

/** The episode panel's reads and actions: the title controller's snapshot, the season sheet's lease, the TV entry focus,
 * paging, and each episode's words. Moved from screens/watch/EpisodePanel; `WatchEpisodesView` draws what this returns. */
export function useWatchEpisodes({controller, currentId, playing, text, artwork, onPlay, onClose, pane = false}: NativeWatchEpisodesProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [seasonsSheet, setSeasonsSheet] = useState<NativeTitlePageLease>();
  const scroll = useRef<ScrollView>(null), first = useRef<View>(null), seasonOpener = useRef<View>(null), entered = useRef(false);
  const interactive = useTVPanelInteractive();
  useEffect(() => {void controller.open().catch(() => {});}, [controller]);
  const workspace = snapshot.workspace, data = workspace?.data;
  const seasons = snapshot.seasons, episodes = snapshot.episodes;
  const selected = data?.selected.seasonId ?? null;
  const season = seasons.find(value => value.id === selected) ?? seasons[0];
  const nextUp = data?.nextUp;
  const count = season?.episodeCount;
  const seasonLabel = season ? (season.number === 0 ? t('next.kit.specials') : t('next.kit.season', {number: season.number})) : t('title.seasons');
  const loading = workspace?.phase === 'loading' || workspace?.phase === 'idle';
  // foldable4/5 draw the episodes that follow the playing one. The controller
  // pages a bounded list, so an absent current episode means the order relative
  // to it is unknown: no rows are claimed as following, and no earlier row is
  // ever dressed up as one.
  const playingIndex = episodes.findIndex(entry => entry.id === currentId);
  const following = pane ? (playingIndex >= 0 ? episodes.slice(playingIndex + 1) : []) : episodes;
  const initialId = episodes[playingIndex]?.id ?? episodes[0]?.id;
  useEffect(() => {entered.current = false;}, [controller]);
  useEffect(() => {
    if (!theme.isTV || pane || !interactive || seasonsSheet || entered.current || !initialId) return;
    if (first.current?.requestTVFocus) {first.current.requestTVFocus(); entered.current = true;}
  }, [theme.isTV, pane, interactive, seasonsSheet, initialId]);
  // The sheet and the switch belong to the page and session this render shows.
  const lease = controller.pageLease();
  // The opener closes over this render's lease; a stale opener cannot recapture.
  const openSeasons = () => {if (lease) setSeasonsSheet(lease);};
  const closeSeasons = () => setSeasonsSheet(undefined);
  /** One episode as a row or tile reads it: Playing, Up next, the label and length, and progress unless it is playing. */
  const episode = (entry: ContentEntry) => {
    const isPlaying = entry.id === currentId;
    const isNext = !isPlaying && nextUp?.id === entry.id;
    return {entry, isPlaying, isNext, title: entry.displayTitle ?? entry.title,
      label: episodeLabel(numbering(entry), 'player', text), length: entry.duration ? durationText(entry.duration, text.t) : undefined,
      progress: !isPlaying && entry.progressSeconds && entry.duration ? Math.max(0, Math.min(1, entry.progressSeconds / entry.duration)) : undefined,
      artwork: artwork(entry.stillUrl ?? data?.show?.backdropUrl), stillMissing: !entry.stillUrl,
      play: () => {if (!isPlaying) onPlay(entry);}};
  };
  /** The list or band keeps going as it nears its end. */
  const nearEnd = (remaining: number, viewport: number) => {if (remaining < viewport) void controller.moreEpisodes().catch(() => {});};
  return {pane, playing, onClose, episodes, following, initialId, loading, count, seasonLabel, seasons, selected, interactive,
    total: data?.seasonTotalCount ?? seasons.length, hideWatched: snapshot.place.hideWatched,
    scroll, first, seasonOpener, seasonsOpen: !!seasonsSheet, openSeasons, closeSeasons, episode, nearEnd,
    selectSeason: (id: string) => {const sheet = seasonsSheet; closeSeasons(); if (sheet) void controller.season(id, sheet).catch(() => {});},
    moreSeasons: () => {void controller.moreSeasons().catch(() => {});},
    /** The TV season sheet's switch acts on the sheet's own lease; the panel's switch on this render's lease. */
    sheetHideWatched: (value: boolean) => {if (seasonsSheet) void controller.hideWatched(value, seasonsSheet).catch(() => {});},
    panelHideWatched: (value: boolean) => {if (lease) void controller.hideWatched(value, lease).catch(() => {});}};
}
