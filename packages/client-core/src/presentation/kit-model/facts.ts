import {clockText, compact, durationText, integer, nonnegative, part, positive, supplied, timelineFacts, type FactPart, type TextEnvironment} from './text.ts';

/** Local semantic inputs, not generated wire responses. `book` means an audiobook; ebook/comic are non-playable Reading leaves. */
export type TitleKind = 'movie' | 'show' | 'season' | 'episode' | 'extra' | 'artist' | 'album' | 'song' | 'disc' | 'radio' | 'book' | 'audiobook_file' | 'chapter' | 'author' | 'book_series' | 'collection' | 'category' | 'playlist' | 'ebook' | 'comic';
export type EpisodeNumbering =
  | Readonly<{mode: 'seasonal'; season?: number; episode?: number}>
  | Readonly<{mode: 'absolute'; number?: number}>
  | Readonly<{mode: 'date'; day?: string}>
  | Readonly<{mode: 'special'}>;
export type CardContext = 'ordinary' | 'continuation' | 'search' | 'in-show' | 'song-row' | 'dock';
export type TitleFacts = Readonly<{
  year?: number; contentRating?: string; durationSeconds?: number; genres?: readonly string[];
  seasonCount?: number; songCount?: number; albumCount?: number; bookCount?: number; itemCount?: number; countIsLowerBound?: boolean;
  artist?: string; album?: string; author?: string; narrator?: string;
  chapterNumber?: number; chapterCount?: number;
}>;
export type CardSubject = TitleFacts & Readonly<{
  id: string; kind: TitleKind; title: string; show?: Readonly<{id?: string; title: string}>;
  episode?: EpisodeNumbering; seasonNumber?: number; positionSeconds?: number;
}>;
export type CardTextModel = Readonly<{id: string; title: string; caption: readonly FactPart[]; detail: readonly FactPart[]; progress?: number; resume?: FactPart}>;
export type TitleFactContext = 'video' | 'music' | 'audiobook' | 'audiobook-chapters' | 'home-movie' | 'home-show';

/** A calendar day is not a device-zone instant. Reject impossible days, including rollover. */
function calendarDay(day: string | undefined, locale: string | undefined): string | undefined {
  if (!day || !/^\d{4}-\d{2}-\d{2}$/.test(day)) return undefined;
  const [y, m, d] = day.split('-').map(Number);
  const date = new Date(`${day}T12:00:00Z`);
  if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() !== y || date.getUTCMonth() + 1 !== m || date.getUTCDate() !== d) return undefined;
  return new Intl.DateTimeFormat(locale ?? 'en-US', {month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC'}).format(date);
}

/** Mixed-show contexts keep the show separately and use a compact seasonal code. */
export function episodeLabel(numbering: EpisodeNumbering | undefined, context: CardContext | 'player' | 'up-next', env: TextEnvironment): FactPart | undefined {
  if (!numbering) return undefined;
  const {t, locale} = env;
  if (numbering.mode === 'special') return part('episode-label', t('next.kit.special'), 'context');
  if (numbering.mode === 'date') {
    const date = calendarDay(numbering.day, locale);
    return date ? part('episode-label', date, 'context') : undefined;
  }
  if (numbering.mode === 'absolute') return integer(numbering.number) ? part('episode-label', t('next.kit.episode', {number: numbering.number}), 'context') : undefined;
  if (!integer(numbering.episode)) return undefined;
  const mixed = context !== 'in-show';
  return part('episode-label', mixed && nonnegative(numbering.season) && Number.isInteger(numbering.season)
    ? t('next.kit.episodeCode', {season: numbering.season, episode: numbering.episode})
    : t('next.kit.episode', {number: numbering.episode}), 'context');
}

/** Caller supplies the title and its genuine facts; this never selects a Home entry. */
export function titleFactParts(facts: TitleFacts, env: TextEnvironment, context: TitleFactContext = 'video'): readonly FactPart[] {
  const {t} = env;
  const time = durationText(facts.durationSeconds, t);
  // Home hero and TV focus share year → genres → movie length or show seasons.
  // The kind is explicit: an unknown show season total never becomes episode length.
  if (context === 'home-movie' || context === 'home-show') {
    // The frame draws genres as one comma-separated metadata group. Consumers render it whole.
    const genres = (facts.genres ?? []).filter(supplied);
    return compact([
      integer(facts.year) ? part('year', String(facts.year)) : undefined,
      genres.length ? part('genres', genres.reduce((list, genre) => t('next.kit.genres.append', {list, genre})), 'supplement') : undefined,
      context === 'home-movie' && time ? part('duration', time) : undefined,
      context === 'home-show' && integer(facts.seasonCount) ? part('season-count', t('next.kit.seasons', {count: facts.seasonCount}), 'supplement') : undefined,
    ]);
  }
  // The full player's chapter-list summary alone includes the chapter total in its label.
  if (context === 'audiobook-chapters') return compact([
    integer(facts.chapterCount) ? part('chapter-count', t('next.kit.chapters', {count: facts.chapterCount}), 'supplement') : undefined,
    chapterPart(facts, env, true),
  ]);
  if (context !== 'video') return compact([
    context === 'music' && supplied(facts.artist) ? part('artist', facts.artist, 'identity') : undefined,
    context === 'music' && supplied(facts.album) ? part('album', facts.album, 'context') : undefined,
    context === 'audiobook' && supplied(facts.author) ? part('author', facts.author, 'identity') : undefined,
    context === 'audiobook' && supplied(facts.narrator) ? part('narrator', t('next.kit.readBy', {name: facts.narrator}), 'context') : undefined,
    context === 'music' && integer(facts.year) ? part('year', String(facts.year)) : undefined,
    ...(context === 'music' ? (facts.genres ?? []).filter(supplied).map((g, i) => part(`genre-${i}`, g, 'supplement')) : []),
    ...(context === 'music' ? counts(facts, env) : []),
    time ? part('duration', time) : undefined,
    context === 'audiobook' && integer(facts.chapterCount) ? part('chapter-count', t('next.kit.chapters', {count: facts.chapterCount}), 'supplement') : undefined,
  ]);
  return compact([
    integer(facts.year) ? part('year', String(facts.year)) : undefined,
    time ? part('duration', time) : undefined,
    ...(facts.genres ?? []).filter(supplied).map((g, i) => part(`genre-${i}`, g, 'supplement')),
    supplied(facts.contentRating) ? part('content-rating', facts.contentRating, 'supplement') : undefined,
    integer(facts.seasonCount) ? part('season-count', t('next.kit.seasons', {count: facts.seasonCount}), 'supplement') : undefined,
  ]);
}

function counts(subject: TitleFacts, env: TextEnvironment): readonly FactPart[] {
  const {t} = env;
  return compact([
    integer(subject.albumCount) ? part('album-count', t('next.kit.albums', {count: subject.albumCount}), 'supplement') : undefined,
    integer(subject.songCount) ? part('song-count', t('next.kit.songs', {count: subject.songCount}), 'supplement') : undefined,
    integer(subject.bookCount) ? part('book-count', t('next.kit.books', {count: subject.bookCount}), 'supplement') : undefined,
    integer(subject.itemCount) ? part('item-count', t(subject.countIsLowerBound ? 'next.kit.itemsAtLeast' : 'next.kit.items', {count: subject.itemCount}), 'supplement') : undefined,
  ]);
}

function chapterPart(facts: TitleFacts, env: TextEnvironment, includeTotal = false): FactPart | undefined {
  if (!integer(facts.chapterNumber)) return undefined;
  return part('chapter', includeTotal && integer(facts.chapterCount) && facts.chapterCount >= facts.chapterNumber
    ? env.t('next.kit.chapterOf', {number: facts.chapterNumber, total: facts.chapterCount})
    : env.t('next.kit.chapter', {number: facts.chapterNumber}), 'context');
}

/** tv.html#f5 draws the top result's resume point as one line under its facts:
 * the episode code it resumes at, then the time left. Only a real position, a
 * real length and a real episode number produce it; a title never supplies one. */
function resumeLine(subject: CardSubject, remainingSeconds: number | undefined, env: TextEnvironment): FactPart | undefined {
  if (!positive(subject.positionSeconds) || !positive(remainingSeconds)) return undefined;
  const left = durationText(remainingSeconds, env.t);
  if (!left) return undefined;
  const remaining = env.t('next.kit.remaining', {time: left});
  const label = episodeLabel(subject.episode, 'search', env);
  return part('resume', label ? env.t('next.search.resume', {episode: label.text, remaining}) : remaining, 'supplement');
}

/** Separate name, caption, facts and measured progress for every card family. */
export function cardText(subject: CardSubject, context: CardContext, env: TextEnvironment): CardTextModel {
  // A radio start is a seed, never a media item, duration, count or resume point.
  if (subject.kind === 'radio') return Object.freeze({id: subject.id, title: env.t('music.radioNamed', {name: subject.title}),
    caption: Object.freeze([part('radio', env.t('music.radio'), 'context')]), detail: Object.freeze([])});
  if (subject.kind === 'ebook' || subject.kind === 'comic') {
    return Object.freeze({id: subject.id, title: subject.title,
      caption: Object.freeze(compact([supplied(subject.author) ? part('author', subject.author, 'identity') : undefined])),
      detail: Object.freeze(compact([integer(subject.year) ? part('year', String(subject.year)) : undefined]))});
  }
  const {t} = env, timeline = timelineFacts(subject.durationSeconds, subject.positionSeconds);
  const resume = context === 'search' ? resumeLine(subject, timeline.remainingSeconds, env) : undefined;
  if (context === 'dock') {
    const kind = ['album', 'song', 'disc'].includes(subject.kind) ? 'music' : ['book', 'audiobook_file', 'chapter'].includes(subject.kind) ? 'audiobook' : 'video';
    const identity = playbackIdentity({kind, item: subject}, 'dock', env);
    return Object.freeze({id: identity.id, title: identity.title, caption: identity.subtitle, detail: Object.freeze([]), ...(timeline.progress === undefined ? {} : {progress: timeline.progress})});
  }
  const duration = context === 'song-row' && subject.kind === 'song' ? clockText(subject.durationSeconds) : durationText(subject.durationSeconds, t);
  const left = positive(subject.positionSeconds) ? durationText(timeline.remainingSeconds, t) : undefined;
  const remaining = left ? part('remaining', t('next.kit.remaining', {time: left}), 'supplement') : undefined;
  let title = subject.title, caption: readonly FactPart[] = [], detail: readonly FactPart[] = [];
  if (subject.kind === 'episode') {
    const label = episodeLabel(subject.episode, context, env);
    if (context === 'continuation') {
      const identity = playbackIdentity({kind: 'video', item: subject}, 'dock', env);
      title = identity.title;
      caption = identity.subtitle;
      detail = compact([remaining]);
    } else {
      caption = compact([context !== 'in-show' && supplied(subject.show?.title) ? part('show', subject.show.title, 'identity') : undefined, label]);
      detail = compact([duration ? part('duration', duration, 'supplement') : undefined]);
    }
  } else if (subject.kind === 'season') {
    const season = subject.seasonNumber === 0 ? t('next.kit.specials') : integer(subject.seasonNumber) ? t('next.kit.season', {number: subject.seasonNumber}) : undefined;
    if (subject.show?.title) title = subject.show.title;
    caption = compact([season ? part('season', season, 'context') : undefined]);
  } else if (['album', 'song', 'disc'].includes(subject.kind)) {
    caption = compact([
      supplied(subject.artist) ? part('artist', subject.artist, 'identity') : undefined,
      context === 'song-row' && supplied(subject.album) ? part('album', subject.album, 'context') : undefined,
      integer(subject.year) ? part('year', String(subject.year), 'supplement') : undefined,
    ]);
    detail = compact([...counts(subject, env), duration ? part('duration', duration, 'supplement') : undefined]);
  } else if (['book', 'audiobook_file', 'chapter'].includes(subject.kind)) {
    caption = compact([supplied(subject.author) ? part('author', subject.author, 'identity') : undefined]);
    detail = context === 'continuation' ? compact([chapterPart(subject, env), remaining]) : [];
  } else if (subject.kind === 'artist' || subject.kind === 'author' || subject.kind === 'book_series' || subject.kind === 'collection' || subject.kind === 'category') {
    caption = counts(subject, env);
  } else {
    caption = subject.kind === 'show' && integer(subject.seasonCount)
      ? [part('season-count', t('next.kit.seasons', {count: subject.seasonCount}), 'supplement')]
      : compact([integer(subject.year) ? part('year', String(subject.year), 'supplement') : undefined]);
    if (context === 'continuation') detail = compact([remaining]);
  }
  return Object.freeze({id: subject.id, title, caption: Object.freeze(caption), detail: Object.freeze(detail),
    ...(timeline.progress === undefined ? {} : {progress: timeline.progress}), ...(resume ? {resume} : {})});
}

export type PlaybackSubject =
  | Readonly<{kind: 'video'; item: CardSubject}>
  | Readonly<{kind: 'music' | 'audiobook'; item: CardSubject}>
  | Readonly<{kind: 'channel'; id: string; sourceId: string; channelName: string; programmeTitle?: string; programmeSubtitle?: string; episode?: EpisodeNumbering; live?: boolean}>;
export type PlaybackIdentityModel = Readonly<{id: string; kind: PlaybackSubject['kind']; title: string; subtitle: readonly FactPart[]; sourceId?: string}>;

/** Display identity only: no queue, source selection, prompts, or session state. */
export function playbackIdentity(subject: PlaybackSubject, context: 'full' | 'dock', env: TextEnvironment): PlaybackIdentityModel {
  if (subject.kind === 'channel') return Object.freeze({id: subject.id, sourceId: subject.sourceId, kind: subject.kind,
    title: subject.programmeTitle || subject.channelName,
    subtitle: compact([
      subject.programmeTitle ? part('channel', subject.channelName, 'identity') : undefined,
      episodeLabel(subject.episode, 'player', env),
      supplied(subject.programmeSubtitle) ? part('programme-subtitle', subject.programmeSubtitle, 'context') : undefined,
      subject.live === true ? part('live', env.t('next.kit.live'), 'status') : undefined,
    ]),
  });
  const e = subject.item;
  const episode = e.kind === 'episode';
  return Object.freeze({id: e.id, kind: subject.kind, title: episode ? e.show?.title || e.title : e.title,
    subtitle: compact(episode ? [episodeLabel(e.episode, 'player', env), supplied(e.show?.title) && e.title !== e.show.title ? part('episode-title', e.title, 'identity') : undefined]
      : subject.kind === 'music' ? [supplied(e.artist) ? part('artist', e.artist, 'identity') : undefined, context === 'full' && supplied(e.album) ? part('album', e.album, 'context') : undefined]
      : subject.kind === 'audiobook' ? [context === 'dock' ? chapterPart(e, env) : undefined, supplied(e.author) ? part('author', e.author, 'identity') : undefined, context === 'full' && supplied(e.narrator) ? part('narrator', env.t('next.kit.readBy', {name: e.narrator}), 'context') : undefined] : []),
  });
}
