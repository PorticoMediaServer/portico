/**
 * Each screen frame's required regions, as data: every region a kind of screen draws across all its mock-up frames
 * and states, which forms draw it, and the frames it comes from. Both apps' frames take exactly these regions as
 * props; a region a screen does not fill is absent, never a blank. FRAMES.md beside each app's frames is the brief.
 *
 * One key per frame; each family owns its own key. Forms are `frameForm`'s, plus `medium-split`: a `medium` window
 * that is wider than tall (an open foldable on its side, foldable.html f2), which some frames lay out as two panes.
 */
import type {FrameForm} from './frames.ts';

export type RegionForm = FrameForm | 'medium-split';
export type FrameRegion = Readonly<{
  id: string;
  /** The forms that draw it. A form not listed has no place for it. */
  forms: readonly RegionForm[];
  /** The mock-up frames it is drawn in (`design/ui-v1`). */
  from: readonly string[];
  /** Fixed regions never move; the one flexible region is the only place that changes when content is missing or added. */
  flexible?: true;
}>;

const all: readonly RegionForm[] = ['phone', 'medium', 'medium-split', 'expanded', 'tv'];
const touch: readonly RegionForm[] = ['phone', 'medium', 'medium-split', 'expanded'];

/** Frame 2, the title page: a film, show, season, episode, person, collection, album, artist or book. */
const title = [
  {id: 'art', forms: all, from: ['desktop-detail f1', 'phone-detail d1', 'tablet f6', 'tablet f8', 'foldable f2', 'detail-tv-1']},
  {id: 'cover', forms: all, from: ['desktop-detail f7', 'desktop-listen album', 'desktop-listen artist', 'desktop-listen book', 'phone-detail d3 (7)', 'detail-tv-6']},
  {id: 'back', forms: touch, from: ['phone-detail d1', 'tablet f6', 'tablet f8', 'foldable f2', 'desktop-detail f7 (web from 1024: a text link on a pushed page; the nav is the way back otherwise)']},
  {id: 'topAction', forms: touch, from: ['phone-detail d1', 'tablet f6', 'tablet f8', 'foldable f2']},
  {id: 'kicker', forms: all, from: ['desktop-detail f1', 'desktop-detail f7', 'desktop-detail f8', 'tablet f6', 'phone-detail d3', 'desktop-listen album', 'desktop-listen book', 'detail-tv-6']},
  {id: 'title', forms: all, from: ['desktop-detail f1', 'phone-detail d1', 'detail-tv-1']},
  {id: 'metadata', forms: all, from: ['desktop-detail f1', 'phone-detail d1', 'detail-tv-1']},
  {id: 'picker', forms: ['phone', 'medium', 'medium-split'], from: ['phone-detail d1 (1)', 'desktop-detail f1 notes: on the web and TV the choices are in the player']},
  {id: 'primary', forms: all, from: ['desktop-detail f1', 'desktop-detail f2', 'phone-detail d1 (3)', 'detail-tv-2', 'desktop-detail f8', 'desktop-listen book']},
  {id: 'actions', forms: all, from: ['desktop-detail f1', 'desktop-detail f2', 'tablet f6', 'phone-detail d1', 'detail-tv-2']},
  {id: 'tagline', forms: ['expanded', 'tv'], from: ['desktop-detail f1', 'tablet f6', 'detail-tv-4']},
  {id: 'synopsis', forms: all, from: ['desktop-detail f1', 'phone-detail d1', 'desktop-detail f7 (Read more)', 'phone-detail d3 (Read more)']},
  {id: 'credits', forms: ['expanded', 'tv'], from: ['desktop-detail f1', 'tablet f6', 'detail-tv-1']},
  {id: 'cast', forms: ['medium-split'], from: ['foldable f2 (left page, under the synopsis)']},
  {id: 'tabs', forms: all, from: ['desktop-detail f1', 'phone-detail d1', 'detail-tv-4', 'detail-tv-5a']},
  {id: 'collapsedBar', forms: all, from: ['desktop-detail f3', 'desktop-detail f4', 'phone-detail d1 (2)', 'phone-detail d2 (4)', 'detail-tv-4', 'detail-tv-5a']},
  {id: 'panel', forms: all, from: ['desktop-detail f1 to f8', 'phone-detail d1 to d3', 'detail-tv-3 to 7'], flexible: true},
  {id: 'aside', forms: all, from: ['desktop-detail f8 (About)', 'desktop-listen album (Details, Files)', 'desktop-listen artist (Albums, Details)', 'desktop-listen book (About this book)']},
  {id: 'state', forms: all, from: ['states-20', 'states-22b']},
] as const satisfies readonly FrameRegion[];

export type TitleRegion = typeof title[number]['id'];
export type TitleKind = 'film' | 'show' | 'season' | 'episode' | 'person' | 'collection' | 'album' | 'artist' | 'book';

/** Which regions each kind of title page fills. `panel` holds the kind's own body: rows (film), the Episodes tab
 * (show, season), the episode's credits and neighbours (episode), On this server (person), Films in order
 * (collection), the songs (album, artist), the chapters (book). */
export const titleKinds = {
  film: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'picker', 'primary', 'actions', 'tagline', 'synopsis', 'credits', 'tabs', 'collapsedBar', 'panel', 'state'],
  show: ['art', 'back', 'topAction', 'title', 'metadata', 'primary', 'actions', 'tagline', 'synopsis', 'credits', 'cast', 'tabs', 'collapsedBar', 'panel', 'state'],
  season: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'primary', 'actions', 'synopsis', 'tabs', 'collapsedBar', 'panel', 'state'],
  episode: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'picker', 'primary', 'actions', 'synopsis', 'credits', 'panel', 'state'],
  person: ['cover', 'back', 'kicker', 'title', 'metadata', 'synopsis', 'panel', 'state'],
  collection: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'primary', 'actions', 'synopsis', 'panel', 'aside', 'state'],
  album: ['cover', 'back', 'kicker', 'title', 'metadata', 'primary', 'actions', 'panel', 'aside', 'state'],
  artist: ['cover', 'back', 'kicker', 'title', 'metadata', 'primary', 'actions', 'panel', 'aside', 'state'],
  book: ['cover', 'back', 'kicker', 'title', 'metadata', 'primary', 'actions', 'panel', 'aside', 'state'],
} as const satisfies Readonly<Record<TitleKind, readonly TitleRegion[]>>;

export const frameRegions = {title} as const;
