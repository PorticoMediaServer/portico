/**
 * What a write confirmed on this device moves (CLIENT-DATA-REVIEW §6 row 7).
 *
 * Every write goes out through the API's one transport (`HttpLocalApi.send`), whoever makes it: a
 * title page, a grid's selection, a menu, the music player, a settings form. When the server
 * confirms one, the transport says so once (`onConfirmedWrite`), and the viewer's cache notes
 * what that write moved, named as the server's own hints name it. Every page built from those
 * dependencies (a show's episode list, a Home row, a grid, Saved) reads again in place now,
 * without waiting for the hint, and with no code in the writer. The hint still arrives and is the
 * backstop for anything this table does not name.
 *
 * Only writes are named here, never reads. A route not in the table moves nothing locally.
 */
import {apiRoot, routes} from '../../../contracts/src/routes.ts';
import type {QueryClient} from '@tanstack/query-core';
import {dependsOn, personalChangedHere, type FreshnessDependency} from '../freshness-hub.ts';
import {queryKeys} from './keys.ts';

/**
 * What the server's answer to a write says about it, for the routes whose answer names something
 * (`readsAnswer`): only these fields, read by this module, never the answer itself.
 */
export type WriteAnswer = Readonly<{
  /** The library the title is in (`put_items_id_personal_state` answers it). */
  libraryId?: string;
  /** The titles a batch changed (`put_items_personal_state_batch`: its results that are `ok`). */
  itemIds?: readonly string[];
}>;

/**
 * A write the server confirmed (a 2xx answer to a request other than GET). Listeners hear the route,
 * its path parameters and what the answer names, never the request body (a settings form's can hold
 * a credential) nor the whole answer.
 */
export type ConfirmedWrite = Readonly<{
  /** The generated route id. */
  route: WriteRouteId;
  /** The route's path parameters, decoded. */
  params: Readonly<Record<string, string>>;
  /** What the answer names, for the routes that read it; otherwise absent (as when the answer could not be read). */
  answer?: WriteAnswer;
}>;

/** What the viewer's cache knows about a title (from a page about it that it holds). */
export type WrittenTitle = Readonly<{libraryId?: string; titleIds: readonly string[]}>;
export type WriteContext = Readonly<{
  /** The library of a title, and the titles above it (an episode's show and season), when the cache holds a page naming it. */
  title(itemId: string): WrittenTitle | undefined;
  /** Every library the cache holds a page of. */
  libraries(): readonly string[];
}>;

type Moves = (write: ConfirmedWrite, context: WriteContext) => readonly FreshnessDependency[];
const ALL = '*';
const record = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
const id = (value: unknown): string | undefined => typeof value === 'string' && value.length > 0 && value.length <= 256 ? value : undefined;
/**
 * This profile's state of titles changed. The library comes from the answer, or else from a page
 * the cache holds that names the title; with neither, every library's personal state moves
 * (`personal:*`) and the pages about these titles, which is what a hint naming the titles but not
 * the library would move.
 */
function titlesChanged(items: readonly Readonly<{itemId: string; libraryId?: string}>[], context: WriteContext, container = false): FreshnessDependency[] {
  const out: FreshnessDependency[] = [];
  for (const item of items) {
    const known = context.title(item.itemId), libraryId = item.libraryId ?? known?.libraryId;
    const titles = [item.itemId, ...(known?.titleIds ?? [])];
    if (libraryId) out.push(...personalChangedHere(libraryId, titles));
    else out.push(dependsOn.personal(), ...titles.map(t => `personal:title:${t}`));
    // A show, season or album changes every title under it, which this device cannot name: every
    // page of its library moves, as for a hint that names no title. Of a library not known here,
    // every library's pages the cache holds.
    if (container) out.push(...(libraryId ? [`personal:any:${libraryId}`] : everyHeldLibrary(context)));
  }
  return out;
}
/** Every page of every library the cache holds moves for this profile's state: a write whose titles or library are not known here. */
const everyHeldLibrary = (context: WriteContext): FreshnessDependency[] =>
  [`personal:any:${ALL}`, ...context.libraries().flatMap(libraryId => [dependsOn.personal(libraryId), `personal:any:${libraryId}`])];
const oneTitle = (param: string, container = false): Moves => (write, context) => {
  const itemId = write.params[param];
  return itemId ? titlesChanged([{itemId, libraryId: write.answer?.libraryId}], context, container) : [];
};
/** Something of this profile's that no page about a title shows by name (history, Saved, pins). */
const profileWide: Moves = () => [dependsOn.personal(), `personal:any:${ALL}`];
const preferences: Moves = () => [dependsOn.settings('preferences')];
const serverSettings: Moves = () => [dependsOn.settings('server')];
const directory: Moves = () => [dependsOn.libraries, dependsOn.settings('server')];

const table = {
  // A title's own state: watched, favourite, watchlist, rating, Continue Watching, not interested.
  put_items_id_personal_state: oneTitle('id'),
  put_items_itemId_reading_favorite: oneTitle('itemId'),
  put_items_itemId_reading_mark: oneTitle('itemId'),
  put_items_itemId_reading_my_list: oneTitle('itemId'),
  post_items_itemId_reading_reread: oneTitle('itemId'),
  // The answer names the titles changed (each `ok` result); their library comes from the pages the
  // cache holds. An answer that could not be read names none: every held library moves.
  put_items_personal_state_batch: (write, context) => {
    const itemIds = write.answer?.itemIds;
    return itemIds ? titlesChanged(itemIds.map(itemId => ({itemId})), context) : [dependsOn.personal(), ...everyHeldLibrary(context)];
  },
  // A show, season, album or series as a whole.
  set_container_personal_state: oneTitle('id', true),
  undo_container_personal_state: oneTitle('id', true),
  put_reading_group_my_list: (write, context) => {
    const libraryId = write.params.libraryId, seriesId = write.params.seriesId;
    return libraryId && seriesId ? titlesChanged([{itemId: seriesId, libraryId}], context, true) : [];
  },
  post_libraries_libraryId_reading_mark_all: write => {
    const libraryId = write.params.libraryId;
    return libraryId ? [dependsOn.personal(libraryId), dependsOn.personal(), `personal:any:${libraryId}`, `personal:any:${ALL}`] : [];
  },
  // The profile's history, removed titles, Saved and pins.
  post_personal_history_actions: () => [dependsOn.personal(), `personal:any:${ALL}`, dependsOn.progress(), `progress:any:${ALL}`],
  delete_me_removed_titles_id: profileWide,
  post_me_recommendations_reset: () => [dependsOn.list('recommendations'), dependsOn.personal()],
  delete_reading_saved_group_reference: profileWide,
  put_saved_pins_order: profileWide,
  put_saved_pins_kind_id: profileWide,
  post_saved_resources: profileWide,
  delete_saved_resources_id: profileWide,
  patch_saved_resources_id: profileWide,
  put_saved_resources_id_entries: profileWide,
  reorder_saved_collection: profileWide,
  // This profile's preferences.
  patch_preferences: preferences,
  put_listening_preferences: preferences,
  put_download_settings: preferences,
  put_me_library_navigation: preferences,
  post_channels_preferences: () => [dependsOn.settings('preferences'), dependsOn.list('guide')],
  post_library_channels_preferences: () => [dependsOn.settings('preferences'), dependsOn.list('libraryChannels')],
  // The owner's libraries and server settings.
  post_libraries: directory,
  patch_admin_libraries_id: directory,
  put_admin_libraries_order: directory,
  put_library_settings: serverSettings,
  put_admin_libraries_id_scan_policy: serverSettings,
  put_admin_libraries_id_shows_show_settings: serverSettings,
  put_admin_libraries_id_sonic_analysis: serverSettings,
  put_libraries_id_metadata_agent: serverSettings,
  put_libraries_id_metadata_screen: serverSettings,
  put_libraries_id_strm_analysis_policy: serverSettings,
  put_metadata_settings: serverSettings,
  put_admin_backup_settings: serverSettings,
  change_console_settings: serverSettings,
  put_admin_dvr_settings: serverSettings,
  set_live_settings: serverSettings,
  set_live_source_configuration: () => [dependsOn.settings('server'), dependsOn.list('liveSources'), dependsOn.list('guide')],
  patch_admin_logs_settings: serverSettings,
  put_admin_maintenance_settings: serverSettings,
  patch_admin_notifications_settings: serverSettings,
  post_networking_remote_config: serverSettings,
  put_storage_source_advanced: serverSettings,
  put_storage_source_program: serverSettings,
  put_storage_source_read_profile: serverSettings,
} satisfies Record<string, Moves>;
export type WriteRouteId = keyof typeof table;
/** Every write the table names. */
export const writeRouteIds = Object.freeze(Object.keys(table) as WriteRouteId[]);

/**
 * The routes whose answer names something the table uses, and the reading of it. Only these
 * answers are read (from a copy), and only these fields leave this module. Container answers
 * (`ContainerPersonal`) name neither a library nor titles, so they are not read.
 */
const answers: Partial<Record<WriteRouteId, (answer: Record<string, unknown>) => WriteAnswer | undefined>> = {
  // `PutItemsIdPersonalStateResponse.libraryId`.
  put_items_id_personal_state: answer => { const libraryId = id(answer.libraryId); return libraryId ? {libraryId} : undefined; },
  // `PutItemsPersonalStateBatchResponse.results[]`: `{itemId, ok, personal?}`, no library.
  put_items_personal_state_batch: answer => {
    if (!Array.isArray(answer.results)) return undefined;
    const itemIds = [...new Set(answer.results.flatMap(entry => { const e = record(entry), itemId = id(e?.itemId); return itemId && e?.ok === true ? [itemId] : []; }))];
    return {itemIds: Object.freeze(itemIds)};
  },
};
/** What a write's answer names, for a route that reads it (`writeRoute(...).readsAnswer`). */
export function readWriteAnswer(route: WriteRouteId, answer: unknown): WriteAnswer | undefined {
  const read = answers[route], value = record(answer);
  if (!read || !value) return undefined;
  const found = read(value);
  return found ? Object.freeze(found) : undefined;
}

type Pattern = Readonly<{id: WriteRouteId; method: string; parts: readonly string[]}>;
const patterns: readonly Pattern[] = routes.filter(route => route.method !== 'GET' && Object.hasOwn(table, route.id))
  .map(route => Object.freeze({id: route.id as WriteRouteId, method: route.method, parts: route.path.split('/')}));
if (patterns.length !== Object.keys(table).length) throw new Error('A write in the cache table has no generated route.');

/** The route a write went to, and its path parameters; undefined when the table does not name it. */
export function writeRoute(path: string, method: string): Readonly<{route: WriteRouteId; params: Readonly<Record<string, string>>; readsAnswer: boolean}> | undefined {
  if (method === 'GET' || method === 'HEAD' || !path.startsWith(apiRoot)) return undefined;
  const segments = path.split(/[?#]/)[0].split('/');
  for (const pattern of patterns) {
    if (pattern.method !== method || pattern.parts.length !== segments.length) continue;
    const params: Record<string, string> = {};
    let ok = true;
    for (let i = 0; i < segments.length && ok; i++) {
      const part = pattern.parts[i], value = segments[i];
      const open = part.indexOf('{');
      if (open < 0) { ok = part === value; continue; }
      const close = part.indexOf('}'), prefix = part.slice(0, open), suffix = part.slice(close + 1);
      if (!value.startsWith(prefix) || !value.endsWith(suffix) || value.length <= prefix.length + suffix.length) { ok = false; continue; }
      try { params[part.slice(open + 1, close)] = decodeURIComponent(value.slice(prefix.length, value.length - suffix.length)); } catch { ok = false; }
    }
    if (ok) return Object.freeze({route: pattern.id, params: Object.freeze(params), readsAnswer: Object.hasOwn(answers, pattern.id)});
  }
  return undefined;
}

/** What a confirmed write moved, for `ViewerQueries.moved`. */
export function writeDependencies(write: ConfirmedWrite, context: WriteContext): readonly FreshnessDependency[] {
  const moves: Moves | undefined = (table as Record<string, Moves>)[write.route];
  return moves ? [...new Set(moves(write, context))] : [];
}

/**
 * A title's library and the titles above it, from a page the cache holds that names it: a page
 * about it (`itemDetail`), a show's page (the show, one of its seasons, or an episode it lists), or
 * a content page (an album, artist or book, or a title it lists).
 */
export function cachedTitle(client: QueryClient, itemId: string): WrittenTitle | undefined {
  for (const related of ['all', 'none'] as const) {
    const page = record(client.getQueryData(queryKeys.itemDetail(itemId, related)));
    const libraryId = id(record(page?.scope)?.libraryId);
    if (!libraryId) continue;
    const episode = record(record(page?.item)?.episode);
    return {libraryId, titleIds: [id(episode?.showId), id(episode?.seasonId)].filter((t): t is string => !!t)};
  }
  for (const query of client.getQueryCache().findAll({queryKey: ['library']})) {
    const [, library, kind] = query.queryKey, libraryId = id(library), page = record(query.state.data);
    if (!libraryId || !page) continue;
    if (kind === 'show-workspace') {
      const showId = id(record(page.show)?.id);
      if (!showId) continue;
      if (showId === itemId) return {libraryId, titleIds: []};
      if (Array.isArray(page.seasons) && page.seasons.some(season => record(season)?.id === itemId)) return {libraryId, titleIds: [showId]};
      if (lists(record(page.episodes), itemId)) return {libraryId, titleIds: [showId, id(record(page.selected)?.seasonId)].filter((t): t is string => !!t)};
    } else if (kind === 'content') {
      if (record(page.scope)?.entityId === itemId || record(page.entity)?.id === itemId || lists(page, itemId)) return {libraryId, titleIds: []};
    }
  }
  return undefined;
}
/** Whether a content projection lists the title (as an entry, or what an entry plays). */
function lists(projection: Record<string, unknown> | undefined, itemId: string): boolean {
  const sections = projection?.sections;
  if (!Array.isArray(sections)) return false;
  return sections.some(section => {
    const entries = record(section)?.entries;
    return Array.isArray(entries) && entries.some(entry => { const e = record(entry); return e?.id === itemId || record(e?.playback)?.itemId === itemId; });
  });
}

/** Every library the cache holds a page of: a library's pages are keyed under it, and a title's page names its library. */
export function cachedLibraries(client: QueryClient): readonly string[] {
  const out = new Set<string>();
  for (const query of client.getQueryCache().getAll()) {
    const key = query.queryKey;
    const libraryId = key[0] === 'library' ? id(key[1]) : key[0] === 'item' ? id(record(record(query.state.data)?.scope)?.libraryId) : undefined;
    if (libraryId) out.add(libraryId);
  }
  return [...out];
}
