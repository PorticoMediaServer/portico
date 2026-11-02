import type {SearchCapabilities, SearchEntry, SearchGroup, SearchGroupResult} from '../search.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

/**
 * Search as every client draws it (Justin, 2 Oct 2026): the type chips in one authored order,
 * result sections in that same order, "Audiobooks" rather than "Books", and a Top result when
 * one title matches the words strongly.
 */
export type SearchChip = Readonly<{id: SearchGroup | 'all'; label: MessageId}>;

export const SEARCH_CHIPS: readonly SearchChip[] = [
  {id: 'all', label: 'search.chip.all'},
  {id: 'movies', label: 'search.chip.movies'},
  {id: 'shows', label: 'search.chip.shows'},
  {id: 'episodes', label: 'search.chip.episodes'},
  {id: 'people', label: 'search.chip.people'},
  {id: 'artists', label: 'search.chip.artists'},
  {id: 'albums', label: 'search.chip.albums'},
  {id: 'songs', label: 'search.chip.songs'},
  {id: 'books', label: 'search.chip.audiobooks'},
  {id: 'reading', label: 'search.chip.reading'},
  {id: 'live-tv', label: 'search.chip.liveTV'},
];

/** Whether a type chip is drawn. All always is. Once an answer has named the groups this viewer may
 *  search, a group it leaves out (Live TV on a profile without it) or marks unavailable (Live TV not set
 *  up) has no chip; before that only Reading waits, because it is optional on every server. */
export function searchChipOffered(id: SearchChip['id'], capabilities: SearchCapabilities | undefined): boolean {
  if (id === 'all') return true;
  if (!capabilities) return id !== 'reading';
  return capabilities.groups.some(group => group.id === id && group.available);
}

/** The groups a client asks for when the All view is shown: the drawn chips' groups and no
 *  others (notes/search.md item 16), so no client receives a group it cannot name; Collections
 *  and Authors stay out of the chips and the answer (notes/search.md:211). Reading is asked for
 *  only once the server has said it is installed, so an older server never gets an unknown group. */
export function searchRequestGroups(readingOffered: boolean): SearchGroup[] {
  return SEARCH_CHIPS.flatMap(chip => chip.id === 'all' || chip.id === 'reading' && !readingOffered ? [] : [chip.id]);
}
/** Whether this answer says the server has Reading installed for this viewer. */
export const searchReadingOffered = (capabilities: SearchCapabilities | undefined): boolean =>
  capabilities?.groups.some(group => group.id === 'reading' && group.available) === true;

const ORDER = new Map<string, number>(SEARCH_CHIPS.map((chip, index) => [chip.id, index]));

/** A group's name, the chip's. */
export function searchGroupLabel(id: SearchGroup): MessageId {
  return SEARCH_CHIPS.find(chip => chip.id === id)?.label ?? 'search.chip.all';
}

/** The groups that have results, in the chips' order. */
export function orderedSearchGroups(groups: readonly SearchGroupResult[]): readonly SearchGroupResult[] {
  return groups.filter(group => group.items.length > 0).slice().sort((a, b) => (ORDER.get(a.id) ?? 99) - (ORDER.get(b.id) ?? 99));
}

const plain = (text: string) => text.normalize('NFKD').replace(/[̀-ͯ]/g, '').toLowerCase().replace(/[^\p{L}\p{N}]+/gu, ' ').trim();
/** Groups whose first result can be the Top result: things with a page of their own. */
const TOP_GROUPS: ReadonlySet<SearchGroup> = new Set<SearchGroup>(['movies', 'shows', 'artists', 'albums', 'books', 'people']);

export type TopSearchResult = Readonly<{group: SearchGroup; entry: SearchEntry}>;

/**
 * The Top result: the one title whose name is the words typed (or the only one that starts with
 * them). Two titles that match equally give none: a block that guesses wrong is worse than none.
 */
export function topSearchResult(query: string, groups: readonly SearchGroupResult[]): TopSearchResult | undefined {
  const words = plain(query);
  if (words.length < 2) return undefined;
  const exact: TopSearchResult[] = [], starts: TopSearchResult[] = [];
  for (const group of orderedSearchGroups(groups)) {
    if (!TOP_GROUPS.has(group.id)) continue;
    for (const entry of group.items) {
      const title = plain(entry.title);
      if (title === words) exact.push({group: group.id, entry});
      else if (words.length >= 3 && title.startsWith(words + ' ')) starts.push({group: group.id, entry});
    }
  }
  if (exact.length === 1) return exact[0];
  if (!exact.length && starts.length === 1) return starts[0];
  return undefined;
}

/** What a Top result is, in the singular ("Show · 2008"); the section headings keep the plural. */
const TOP_KIND: Readonly<Partial<Record<SearchGroup, MessageId>>> = {movies: 'search.kind.movie', shows: 'search.kind.show', artists: 'search.kind.artist', albums: 'search.kind.album', books: 'search.kind.audiobook', people: 'search.kind.person'};
export function topResultKindLabel(group: SearchGroup): MessageId | undefined {
  return TOP_KIND[group];
}
