import React, {useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {AmbientArt, Artwork, StandardBackdrop} from '../../../kit/artwork';
import {Button, Focusable} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {MusicScrollView, MusicWindow} from '../music/MusicWindow';
import {Icon} from '../../../kit/Icon';
import {StateView} from '../../../kit/states';
import {alpha, browseType} from '../../../kit/browse/presentation';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryActions} from '../entry-actions';
import {askNativeDownload} from '../../downloads/request';
import {nativeDownloadRequest} from '../../watch/download-request';
import type {AudiobookScreenController} from './model';

const t = defaultI18n.t;
type Actions = Pick<NativeEntryActions, 'menu'>;

const duration = (seconds: number | undefined) => seconds === undefined ? undefined : defaultI18n.duration(seconds);
const chapterDuration = (entry: ContentEntry) => duration(entry.duration);

export function AudiobookErrorState({error, retry, actionId, preferred, refresh}: {error: unknown; retry: () => void; actionId?: string; preferred?: boolean; refresh?: boolean}) {
  const presented = presentError(refresh ? {code: 'refresh_required'} : error, 'detail', {operation: 'load'});
  return <StateView variant="inline" title={presented.title} body={presented.body}
    {...(presented.action === 'try-again' || presented.action === 'refresh' ? {primaryAction: {id: 'retry', testID: actionId, hasTVPreferredFocus: preferred, label: presented.actionLabel ?? t('action.tryAgain'), onPress: retry}} : {})}/>;
}

function Cover({runtime, path, size}: {runtime: NativeShellRuntime; path: string | undefined; size: number}) {
  const source = runtime.artwork(path), ambient = runtime.ambient(path), theme = useTheme();
  // collections 41a/43: the cover sits in a glow taken from its own art. The
  // ambient variant is already blurred by the server, so no device runs a live
  // blur (the Android TV rule).
  return <View style={{width: size, height: size}}>
    {ambient ? <View pointerEvents="none" style={{position: 'absolute', left: -size * .07, top: -size * .07, width: size * 1.14, height: size * 1.14, borderRadius: 16, overflow: 'hidden'}}>
      <AmbientArt source={ambient} treatment="cover"/>
    </View> : null}
    <View style={{width: size, height: size, borderRadius: 16, overflow: 'hidden', backgroundColor: theme.colors.raised,
      shadowColor: theme.colors.shadow, shadowOffset: {width: 0, height: 24}, shadowOpacity: .85, shadowRadius: 28}}>
      {source ? <Artwork source={source} width={size} height={size} priority/> : null}
    </View>
  </View>;
}

function ChapterRows({controller, entries, currentId, fraction, runtime, tv, focusPrefix, onTail}: {
  controller: AudiobookScreenController; entries: readonly ContentEntry[]; currentId: string | undefined; fraction: number | undefined;
  runtime: NativeShellRuntime; tv: boolean; focusPrefix: string; onTail: () => void;
}) {
  const theme = useTheme(), playingIndex = currentId === undefined ? -1 : entries.findIndex(entry => entry.id === currentId);
  // LR05: only the rows in view are mounted (fixed 68/54 rows; TV draws the playing one at 84).
  return <View style={{marginTop: tv ? 14 : 12}}>
    <MusicWindow items={entries} itemHeight={tv ? 68 : 54} expanded={tv && playingIndex >= 0 ? {index: playingIndex, extra: 16} : undefined} renderItem={(entry, index) => {
      // collections 43: the row you are in is the one whose own id this profile
      // resumed — a chapter row is `file:chapter_index`, a part row is its file —
      // never a number counted across the book's files. The drawn number is the
      // list's own 1-based position.
      const playing = currentId !== undefined && entry.id === currentId, number = index + 1;
      // A finished chapter keeps its row but trades its number for the check and
      // dims. The server sends `watched` only when playback really passed that
      // chapter's end; nothing here infers it.
      const finished = entry.watched === true && !playing;
      return <Focusable key={entry.id} focusId={`${focusPrefix}-${entry.id}`} accessibilityRole="button"
        accessibilityLabel={finished ? `${entry.title}, ${t('title.finished')}` : entry.title} focusKind="row" focusRadius={tv ? 14 : 10}
        onFocus={() => {if (index >= entries.length - 3) onTail();}}
        onPress={() => {void controller.playChapter(entry).catch(error => runtime.reportError(error));}}
        style={tv ? {height: playing ? 84 : 68, paddingHorizontal: 22, borderRadius: 14, flexDirection: 'row', alignItems: 'center', gap: 22,
          backgroundColor: playing ? alpha(theme.colors.text, .06) : undefined}
          : {height: 54, paddingHorizontal: 4, flexDirection: 'row', alignItems: 'center', gap: 12, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.line, .7)}}>
        {finished
          ? <View style={{width: tv ? 34 : 24, alignItems: 'center'}}><Icon name="i-check" size={tv ? 20 : 15} color={theme.colors.textTertiary}/></View>
          : <Text style={[browseType(theme, tv ? 22 : 14, 600), {width: tv ? 34 : 24, color: playing ? theme.colors.accentStrong : theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{number}</Text>}
        <Text numberOfLines={1} style={[browseType(theme, tv ? 26 : 15.5, 600), {flex: 1, color: finished ? theme.colors.textTertiary : playing ? theme.colors.accentStrong : theme.colors.text}]}>{entry.title}</Text>
        {chapterDuration(entry) ? <Text style={[browseType(theme, tv ? 22 : 13.5, 400), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{chapterDuration(entry)}</Text> : null}
        {playing && fraction !== undefined ? <View style={tv
          ? {position: 'absolute', left: 78, right: 130, bottom: 12, height: 5, borderRadius: 5, overflow: 'hidden', backgroundColor: alpha(theme.colors.text, .18)}
          : {position: 'absolute', left: 4, right: 4, bottom: 0, height: 2, backgroundColor: alpha(theme.colors.accentStrong, .5)}}>
          <View style={{width: `${Math.max(0, Math.min(1, fraction)) * 100}%`, height: tv ? 5 : 2, backgroundColor: theme.colors.accentStrong}}/>
        </View> : null}
      </Focusable>;
    }}/>
  </View>;
}

/** The drawn phone (41a) and TV (43) book page. Real catalog entity, chapters,
 * resume and container finished mark; author/series links are text until their
 * pages exist, and nothing is invented for a missing fact. */
export function BookScreen({controller, entry, runtime, actions, openEntry, onBack}: {
  controller: AudiobookScreenController; entry: ContentEntry; runtime: NativeShellRuntime; actions: Actions;
  /** Opens an author or series destination the book's own projection names. */
  openEntry?: (entry: ContentEntry) => void; onBack?: () => void;
}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), insets = useSafeAreaInsets();
  const [aboutExpanded, setAboutExpanded] = React.useState(false), [aboutLong, setAboutLong] = React.useState(false);
  const entity = snapshot.entity ?? entry, tv = theme.isTV;
  const chapterSection = snapshot.sections.find(section => section.id === 'chapters'), partSection = snapshot.sections.find(section => section.id === 'files');
  const chapters = chapterSection?.entries ?? [], parts = partSection?.entries ?? [];
  const rows = chapters.length ? chapters : parts, rowsSection = chapters.length ? chapterSection : partSection;
  // collections 41/43: the projection's own Author & series entries carry the real
  // author and book_series navigation ids; a link exists only when the server sent one.
  const context = snapshot.sections.find(section => section.id === 'context')?.entries ?? [];
  const seriesEntry = context.find(value => value.kind === 'book_series');
  const authorEntry = context.find(value => value.kind === 'author');
  // collections 43: the row this profile is in is the row whose own identity it
  // resumed — a chapter row is `file:chapter_index`, a part row is its file. Its
  // place is measured inside that same file, never by counting chapters across files.
  const progress = entity.bookProgress;
  const currentId = progress ? (progress.chapter ? `${progress.itemId}:${progress.chapter.index}` : progress.itemId) : undefined;
  const currentRow = currentId === undefined ? -1 : rows.findIndex(row => row.id === currentId);
  const placeFraction = (() => {
    if (!progress || currentRow < 0) return undefined;
    const chapter = progress.chapter, rowDuration = rows[currentRow]?.duration;
    if (chapter && chapter.endMs > chapter.startMs) return (progress.partPositionMs - chapter.startMs) / (chapter.endMs - chapter.startMs);
    return rowDuration ? progress.partPositionMs / 1000 / rowDuration : undefined;
  })();
  // The book's own bar is book-wide only: the whole-book position against the
  // whole-book length. A part's position is not a fraction of the whole book.
  const progressRatio = progress?.positionMs !== undefined && progress.durationMs ? progress.positionMs / progress.durationMs : undefined;
  const remaining = progress?.remainingMs === undefined ? undefined : duration(progress.remainingMs / 1000);
  // collections 41a/43: who read it, how long it is and how many chapters; on TV the
  // author leads that row (frame 43's meta) and keeps its own link, while the phone
  // keeps the author on its own line.
  // A book's entity count is its container's item count — the book files — not
  // chapters (listening_entity.go counts catalog_book_files, then replaces the
  // count with ContainerPersonalCounts over those same files). So only a real
  // chapter list can name a chapter count; a parts book names none.
  const chapterCount = chapters.length ? chapterSection?.totalCount : undefined;
  const facts = [entity.narrators?.length ? t('next.kit.readBy', {name: defaultI18n.list(entity.narrators ?? [])}) : undefined,
    entity.duration === undefined ? undefined : duration(entity.duration), chapterCount === undefined ? undefined : t('title.chapterCount', {count: chapterCount})]
    .filter((value): value is string => !!value);
  const resume = snapshot.listening?.resume;
  const canResume = !!resume && !snapshot.listening?.resumeUnavailable;
  // The drawn label carries the number this page itself draws for that row.
  const rowNumber = currentRow >= 0 ? currentRow + 1 : undefined;
  const resumeLabel = canResume && rowNumber !== undefined && rows === chapters ? t('title.resumeChapterNumber', {number: rowNumber}) : t(canResume ? 'entry.resume' : 'entry.play');
  const onPlay = () => {void (canResume ? controller.resume() : controller.startOver()).catch(error => runtime.reportError(error));};
  const retry = () => {void controller.load();};
  const tail = () => {if (rowsSection) void controller.next(rowsSection.id);};
  // phone-player 41: Download sits beside Finished; mobile web has none, and the TV
  // page draws Resume, Start over and More only (collections 43).
  const download = nativeDownloadRequest(entity);
  const regionId = `book-${entry.id}`, resumeFocusId = `${regionId}.resume`, startFocusId = `${regionId}.start`,
    moreFocusId = `${regionId}.more`, chapterFocus = `${regionId}.chapter`;
  // collections 43 draws a slice of the list: a real book with more chapters than fit
  // still has to be reachable, so the canvas grows past its drawn 1120 rather than
  // clipping the rows below it.
  const chaptersHeight = rows.reduce((sum, row) => sum + (row.id === currentId ? 84 : 68), 0);
  const canvasHeight = Math.max(1120, 548 + 44 + chaptersHeight + 64);
  const progressRow = remaining ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginTop: tv ? 26 : 14, alignSelf: tv ? 'flex-start' : 'center'}}>
        {progressRatio === undefined ? null : <View style={{width: tv ? 380 : 120, height: 4, borderRadius: 2, backgroundColor: alpha(theme.colors.text, .18)}}>
          <View style={{width: `${Math.max(0, Math.min(1, progressRatio)) * 100}%`, height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/></View>}
        <Text style={[browseType(theme, tv ? 22 : 13, 600), {color: theme.colors.textSecondary}]}>{t('title.resumeRemaining', {remaining})}</Text>
      </View> : null;
  const body = <>
    <View style={tv ? {position: 'absolute', left: 200, top: 96} : undefined}><Cover runtime={runtime} path={entity.posterUrl} size={tv ? 420 : 232}/></View>
    <View style={tv ? {position: 'absolute', left: 700, top: 96, width: 1040} : {alignItems: 'center', marginTop: 20, width: '100%'}}>
      {entity.series?.[0] || seriesEntry ? <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: tv ? 'flex-start' : 'center', flexWrap: 'wrap'}}>
        {seriesEntry && openEntry
          ? <Focusable focusId="book-series-link" accessibilityRole="link" accessibilityLabel={seriesEntry.title} onPress={() => openEntry(seriesEntry)} style={{paddingVertical: 2}}>
            {({focused}) => <Text style={[browseType(theme, tv ? 24 : 13, 600), {color: theme.colors.accentStrong, textDecorationLine: focused ? 'underline' : 'none'}]}>{entity.series?.[0]?.name ?? seriesEntry.title}</Text>}
          </Focusable>
          : <Text style={[browseType(theme, tv ? 24 : 13, 600), {color: theme.colors.accentStrong}]}>{entity.series?.[0]?.name}</Text>}
        {entity.series?.[0]?.position ? <Text style={[browseType(theme, tv ? 24 : 13, 600), {color: theme.colors.textSecondary}]}>{t('title.bookInSeries', {number: entity.series[0].position})}</Text> : null}
      </View> : null}
      <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, tv ? 76 : 30, 800, -.04), {color: theme.colors.text, textAlign: tv ? 'left' : 'center', marginTop: tv ? 14 : 6}]}>{entity.displayTitle ?? entity.title}</Text>
      {!tv && (entity.author || authorEntry) ? (authorEntry && openEntry
        ? <Focusable focusId="book-author-link" accessibilityRole="link" accessibilityLabel={authorEntry.title} onPress={() => openEntry(authorEntry)} style={{marginTop: 8, paddingVertical: 2}}>
          {({focused}) => <Text style={[browseType(theme, 16, 700), {color: theme.colors.text, textAlign: 'center', textDecorationLine: focused ? 'underline' : 'none'}]}>{entity.author ?? authorEntry.title}</Text>}
        </Focusable>
        : <Text style={[browseType(theme, 16, 700), {color: theme.colors.text, textAlign: 'center', marginTop: 8}]}>{entity.author}</Text>) : null}
      <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: tv ? 'flex-start' : 'center', alignItems: 'center', columnGap: 18, rowGap: 6, marginTop: tv ? 18 : 10}}>
        {tv && (entity.author || authorEntry) ? (authorEntry && openEntry
          ? <Focusable focusId="book-author-link" accessibilityRole="link" accessibilityLabel={authorEntry.title} onPress={() => openEntry(authorEntry)} style={{paddingVertical: 2}}>
            {({focused}) => <Text style={[browseType(theme, 24, 700), {color: theme.colors.text, textDecorationLine: focused ? 'underline' : 'none'}]}>{entity.author ?? authorEntry.title}</Text>}
          </Focusable>
          : <Text style={[browseType(theme, 24, 700), {color: theme.colors.text}]}>{entity.author}</Text>) : null}
        {facts.map((fact, index) => <Text key={`${fact}-${index}`} style={[browseType(theme, tv ? 24 : 13, index === 0 && !tv ? 700 : 400),
          {color: index === 0 && !tv ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
      </View>
      {tv ? progressRow : null}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 18 : 10, marginTop: tv ? 26 : 18, alignSelf: tv ? 'flex-start' : 'stretch'}}>
        <Button focusId={resumeFocusId} icon="i-play" onPress={onPlay} accessibilityLabel={resumeLabel} hasTVPreferredFocus={tv} style={{flex: tv ? undefined : 1, minWidth: tv ? 320 : undefined}}>{resumeLabel}</Button>
        {tv ? <Button focusId={startFocusId} variant="glass" icon="i-restart" onPress={() => {void controller.startOver().catch(error => runtime.reportError(error));}} accessibilityLabel={t('action.startOver')}>{t('action.startOver')}</Button> : null}
        {!tv ? <Button variant="glass" iconOnly icon={'i-check'} accessibilityLabel={t('title.markFinished')}
          accessibilityState={{checked: snapshot.finished}} onPress={() => {void controller.markFinished().catch(error => runtime.reportError(error));}}/> : null}
        {!tv && download ? <Button variant="glass" iconOnly icon="i-dl" accessibilityLabel={t('action.download')}
          onPress={() => askNativeDownload(download)}/> : null}
        {tv ? <Button focusId={moreFocusId} variant="glass" iconOnly icon="i-more" accessibilityLabel={t('card.more', {title: entity.title})} onPress={() => actions.menu(entity)}/> : null}
      </View>
      {!tv ? progressRow : null}
    </View>
    <View style={tv ? {position: 'absolute', left: 700, top: 548, width: 1040} : {width: '100%', marginTop: 22}}>
      {snapshot.phase === 'error' ? <AudiobookErrorState error={snapshot.error} retry={retry}/> : null}
      {/* collections 41a/42a: the phone page shows the description and a
          Read more link; collections 43 keeps the description in Details. */}
      {!tv && entity.overview ? <View style={{marginBottom: 14}}>
        <Text accessible={false} style={[browseType(theme, 14.5, 400), {position: 'absolute', left: 0, right: 0, top: 0, opacity: 0, lineHeight: 23}]}
          onTextLayout={event => {const long = event.nativeEvent.lines.length > 3; if (long !== aboutLong) setAboutLong(long);}}>{entity.overview}</Text>
        <Text numberOfLines={aboutExpanded ? undefined : 3} style={[browseType(theme, 14.5, 400), {color: theme.colors.textSecondary, lineHeight: 23}]}>{entity.overview}</Text>
        {aboutLong ? <Focusable accessibilityRole="button" accessibilityLabel={t(aboutExpanded ? 'web.person.showLess' : 'web.person.readMore')} focusKind="button"
          onPress={() => setAboutExpanded(value => !value)} style={{marginTop: 6, paddingVertical: 3}}>
          {({focused}) => <Text style={[browseType(theme, 13.5, 600), {color: theme.colors.accentStrong, textDecorationLine: focused ? 'underline' : 'none'}]}>{t(aboutExpanded ? 'web.person.showLess' : 'web.person.readMore')}</Text>}
        </Focusable> : null}
      </View> : null}
      {snapshot.continuation?.phase === 'loading' ? <Text accessibilityLiveRegion="polite" style={[browseType(theme, tv ? 22 : 14, 400), {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text> : null}
      {snapshot.continuation && snapshot.continuation.phase !== 'loading' ? <AudiobookErrorState refresh={snapshot.continuation.phase === 'refresh-required'} error={snapshot.continuation.error} actionId={`${regionId}.continuation`} retry={() => {void controller.recover();}}/> : null}
      {rows.length ? <View style={{marginTop: tv ? 0 : 18}}>
        <View style={{flexDirection: 'row', alignItems: 'baseline', gap: tv ? 16 : 12, marginBottom: tv ? 12 : 0}}>
          <Text style={[browseType(theme, tv ? 32 : 20, 700), {color: theme.colors.text}]}>{chapters.length ? t('next.browse.chapters') : serverTextLabel(defaultI18n, rowsSection?.heading)}</Text>
          <Text style={[browseType(theme, tv ? 20 : 14, 600), {color: theme.colors.textTertiary}]}>{rowsSection?.totalCount ?? rows.length}</Text>
        </View>
        <ChapterRows controller={controller} entries={rows} currentId={currentId} fraction={placeFraction} runtime={runtime} tv={tv} focusPrefix={chapterFocus} onTail={tail}/>
      </View> : null}
    </View>
  </>;
  return <View testID={tv ? 'native-tv-book' : 'native-phone-book'} style={{flex: 1}}><StandardBackdrop/>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, overflow: 'hidden'}}>
      {runtime.ambient(entity.posterUrl) ? <AmbientArt source={runtime.ambient(entity.posterUrl)} treatment="title" style={{position: 'absolute', inset: 0}}/> : null}
      <View style={{position: 'absolute', inset: 0, backgroundColor: alpha(theme.colors.ink, .45)}}/>
    </View>
    {!tv && onBack ? <Button variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={onBack}
      style={{position: 'absolute', top: insets.top + 6, left: 10, zIndex: 10, height: 44, width: 44}}/> : null}
    {!tv && <Button variant="icon" icon="i-more" iconOnly accessibilityLabel={t('card.more', {title: entity.title})} onPress={() => actions.menu(entity, undefined, undefined, [{id: 'book-start-over', icon: 'i-restart', label: t('action.startOver'), onSelect: () => {void controller.startOver().catch(error => runtime.reportError(error));}}])}
      style={{position: 'absolute', top: insets.top + 6, right: 10, zIndex: 10, height: 44, width: 44}}/>}
    <MusicScrollView onNearEnd={tail} contentContainerStyle={tv ? {paddingBottom: 64} : {paddingTop: Math.max(104, insets.top + 56), paddingBottom: 48}} showsVerticalScrollIndicator={false}>
      {tv ? <FocusRegion id={regionId} entry={resumeFocusId} orientation="free" style={{width: 1920, height: canvasHeight}}>{body}</FocusRegion>
        : <View style={{alignItems: 'center', paddingHorizontal: 20}}>{body}</View>}
    </MusicScrollView>
  </View>;
}
