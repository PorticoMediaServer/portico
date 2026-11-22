import {defaultI18n} from '../../../../../../packages/i18n/src';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import type {EntryActionId} from '../../../../../../packages/client-core/src/presentation/entry-actions';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {MenuAction} from '../../../kit/overlays';
import {titleMenuItems} from '../../../kit/overlays/title-menu';
import type {IconName} from '../../../kit/Icon';
import {nativeWatchMenuGroups} from '../../../screens/watch/menu-model';
import type {NativeWatchOverlay, NativeWatchPresenters} from '../../../screens/watch/presenters';

export const watchEntryName = (entry: NativeWatchOverlay['capture']['entry']) => entry.displayTitle ?? entry.title;

/** The player's title menu: the one shared title-row model and renderer (also Home's More menu), plus only what belongs to
 * a screen's own surface (Start radio, Play a different version…, File info, Select, Disc titles and the caller's extras;
 * select.html 17 and 23, tablet.html 9), and the TV side sheet's context facts. Moved from WatchOverlays as it was. */
export function watchTitleMenu(value: NativeWatchOverlay, presenters: NativeWatchPresenters, runtime: NativeShellRuntime, tv: boolean) {
  const t = defaultI18n.t;
  const entry = value.capture.entry, art = runtime.artwork(entry.posterUrl), subject = watchEntryName(entry);
  const menu = value.actions?.entry;
  const kindKey = entry.kind === 'movie' ? 'search.kind.movie' : entry.kind === 'show' ? 'search.kind.show' : entry.kind === 'artist' ? 'search.kind.artist' : entry.kind === 'album' ? 'search.kind.album' : undefined;
  const together = presenters.togetherActions(value);
  const groups = nativeWatchMenuGroups({entry, detail: value.detail, menu, together});
  const row = (id: string, label: string, icon: IconName, onSelect: () => void, extra?: Partial<MenuAction>): MenuAction => ({id, label, icon, onSelect, ...extra});
  const select = value.actions?.select ? row('select', t('next.selection.select'), 'i-check', () => presenters.selectEntry(value)) : undefined;
  const version = value.actions?.differentVersion ? row('different-version', t('next.overlays.differentVersion'), 'i-film', () => {void presenters.differentVersion(value);}) : undefined;
  const fileInfo = row('file-info', t('next.overlays.fileInfo'), 'i-info', () => {void presenters.fileInfo(value);});
  const season = tv && menu?.goToSeason ? row('go-to-season', t('next.overlays.native.goToSeason'), 'i-list', () => presenters.goToSeason(value)) : undefined;
  const artist = tv && menu?.goToArtist ? row('go-to-artist', t('next.watch.goToArtist'), 'i-person', () => presenters.goToArtist(value)) : undefined;
  const discs = value.actions?.discTitles ? row('disc-titles', t('next.overlays.discTitles'), 'i-layers', () => {value.actions!.discTitles!();}) : undefined;
  const radio = menu?.startRadio ? row('start-radio', t('music.startRadio'), 'i-pulse', () => presenters.startRadio(value)) : undefined;
  const run = (id: EntryActionId) => {
    switch (id) {
      case 'play': case 'resume': return presenters.playEntry(value);
      case 'playFromStart': return presenters.startOver(value);
      case 'playNext': case 'addToQueue': void presenters.enqueue(value, id === 'playNext' ? 'next' : 'end'); return;
      case 'watchWithGroup': case 'addToGroupQueue': void presenters.togetherEntry(value, id === 'watchWithGroup').catch(runtime.reportError); return;
      case 'watchlist': void presenters.personal(value, {action: 'watchlist', value: !value.detail?.personal.watchlisted}); return;
      case 'favorite': void presenters.personal(value, {action: 'favorite', value: !value.detail?.personal.favorite}); return;
      case 'watched': void presenters.personal(value, {action: 'watched', value: !(value.detail?.personal.watched ?? entry.watched ?? false)}); return;
      case 'rate': case 'addToCollection': case 'download': case 'editMetadata': case 'refreshMetadata': return presenters.titleAction(value, id);
      case 'delete': return presenters.titleAction(value, 'deleteTitle');
      case 'addToPlaylist': return presenters.addToPlaylist(value);
      case 'details': return presenters.showDetails(value);
      default: return;
    }
  };
  const extras = (value.actions?.extraActions ?? []).map(action => ({...action, disabled: value.busy || action.disabled, onSelect: () => presenters.extraEntry(value, action.id)}));
  // TV follows tv-more.html 2 ("Add to collection…, Select, Details, File info") and 3 ("Select, Go to artist, File info"),
  // with tv-detail.html 7's "Play a different version…" directly above File info. Disc titles are undrawn and keep their place after Select.
  const surface = (tv ? [select, artist, discs] : [version, select, fileInfo, discs]).filter((item): item is MenuAction => !!item);
  // The title's own ⋯ on its page is phone-detail 9's grouped menu; a card's menu stays select.html 1's list.
  const page = !tv && value.actions?.page === true;
  const items = titleMenuItems(groups, {tv, page, run, busy: value.busy, afterPlay: radio ? [radio] : undefined, related: season ? [season] : undefined, surface, afterDetails: tv ? [version, fileInfo].filter((item): item is MenuAction => !!item) : undefined, trailing: extras});
  // tv-detail.html frame 7 and playback-info.html frame 17 draw the side sheet's
  // small context line above the title, from the entry's own facts only.
  const series = tv && entry.kind === 'episode' ? entry.show?.name : undefined;
  const code = tv && entry.kind === 'episode' && entry.seasonNumber !== undefined && entry.episodeNumber !== undefined
    ? t('next.kit.episodeCode', {season: entry.seasonNumber, episode: entry.episodeNumber}) : undefined;
  const year = entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false});
  const movie = tv && !series && !code && entry.kind === 'movie' ? {kind: t('search.kind.movie'), year} : undefined;
  // playback-info.html frame 17 draws the film's runtime under the title at 22/1.45.
  const description = tv && entry.kind === 'movie' && entry.duration ? durationText(entry.duration, t) : undefined;
  return {value, subject, art, kind: kindKey ? t(kindKey) : undefined, year, items, page, tv, series, code, movie, description,
    close: () => presenters.closeMenu(value)};
}
