import React, {useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {AmbientArt, Artwork, PersonCircle, StandardBackdrop, missingArt, useWholeBox} from '../../../kit/artwork';
import {CoverHero} from '../../../kit/layout/CoverHero';
import {Button, Focusable} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {MusicScrollView, MusicWindow} from '../music/MusicWindow';
import {alpha, browseType} from '../../../kit/browse/presentation';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../entry-actions';
import type {AudiobookScreenController} from './model';
import {AudiobookErrorState} from './BookScreen';
import {audiobookAuthorInitials} from './presentation';
import {TopBar, RoundBarButton} from '../../../kit/layout/TopBar';

const t = defaultI18n.t;
type Actions = Pick<NativeEntryActions, 'menu'>;
const seconds = (value: number | undefined) => value === undefined ? undefined : defaultI18n.duration(value);

function BookRow({entry, runtime, tv, position, separated, focusId, onOpen, onPlay, onMore, onFocus}: {
  entry: ContentEntry; runtime: NativeShellRuntime; tv: boolean; position?: string; separated: boolean; focusId: string;
  onOpen: () => void; onPlay: () => void; onMore: () => void; onFocus: () => void;
}) {
  const theme = useTheme(), art = runtime.artwork(entry.posterUrl), progress = entry.bookProgress;
  const slot = tv ? 128 : 66, {box, onSize} = useWholeBox(tv ? undefined : art, slot, slot);
  const left = progress?.remainingMs === undefined ? undefined : seconds(progress.remainingMs / 1000);
  const readBy = entry.narrators?.length ? t('next.kit.readBy', {name: defaultI18n.list(entry.narrators)}) : undefined;
  // The book's own bar is book-wide only: a part's position is not a fraction of
  // the whole book, so it never feeds this.
  const ratio = progress?.positionMs !== undefined && progress.durationMs ? progress.positionMs / progress.durationMs : undefined;
  // collections 44/45 draw the place in small type under the title. The server's
  // chapter index is file-local, so a book-wide "chapter N of M" is not derived
  // from it; the chapter's own title is the place the server actually sent.
  const place = progress?.chapter?.title;
  // collections 41b/41c draw who read it and the time left on the phone; 44/45
  // add the place and the whole length on TV.
  const tvLine = [place, left ? t('next.kit.remaining', {time: left}) : undefined].filter(Boolean).join(', ');
  // TV 44/45: OK plays the book, hold OK opens its menu; the rows carry no button.
  // The phone keeps its own row button and opens the book page.
  return <Focusable focusId={focusId} accessibilityRole="button" accessibilityLabel={entry.title} focusKind="row" focusRadius={tv ? 18 : 10}
    onFocus={onFocus} onPress={tv ? onPlay : onOpen} onLongPressAction={tv ? onMore : undefined}
    style={tv ? {height: 168, paddingHorizontal: 20, borderRadius: 18, flexDirection: 'row', alignItems: 'center', gap: 32}
      : {height: '100%', flexDirection: 'row', alignItems: 'center', gap: 14, paddingVertical: 10, marginRight: -6}}>
    {!tv && separated ? <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 80, right: 0, height: 1, backgroundColor: alpha(theme.colors.line, .35)}}/> : null}
    {/* A cover is never cropped: in its 66 pt slot it keeps its own shape, whole and centred (TV keeps its square). */}
    <View style={{width: slot, height: slot, alignItems: 'center', justifyContent: 'center'}}>
    <View style={{...box, borderRadius: tv ? 10 : 7, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
      <Artwork source={art} width={box.width} height={box.height} onSize={onSize} fallback={art ? undefined : missingArt({title: entry.title, shape: 'square', width: slot, height: slot})}/>
      {ratio === undefined ? null : <View style={{position: 'absolute', left: tv ? 8 : 5, right: tv ? 8 : 5, bottom: tv ? 8 : 5, height: tv ? 6 : 3, borderRadius: 3, overflow: 'hidden', backgroundColor: alpha(theme.colors.ink, .55)}}>
        <View style={{width: `${Math.max(0, Math.min(1, ratio)) * 100}%`, height: tv ? 6 : 3, backgroundColor: theme.colors.accentStrong}}/>
      </View>}
    </View>
    </View>
    <View style={{flex: 1, minWidth: 0, gap: tv ? 5 : 3}}>
      {position ? <Text numberOfLines={1} style={[browseType(theme, tv ? 20 : 12, 700), {color: theme.colors.accentStrong}]}>{t('next.reader.bookNumber', {number: position})}</Text> : null}
      <Text numberOfLines={tv ? 1 : 2} style={[browseType(theme, tv ? 32 : 15.5, 700, tv ? 0 : -.01), {color: theme.colors.text}]}>{entry.title}</Text>
      {tv ? <>
        {readBy ? <Text numberOfLines={1} style={[browseType(theme, 22, 400), {color: theme.colors.textTertiary}]}>{readBy}</Text> : null}
        {tvLine ? <Text numberOfLines={1} style={[browseType(theme, 22, 600), {color: theme.colors.accentStrong}]}>{tvLine}</Text> : null}
      </> : readBy || left ? <View style={{flexDirection: 'row', alignItems: 'baseline', columnGap: 8}}>
        {readBy ? <Text numberOfLines={1} style={[browseType(theme, 12.5, 400), {color: theme.colors.textTertiary, flexShrink: 1}]}>{readBy}</Text> : null}
        {left ? <Text style={[browseType(theme, 12.5, 700), {color: theme.colors.accentStrong}]}>{t('next.kit.remaining', {time: left})}</Text> : null}
      </View> : null}
    </View>
    {tv && entry.duration !== undefined ? <Text style={[browseType(theme, 24, 400), {width: 200, textAlign: 'right', color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{seconds(entry.duration)}</Text> : null}
    {!tv ? <Button variant="icon" icon="i-more" iconOnly accessibilityLabel={t('card.more', {title: entry.title})} onPress={onMore}
      style={{height: 36, width: 36}}/> : null}
  </Focusable>;
}

/** Frames 41b/41c, 44 and 45: the canonical author and series book rows. */
export function AuthorSeriesScreen({controller, entry, runtime, actions, openEntry, onBack}: {
  controller: AudiobookScreenController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions;
  openEntry: (entry: ContentEntry) => void; onBack?: () => void;
}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), insets = useSafeAreaInsets();
  const entity = snapshot.entity ?? entry, tv = theme.isTV;
  const failed = snapshot.phase === 'error' || snapshot.phase === 'refresh-required';
  const books = snapshot.sections.find(section => section.id === 'books')?.entries ?? [];
  // collections 41c/45: the series page's author opens the author destination.
  // The entry exists only when the projection named a real author identity.
  const authorEntry = (snapshot.sections.find(section => section.id === 'context')?.entries ?? []).find(value => value.kind === 'author');
  const covers = books.flatMap(book => {const art = runtime.artwork(book.posterUrl); return art ? [art] : [];});
  // collections 45: Resume continues the book this profile is actually in — the
  // listening journey names it, and a book's own progress names its part. Neither
  // is guessed from order.
  const listeningId = snapshot.listening?.resume?.itemId;
  const resume = (listeningId === undefined ? undefined : books.find(book => book.bookProgress?.itemId === listeningId))
    ?? books.find(book => book.bookProgress?.itemId);
  const series = entry.kind === 'book_series';
  const author = series ? books[0]?.author ?? books[0]?.subtitle : undefined;
  const narrators = [...new Set([...(entity.narrators ?? []), ...books.flatMap(book => book.narrators ?? [])])];
  const facts = [entity.count === undefined ? undefined : t('next.kit.books', {count: entity.count}),
    entity.duration === undefined ? undefined : seconds(entity.duration)].filter((value): value is string => !!value);
  const coverPath = series ? books[0]?.posterUrl ?? entity.posterUrl : entity.posterUrl;
  const title = entity.displayTitle ?? entity.title;
  // A file-local chapter index is not a book-wide chapter number, so the drawn
  // label names the book, exactly as frame 44 draws "Resume The Martian".
  const resumeLabel = resume ? t('next.browse.resume', {title: resume.title}) : t('entry.play');
  const tail = () => {void controller.next('books');};
  const open = (book: ContentEntry) => openEntry(book);
  const start = () => {if (resume) void controller.resumeBook(resume).catch(error => runtime.reportError(error)); else if (books[0]) open(books[0]);};
  // A row plays the book it names when this profile already has a place in it;
  // with no place there is nothing to resume, so the row opens the book page,
  // which is where the first chapter can be started.
  const playBook = (book: ContentEntry) => book.bookProgress?.itemId
    ? void controller.resumeBook(book).catch(error => runtime.reportError(error)) : open(book);
  const regionId = `audiobook-${entry.id}`, resumeFocusId = `${regionId}.resume`, startFocusId = `${regionId}.start`,
    moreFocusId = `${regionId}.more`, bookFocus = `${regionId}.book`, retryFocusId = `${regionId}.retry`;
  // collections 44/45 draw the first rows at 560; a longer shelf keeps its rows
  // reachable by growing the canvas instead of clipping them at the frame's height.
  const canvasHeight = Math.max(1120, 560 + books.length * 168 + Math.max(0, books.length - 1) * 10 + 64);
  // Room for the book number, a two-line title (titles wrap before they are cut) and who read it.
  const phoneRow = Math.ceil(100 * Math.max(1, theme.fontScale));
  // TV keeps its own composition (collections 44/45); the TV lane moves it onto the kit's cover hero.
  const tvHero = <>
    {series ? <View style={{position: 'absolute', left: 200, top: 120, width: 380, height: 380}}>
      {runtime.ambient(coverPath) ? <View pointerEvents="none" style={{position: 'absolute', left: -26, top: -26, width: 432, height: 432, borderRadius: 18, overflow: 'hidden'}}>
        <AmbientArt source={runtime.ambient(coverPath)} treatment="cover"/>
      </View> : null}
      <View style={{width: 380, height: 380, borderRadius: 16, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
        <Artwork source={runtime.artwork(coverPath)} width={380} height={380} priority fallback={runtime.artwork(coverPath) ? undefined : missingArt({title, shape: 'square', width: 380, height: 380})}/>
      </View>
    </View> : <View style={{position: 'absolute', left: 200, top: 120}}><PersonCircle covers={covers} initials={[...title.trim()][0] ?? ''} alt={title} size={380} visible/></View>}
    <View style={{position: 'absolute', left: 700, top: 150, width: 1040}}>
      <Text style={[browseType(theme, 24, 700), {color: theme.colors.textSecondary, textAlign: 'left'}]}>{t('title.kind', {kind: entry.kind})}</Text>
      <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, 76, 800, -.04), {color: theme.colors.text, textAlign: 'left', marginTop: 8, lineHeight: undefined}]}>{title}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'flex-start', alignItems: 'center', columnGap: 18, rowGap: 6, marginTop: 10}}>
        {author || authorEntry ? (authorEntry && openEntry
          ? <Focusable focusId="series-author-link" accessibilityRole="link" accessibilityLabel={authorEntry.title} onPress={() => openEntry(authorEntry)} style={{paddingVertical: 2}}>
            {({focused}) => <Text style={[browseType(theme, 24, 700), {color: theme.colors.text, textDecorationLine: focused ? 'underline' : 'none'}]}>{author ?? authorEntry.title}</Text>}
          </Focusable>
          : <Text style={[browseType(theme, 24, 700), {color: theme.colors.text}]}>{author}</Text>) : null}
        {facts.map((fact, index) => <Text key={fact} style={[browseType(theme, 24, index === 0 ? 700 : 400), {color: index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
      </View>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginTop: 26, alignSelf: 'flex-start'}}>
        <Button focusId={resumeFocusId} icon="i-play" disabled={failed || !books.length} onPress={start} accessibilityLabel={resumeLabel} preferredFocus style={{flex: undefined, minWidth: 320}}>{resumeLabel}</Button>
        {series && books[0] ? <Button focusId={startFocusId} variant="glass" icon="i-restart" iconOnly={false} onPress={() => open(books[0]!)} accessibilityLabel={t('action.startSeriesOver')}>{t('action.startSeriesOver')}</Button> : null}
        <Button focusId={moreFocusId} variant="glass" iconOnly icon="i-more" accessibilityLabel={t('card.more', {title})} onPress={() => actions.menu(entity)}/>
      </View>
    </View>
  </>;
  // collections 41b/41c, phone-app r5: the phone's cover hero is the kit's: a series' square cover, or
  // the author's circle with their initials over their books' covers.
  const phoneHero = <CoverHero layout="stack" shape={series ? 'square' : 'person'} title={title}
    covers={series ? [{key: 'cover', source: runtime.artwork(coverPath), fallback: missingArt({title, shape: 'square', width: 232, height: 232})}] : undefined}
    glow={series ? runtime.ambient(coverPath) : undefined} person={series ? undefined : {covers, initials: audiobookAuthorInitials(title)}}
    eyebrow={{label: t('title.kind', {kind: entry.kind})}}
    byline={author || authorEntry ? {label: author ?? authorEntry?.title ?? '', focusId: 'series-author-link', onPress: authorEntry && openEntry ? () => openEntry(authorEntry) : undefined} : undefined}
    facts={facts}
    actions={<View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      <Button focusId={resumeFocusId} icon="i-play" disabled={failed || !books.length} onPress={start} accessibilityLabel={resumeLabel} style={{flex: 1}}>{resumeLabel}</Button>
      {series && books[0] ? <Button focusId={startFocusId} variant="glass" icon="i-restart" iconOnly onPress={() => open(books[0]!)} accessibilityLabel={t('action.startOver')}/> : null}
    </View>}/>;
  const body = <>
    {tv ? tvHero : phoneHero}
    <View style={tv ? {position: 'absolute', left: 700, top: 560, width: 1040} : {width: '100%', marginTop: 22}}>
      {failed ? <AudiobookErrorState refresh={snapshot.phase === 'refresh-required'} error={snapshot.error} actionId={retryFocusId} preferred={tv} retry={() => {void controller.load();}}/> : null}
      {snapshot.continuation?.phase === 'loading' ? <Text accessibilityLiveRegion="polite" style={[browseType(theme, tv ? 22 : 14, 400), {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text> : null}
      {snapshot.continuation && snapshot.continuation.phase !== 'loading' ? <AudiobookErrorState refresh={snapshot.continuation.phase === 'refresh-required'} error={snapshot.continuation.error} actionId={`${regionId}.continuation`} retry={() => {void controller.recover();}}/> : null}
      {/* collections 44/45 draw the rows without a section heading; 41b/41c
          keep the heading above the list on the phone. */}
      {!tv ? <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 10, marginBottom: 6}}>
        <Text style={[browseType(theme, 20, 700), {color: theme.colors.text}]}>{t(series ? 'next.kit.booksInSeries' : 'next.kit.booksHeading')}</Text>
        <Text style={[browseType(theme, 14, 600), {color: theme.colors.textTertiary}]}>{entity.count ?? books.length}</Text>
      </View> : null}
      {/* collections 41c/45: the rows keep the server's own order and only a
          server-sent series position is labelled; nothing is numbered by index. */}
      {/* LR05: only the rows in view are mounted (fixed 168/86 rows, 10 apart on TV). */}
      <MusicWindow items={books} itemHeight={tv ? 168 : phoneRow} gap={tv ? 10 : 0} renderItem={(book, index) =>
        <BookRow entry={book} runtime={runtime} tv={tv} separated={index > 0} focusId={`${bookFocus}-${book.id}`}
          onFocus={() => {if (index >= books.length - 3) tail();}} position={series ? book.series?.[0]?.position : undefined} onOpen={() => open(book)} onPlay={() => playBook(book)} onMore={() => actions.menu(book)}/>}/>
      {!tv && !series && (narrators.length || entity.duration !== undefined) ? <View style={{marginTop: 20}}>
        <Text style={[browseType(theme, 20, 700), {color: theme.colors.text, marginBottom: 4}]}>{t('title.details')}</Text>
        <View style={{marginHorizontal: -4, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
          {narrators.length ? <View style={{minHeight: 48, paddingVertical: 8, paddingHorizontal: 14, justifyContent: 'center'}}>
            <Text style={[browseType(theme, 15.5, 600), {color: theme.colors.text}]}>{t('next.kit.narrators')}</Text>
            <Text style={[browseType(theme, 12.5, 500), {color: theme.colors.textTertiary, marginTop: 2, lineHeight: 17.5}]}>{defaultI18n.list(narrators)}</Text>
          </View> : null}
          {entity.duration === undefined ? null : <View style={{minHeight: 48, paddingVertical: 8, paddingHorizontal: 14, justifyContent: 'center'}}>
            {narrators.length ? <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 14, right: 0, height: 1, backgroundColor: alpha(theme.colors.line, .45)}}/> : null}
            <Text style={[browseType(theme, 15.5, 600), {color: theme.colors.text}]}>{t('next.kit.length')}</Text>
            <Text style={[browseType(theme, 12.5, 500), {color: theme.colors.textTertiary, marginTop: 2, lineHeight: 17.5}]}>{seconds(entity.duration)}</Text>
          </View>}
        </View>
      </View> : null}
    </View>
  </>;
  return <View testID={tv ? 'native-tv-audiobook-collection' : 'native-phone-audiobook-collection'} style={{flex: 1}}>{theme.isTV && <StandardBackdrop/>}
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, overflow: 'hidden'}}>
      {runtime.ambient(coverPath) ? <AmbientArt source={runtime.ambient(coverPath)} treatment="title" style={{position: 'absolute', inset: 0}}/> : null}
      <View style={{position: 'absolute', inset: 0, backgroundColor: alpha(theme.colors.ink, .45)}}/>
    </View>
    {!tv && <View pointerEvents="box-none" style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: 10}}>
      <TopBar overArtwork back={onBack ? {label: t('action.back'), onPress: onBack} : undefined}
        trailing={<RoundBarButton icon="i-more" label={t('card.more', {title})} onPress={() => actions.menu(entity)}/>}/>
    </View>}
    <MusicScrollView onNearEnd={tail} contentContainerStyle={tv ? {paddingBottom: 64} : {paddingTop: Math.max(96, insets.top + 56), paddingBottom: 48}}>
      {tv ? <FocusRegion id={regionId} entry={failed ? retryFocusId : resumeFocusId} orientation="free" style={{width: 1920, height: canvasHeight}}>{body}</FocusRegion>
        : <View style={{alignItems: 'center', paddingHorizontal: 20}}>{body}</View>}
    </MusicScrollView>
  </View>;
}
