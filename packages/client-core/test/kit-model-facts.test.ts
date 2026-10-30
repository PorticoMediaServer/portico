import test from 'node:test';
import assert from 'node:assert/strict';
import {formatMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {searchMessages} from '../../i18n/src/catalog/en-US/next/search.ts';
import {cardText, titleFactParts, episodeLabel, playbackIdentity, timelineFacts, type CardContext, type EpisodeNumbering, type FactPart, type KitTranslate} from '../src/presentation/kit-model/index.ts';

const messages = {...shared, ...searchMessages};
const t: KitTranslate = (id, ...args) => formatMessage(messages[id], args[0]);
const env = {t, locale: 'en-US'};
const parts = (p: readonly FactPart[]) => p.map(x => [x.id, x.text, x.role]);

test('title facts keep the exact year, runtime, genre, rating and season order without joining', () => {
  assert.deepEqual(parts(titleFactParts({year: 2024, durationSeconds: 9960, genres: ['Science Fiction', 'Adventure'], contentRating: 'PG-13'}, env)), [
    ['year', '2024', 'value'], ['duration', '2 h 46 min', 'value'], ['genre-0', 'Science Fiction', 'supplement'], ['genre-1', 'Adventure', 'supplement'], ['content-rating', 'PG-13', 'supplement'],
  ]);
  assert.deepEqual(parts(titleFactParts({year: 2022, genres: ['Crime', 'Drama', 'Mystery'], seasonCount: 1}, env)).at(-1), ['season-count', '1 season', 'supplement']);
  assert.deepEqual(titleFactParts({durationSeconds: NaN, year: 0, contentRating: '', genres: [''], seasonCount: 0}, env), []);
});

const modes: readonly [EpisodeNumbering, string, string][] = [
  [{mode: 'seasonal', season: 1, episode: 2}, 'Episode 2', 'S1 E2'],
  [{mode: 'absolute', number: 37}, 'Episode 37', 'Episode 37'],
  [{mode: 'date', day: '2026-10-21'}, 'Oct 21, 2026', 'Oct 21, 2026'],
  [{mode: 'special'}, 'Special', 'Special'],
];
const contexts: readonly CardContext[] = ['ordinary', 'continuation', 'search', 'in-show', 'song-row', 'dock'];
for (const [numbering, local, mixed] of modes) for (const context of [...contexts, 'player', 'up-next'] as const) test(`${numbering.mode} episode label in ${context}`, () => {
  assert.deepEqual(episodeLabel(numbering, context, env), {id: 'episode-label', text: context === 'in-show' ? local : mixed, role: 'context'});
  if (context === 'player' || context === 'up-next') return;
  const model = cardText({id: 'episode-real', kind: 'episode', title: 'Infected', show: {id: 'show-real', title: 'The Last of Us'}, episode: numbering, durationSeconds: 53 * 60, positionSeconds: 29 * 60}, context, env);
  if (context === 'continuation' || context === 'dock') {
    assert.equal(model.title, 'The Last of Us');
    assert.deepEqual(parts(model.caption), [['episode-label', mixed, 'context'], ['episode-title', 'Infected', 'identity']]);
    assert.deepEqual(parts(model.detail), context === 'continuation' ? [['remaining', '24 min left', 'supplement']] : []);
  } else {
    assert.equal(model.title, 'Infected');
    assert.deepEqual(parts(model.caption), [...(context === 'in-show' ? [] : [['show', 'The Last of Us', 'identity']]), ['episode-label', context === 'in-show' ? local : mixed, 'context']]);
    assert.deepEqual(parts(model.detail), [['duration', '53 min', 'supplement']]);
  }
  assert.equal(model.id, 'episode-real'); assert.equal(model.progress, 29 / 53);
});

test('incomplete episode numbering is omitted; calendar days validate and retain the full year', () => {
  for (const n of [undefined, {mode: 'seasonal'}, {mode: 'seasonal', season: 1, episode: -1}, {mode: 'absolute', number: Infinity}, {mode: 'date', day: '2026-02-29'}, {mode: 'date', day: '2026-02-30'}, {mode: 'date', day: '2026-13-01'}, {mode: 'date', day: 'Oct 21'}] as const) assert.equal(episodeLabel(n, 'in-show', env), undefined);
  assert.equal(episodeLabel({mode: 'date', day: '2024-02-29'}, 'in-show', env)?.text, 'Feb 29, 2024');
  assert.equal(episodeLabel({mode: 'seasonal', episode: 8}, 'search', env)?.text, 'Episode 8');
  assert.equal(episodeLabel({mode: 'seasonal', season: 0, episode: 1}, 'search', env)?.text, 'S0 E1');
  assert.equal(episodeLabel({mode: 'absolute', number: 1043}, 'in-show', env)?.text, 'Episode 1043');
});

test('ordinary movie/show/season and special-season captions retain identity', () => {
  assert.deepEqual(cardText({id: 'm', kind: 'movie', title: 'Dune: Part Two', year: 2024}, 'ordinary', env), {id: 'm', title: 'Dune: Part Two', caption: [{id: 'year', text: '2024', role: 'supplement'}], detail: []});
  assert.equal(cardText({id: 's', kind: 'show', title: 'Show', seasonCount: 6}, 'ordinary', env).caption[0]?.text, '6 seasons');
  assert.deepEqual(parts(cardText({id: 's1', kind: 'season', title: 'Season one', seasonNumber: 1, show: {title: 'Show'}}, 'ordinary', env).caption), [['season', 'Season 1', 'context']]);
  assert.equal(cardText({id: 's0', kind: 'season', title: 'Extras', seasonNumber: 0}, 'ordinary', env).caption[0]?.text, 'Specials');
});

test('Continue Watching retains film year and independent remaining detail', () => {
  const film = {id: 'film-real', kind: 'movie', title: 'Inside Out', year: 2015, durationSeconds: 6000, positionSeconds: 840} as const;
  assert.deepEqual(cardText(film, 'continuation', env), {id: 'film-real', title: 'Inside Out', caption: [{id: 'year', text: '2015', role: 'supplement'}], detail: [{id: 'remaining', text: '1 h 26 min left', role: 'supplement'}], progress: 0.14});
  const withoutYear = cardText({...film, year: undefined}, 'continuation', env);
  assert.deepEqual(withoutYear.caption, []);
  assert.deepEqual(parts(withoutYear.detail), [['remaining', '1 h 26 min left', 'supplement']]);
  for (const bad of [undefined, NaN, Infinity, -Infinity, -1, 0]) {
    const model = cardText({...film, durationSeconds: bad}, 'continuation', env);
    assert.deepEqual(parts(model.caption), [['year', '2015', 'supplement']]);
    assert.deepEqual(model.detail, []);
    assert.equal('progress' in model, false);
  }
  for (const bad of [undefined, NaN, Infinity, -Infinity, -1]) {
    const model = cardText({...film, positionSeconds: bad}, 'continuation', env);
    assert.deepEqual(parts(model.caption), [['year', '2015', 'supplement']]);
    assert.deepEqual(model.detail, []);
    assert.equal('progress' in model, false);
  }
});

test('continuation episode identity survives unknown numbering and omitted show without duplication', () => {
  const item = {id: 'episode-real', kind: 'episode', title: 'A long episode title · with provider punctuation', show: {title: 'A long show title'}, episode: {mode: 'seasonal', season: 1, episode: 2}, durationSeconds: 3180, positionSeconds: 1740} as const;
  const model = cardText(item, 'continuation', env);
  assert.equal(model.title, item.show.title);
  assert.deepEqual(parts(model.caption), [['episode-label', 'S1 E2', 'context'], ['episode-title', item.title, 'identity']]);
  assert.deepEqual(parts(model.detail), [['remaining', '24 min left', 'supplement']]);
  assert.deepEqual(parts(cardText({...item, episode: undefined}, 'continuation', env).caption), [['episode-title', item.title, 'identity']]);
  const withoutShow = cardText({...item, show: undefined}, 'continuation', env);
  assert.equal(withoutShow.title, item.title);
  assert.deepEqual(parts(withoutShow.caption), [['episode-label', 'S1 E2', 'context']]);
  assert.deepEqual(parts(withoutShow.detail), [['remaining', '24 min left', 'supplement']]);
  assert.deepEqual(parts(cardText({...item, title: item.show.title}, 'continuation', env).caption), [['episode-label', 'S1 E2', 'context']]);
});

test('song rows separate artist, album, year, counts and clocks; dock stays quiet', () => {
  const song = {id: 'song', kind: 'song', title: 'DNA.', artist: 'Kendrick Lamar', album: 'DAMN.', year: 2017, durationSeconds: 185} as const;
  assert.deepEqual(parts(cardText(song, 'song-row', env).caption), [['artist', 'Kendrick Lamar', 'identity'], ['album', 'DAMN.', 'context'], ['year', '2017', 'supplement']]);
  assert.deepEqual(parts(cardText(song, 'song-row', env).detail), [['duration', '3:05', 'supplement']]);
  assert.deepEqual(parts(cardText(song, 'dock', env).caption), [['artist', 'Kendrick Lamar', 'identity']]);
  assert.deepEqual(cardText(song, 'dock', env).detail, []);
  assert.deepEqual(parts(cardText({...song, kind: 'album', songCount: 4, durationSeconds: 900}, 'ordinary', env).detail), [['song-count', '4 songs', 'supplement'], ['duration', '15 min', 'supplement']]);
});

test('music and audiobook title facts follow their own metadata order with real counts only', () => {
  assert.deepEqual(parts(titleFactParts({artist: 'The Weeknd', year: 2020, genres: ['R&B/Soul'], songCount: 4, durationSeconds: 900}, env, 'music')), [['artist', 'The Weeknd', 'identity'], ['year', '2020', 'value'], ['genre-0', 'R&B/Soul', 'supplement'], ['song-count', '4 songs', 'supplement'], ['duration', '15 min', 'value']]);
  assert.deepEqual(parts(titleFactParts({author: 'J.K. Rowling', narrator: 'Stephen Fry', durationSeconds: 29580, chapterCount: 17}, env, 'audiobook')), [['author', 'J.K. Rowling', 'identity'], ['narrator', 'Read by Stephen Fry', 'context'], ['duration', '8 h 13 min', 'value'], ['chapter-count', '17 chapters', 'supplement']]);
  assert.deepEqual(parts(titleFactParts({narrator: 'Stephen Fry', durationSeconds: 29580}, env, 'audiobook')), [['narrator', 'Read by Stephen Fry', 'context'], ['duration', '8 h 13 min', 'value']]);
  assert.deepEqual(titleFactParts({author: '', narrator: ' ', durationSeconds: NaN, chapterCount: 0}, env, 'audiobook'), []);
});

// desktop-listen#8, :697 caption and :707 facts: author, narrator, length, chapters only.
test('book-page facts omit supplied year and genres without changing music or video metadata', () => {
  const facts = {author: 'J.K. Rowling', narrator: 'Stephen Fry', year: 1997, genres: ['Fantasy', 'Adventure'], durationSeconds: 29580, chapterCount: 17};
  assert.deepEqual(parts(titleFactParts(facts, env, 'audiobook')), [['author', 'J.K. Rowling', 'identity'], ['narrator', 'Read by Stephen Fry', 'context'], ['duration', '8 h 13 min', 'value'], ['chapter-count', '17 chapters', 'supplement']]);
  assert.deepEqual(titleFactParts({year: facts.year, genres: facts.genres}, env, 'audiobook'), []);
  assert.deepEqual(parts(titleFactParts(facts, env)), [['year', '1997', 'value'], ['duration', '8 h 13 min', 'value'], ['genre-0', 'Fantasy', 'supplement'], ['genre-1', 'Adventure', 'supplement']]);
  assert.deepEqual(parts(titleFactParts({artist: 'Artist', year: facts.year, genres: facts.genres}, env, 'music')), [['artist', 'Artist', 'identity'], ['year', '1997', 'value'], ['genre-0', 'Fantasy', 'supplement'], ['genre-1', 'Adventure', 'supplement']]);
});

test('song counts never become album counts; unknown counts and metadata disappear', () => {
  assert.deepEqual(parts(cardText({id: 'a', kind: 'artist', title: 'The Weeknd', songCount: 8}, 'ordinary', env).caption), [['song-count', '8 songs', 'supplement']]);
  assert.deepEqual(parts(cardText({id: 'a', kind: 'artist', title: 'The Weeknd', albumCount: 1, songCount: 4}, 'ordinary', env).caption), [['album-count', '1 album', 'supplement'], ['song-count', '4 songs', 'supplement']]);
  assert.deepEqual(cardText({id: 'a', kind: 'artist', title: 'Artist', albumCount: 0, songCount: NaN}, 'ordinary', env).caption, []);
  assert.equal(cardText({id: 'c', kind: 'collection', title: 'Collection', itemCount: 10000, countIsLowerBound: true}, 'ordinary', env).caption[0]?.text, '10,000+ items');
  assert.deepEqual(cardText({id: 'c', kind: 'category', title: 'Category'}, 'ordinary', env).caption, []);
});

test('author and book-series cards use genuine singular/plural book counts without substitutions', () => {
  for (const kind of ['author', 'book_series'] as const) {
    for (const [bookCount, text] of [[1, '1 book'], [2, '2 books']] as const) assert.deepEqual(parts(cardText({id: 'a', kind, title: 'Andy Weir', bookCount}, 'ordinary', env).caption), [['book-count', text, 'supplement']]);
    for (const bookCount of [undefined, 0, -1, NaN, Infinity, -Infinity, 1.5]) assert.deepEqual(cardText({id: 'a', kind, title: 'Author', bookCount}, 'ordinary', env).caption, []);
    assert.deepEqual(parts(cardText({id: 'a', kind, title: 'Author', itemCount: 2}, 'ordinary', env).caption), [['item-count', '2 items', 'supplement']]);
  }
});

test('audiobook continuation keeps author then chapter and whole-book remaining without narrator or total', () => {
  for (const kind of ['book', 'audiobook_file', 'chapter'] as const) {
    const item = {id: 'book', kind, title: 'The Martian', author: 'Andy Weir', narrator: 'R.C. Bray', chapterNumber: 6, chapterCount: 19, durationSeconds: 36000, positionSeconds: 19080};
    const model = cardText(item, 'continuation', env);
    assert.deepEqual(parts(model.caption), [['author', 'Andy Weir', 'identity']]);
    assert.deepEqual(parts(model.detail), [['chapter', 'Chapter 6', 'context'], ['remaining', '4 h 42 min left', 'supplement']]);
    assert.deepEqual(cardText(item, 'ordinary', env).detail, []);
    for (const chapterNumber of [undefined, 0, -1, NaN, Infinity, -Infinity, 1.5]) assert.deepEqual(parts(cardText({...item, chapterNumber}, 'continuation', env).detail), [['remaining', '4 h 42 min left', 'supplement']]);
    assert.deepEqual(parts(cardText({...item, author: undefined, durationSeconds: undefined}, 'continuation', env).detail), [['chapter', 'Chapter 6', 'context']]);
  }
  assert.deepEqual(cardText({id: 'b', kind: 'book', title: 'No metadata'}, 'ordinary', env).caption, []);
});

test('only the explicit audiobook chapter-list summary includes Chapter N of total', () => {
  const facts = {author: 'Andy Weir', narrator: 'R.C. Bray', chapterNumber: 15, chapterCount: 26};
  assert.deepEqual(parts(titleFactParts(facts, env, 'audiobook-chapters')), [['chapter-count', '26 chapters', 'supplement'], ['chapter', 'Chapter 15 of 26', 'context']]);
  assert.deepEqual(parts(titleFactParts(facts, env, 'audiobook')), [['author', 'Andy Weir', 'identity'], ['narrator', 'Read by R.C. Bray', 'context'], ['chapter-count', '26 chapters', 'supplement']]);
  for (const chapterNumber of [undefined, 0, -1, NaN, Infinity, -Infinity, 1.5]) assert.deepEqual(parts(titleFactParts({...facts, chapterNumber}, env, 'audiobook-chapters')), [['chapter-count', '26 chapters', 'supplement']]);
  for (const chapterCount of [undefined, 0, -1, NaN, Infinity, -Infinity, 1.5]) assert.deepEqual(parts(titleFactParts({...facts, chapterCount}, env, 'audiobook-chapters')), [['chapter', 'Chapter 15', 'context']]);
  assert.deepEqual(parts(titleFactParts({...facts, chapterCount: 10}, env, 'audiobook-chapters')), [['chapter-count', '10 chapters', 'supplement'], ['chapter', 'Chapter 15', 'context']]);
});

test('dock card and playback identity agree on chapter then author and retain no continuation facts', () => {
  for (const kind of ['book', 'audiobook_file', 'chapter'] as const) {
    const item = {id: 'book-real', kind, title: 'The Martian', author: 'Andy Weir', narrator: 'R.C. Bray', chapterNumber: 15, chapterCount: 26, durationSeconds: 36000, positionSeconds: 19080};
    const identity = playbackIdentity({kind: 'audiobook', item}, 'dock', env);
    assert.deepEqual(parts(identity.subtitle), [['chapter', 'Chapter 15', 'context'], ['author', 'Andy Weir', 'identity']]);
    assert.deepEqual(cardText(item, 'dock', env), {id: identity.id, title: identity.title, caption: identity.subtitle, detail: [], progress: 19080 / 36000});
    assert.deepEqual(parts(playbackIdentity({kind: 'audiobook', item}, 'full', env).subtitle), [['author', 'Andy Weir', 'identity'], ['narrator', 'Read by R.C. Bray', 'context']]);
    for (const chapterNumber of [undefined, 0, -1, NaN, Infinity, -Infinity, 1.5]) {
      const sparse = {...item, chapterNumber};
      const subtitle = playbackIdentity({kind: 'audiobook', item: sparse}, 'dock', env).subtitle;
      assert.deepEqual(parts(subtitle), [['author', 'Andy Weir', 'identity']]);
      assert.deepEqual(cardText(sparse, 'dock', env).caption, subtitle);
    }
    assert.deepEqual(parts(playbackIdentity({kind: 'audiobook', item: {...item, author: undefined}}, 'dock', env).subtitle), [['chapter', 'Chapter 15', 'context']]);
  }
  for (const item of [
    {id: 'e', kind: 'episode', title: 'Infected', show: {title: 'The Last of Us'}, episode: {mode: 'seasonal', season: 1, episode: 2}},
    {id: 'm', kind: 'movie', title: 'Inside Out', year: 2015},
    {id: 's', kind: 'song', title: 'Dreams', artist: 'Fleetwood Mac', album: 'Rumours', year: 1977},
  ] as const) {
    const identity = playbackIdentity({kind: item.kind === 'song' ? 'music' : 'video', item}, 'dock', env);
    assert.deepEqual(cardText(item, 'dock', env), {id: identity.id, title: identity.title, caption: identity.subtitle, detail: []});
  }
});

for (const bad of [undefined, NaN, Infinity, -Infinity, -1, 0]) test(`unknown or invalid duration ${String(bad)} invents no runtime/progress`, () => {
  assert.deepEqual(timelineFacts(bad, 60), {});
  const m = cardText({id: 'm', kind: 'movie', title: 'Film', durationSeconds: bad, positionSeconds: 60}, 'continuation', env);
  assert.deepEqual(m.caption, []); assert.deepEqual(m.detail, []); assert.equal('progress' in m, false);
});
for (const bad of [undefined, NaN, Infinity, -Infinity, -1]) test(`unknown or invalid position ${String(bad)} invents no remaining/progress`, () => {
  assert.deepEqual(timelineFacts(3600, bad), {});
});
test('real zero, overshoot and sub-minute timelines are bounded truthfully', () => {
  assert.deepEqual(timelineFacts(3600, 0), {progress: 0, remainingSeconds: 3600});
  assert.deepEqual(timelineFacts(3600, 4000), {progress: 1, remainingSeconds: 0});
  assert.deepEqual(cardText({id: 'm', kind: 'movie', title: 'Film', durationSeconds: 3600, positionSeconds: 0}, 'continuation', env).caption, []);
  assert.equal(cardText({id: 'm', kind: 'movie', title: 'Film', durationSeconds: 60, positionSeconds: 40}, 'continuation', env).detail[0]?.text, '20 s left');
});

test('full and dock identities preserve long show/episode names without renderer truncation', () => {
  const show = 'The Inheritance: A Very Long Show Title That Should Truncate Gracefully In Every Player';
  const title = 'An episode with a long provider title · punctuation intact';
  for (const context of ['full', 'dock'] as const) assert.deepEqual(playbackIdentity({kind: 'video', item: {id: 'e', kind: 'episode', title, show: {id: 's', title: show}, episode: {mode: 'seasonal', season: 1, episode: 2}}}, context, env), {id: 'e', kind: 'video', title: show, subtitle: [{id: 'episode-label', text: 'S1 E2', role: 'context'}, {id: 'episode-title', text: title, role: 'identity'}]});
  assert.equal(cardText({id: 'a', kind: 'album', title: 'Long title', artist: title}, 'ordinary', env).caption[0]?.text, title);
});

test('music, audiobook, VOD and true channel identities have distinct supplied facts', () => {
  const item = {id: 's', kind: 'song', title: 'Dreams', artist: 'Fleetwood Mac', album: 'Rumours'} as const;
  assert.deepEqual(parts(playbackIdentity({kind: 'music', item}, 'full', env).subtitle), [['artist', 'Fleetwood Mac', 'identity'], ['album', 'Rumours', 'context']]);
  assert.deepEqual(parts(playbackIdentity({kind: 'music', item}, 'dock', env).subtitle), [['artist', 'Fleetwood Mac', 'identity']]);
  assert.deepEqual(playbackIdentity({kind: 'video', item: {id: 'f', kind: 'movie', title: 'Dune'}}, 'dock', env).subtitle, []);
  const channel = {kind: 'channel', id: 'c1', sourceId: 's1', channelName: 'Summit Sports', programmeTitle: 'Cycling: Mountain Stage'} as const;
  assert.deepEqual(playbackIdentity(channel, 'dock', env), {id: 'c1', sourceId: 's1', kind: 'channel', title: 'Cycling: Mountain Stage', subtitle: [{id: 'channel', text: 'Summit Sports', role: 'identity'}]});
  assert.equal(playbackIdentity({...channel, live: true}, 'full', env).subtitle.at(-1)?.text, 'Live');
  assert.equal(playbackIdentity({...channel, programmeTitle: undefined}, 'full', env).title, 'Summit Sports');
  assert.deepEqual(parts(playbackIdentity({kind: 'audiobook', item: {id: 'b', kind: 'book', title: 'The Hobbit', author: 'J. R. R. Tolkien', narrator: 'Andy Serkis'}}, 'full', env).subtitle), [['author', 'J. R. R. Tolkien', 'identity'], ['narrator', 'Read by Andy Serkis', 'context']]);
});

test('Reading card identity never acquires audiovisual runtime, narration or progress',()=>{
 for(const kind of ['ebook','comic'] as const)for(const context of ['ordinary','continuation','dock'] as const){
  const text=cardText({id:'reading',kind,title:'Actual title',author:'Accepted author',year:2026,durationSeconds:1000,positionSeconds:400,narrator:'Unrelated audio narrator'},context,env);
  assert.equal(text.title,'Actual title');
  assert.deepEqual(parts(text.caption),[['author','Accepted author','identity']]);
  assert.deepEqual(parts(text.detail),[['year','2026','value']]);
  assert.equal(text.progress,undefined);
 }
});
