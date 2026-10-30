import React, {useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n, formatMessage} from '../../../../../../packages/i18n/src/index';
import {fileInfoModel, type FileFacts} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {overlaysNativeFixtures} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/overlays-native-fixtures';
import type {Gallery} from '../../gallery-registry';
import {Button} from '../../../kit/controls';
import {Banner, BottomSheet, Dialog, Menu, MenuRow, OverlaysProvider, Popover, SidePanel, ToastHost, type MenuEntry, type ToastMessage} from '../../../kit/overlays';
import {ThemeProvider, useTheme} from '../../../kit/theme';
import {Artwork} from '../../../kit/artwork/Artwork';

const t = defaultI18n.t;
// Reference data belongs only to this development module, never to a shipped surface.
const fixture = (key: keyof typeof overlaysNativeFixtures) => formatMessage(overlaysNativeFixtures[key]);
const film = {name: fixture('next.overlays.native.fixture.film'), year: 2024};
const episode = {show: fixture('next.overlays.native.fixture.show'), name: fixture('next.overlays.native.fixture.episode'), season: 2, number: 3};
const memberEpisode = {show: fixture('next.overlays.native.fixture.memberShow'), name: fixture('next.overlays.native.fixture.memberEpisode'), season: 1, number: 3};
const files: readonly FileFacts[] = [
  {id: 'v1', available: true, height: 1080, width: 1920, videoCodec: 'h264', container: 'mkv', sizeBytes: 1.1 * 1024 ** 3, durationSeconds: 2820, audio: [{id: 'a1', language: 'en', channels: 6, codec: 'ac3'}], subtitles: [{id: 's1', language: 'en', format: 'srt'}]},
  {id: 'v2', available: true, height: 720, width: 1280, videoCodec: 'h264', container: 'mp4', sizeBytes: 700 * 1024 ** 2, durationSeconds: 2820, audio: [{id: 'a2', language: 'en', channels: 2, codec: 'aac'}], subtitles: []},
];

function Scene({children}: {children: React.ReactNode}) {
  const theme = useTheme();
  // The surface owns its native window; a second Modal here obscures an initially open surface on Android.
  return <View style={{flex: 1, backgroundColor: theme.colors.ink}}><OverlaysProvider>{children}</OverlaysProvider></View>;
}
function FileContent() {
  const theme = useTheme();
  const model = fileInfoModel(files, {audience: 'viewer'}, {t});
  return <View style={{paddingHorizontal: theme.isTV ? 0 : 16, gap: 12}}>{model.files.map(file => <View key={file.id} style={{padding: 14, borderRadius: 14, backgroundColor: theme.colors.raisedHigh}}>
    <View style={{flexDirection: 'row', gap: 8, flexWrap: 'wrap'}}>{file.version.name.map(part => <Text key={part.id} style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{part.text}</Text>)}</View>
    {file.rows.map(row => <View key={row.id} style={{flexDirection: 'row', gap: 10, marginTop: 6}}><Text style={[theme.type.caption, {color: theme.colors.textTertiary, width: theme.isTV ? 160 : 76}]}>{row.label}</Text><View style={{flex: 1, flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{row.parts.map(part => <Text key={part.id} style={[theme.type.captionStrong, {color: theme.colors.text}]}>{part.text}</Text>)}</View></View>)}
    {([{label: t('next.kit.audio'), tracks: file.audio}, {label: t('next.kit.subtitles'), tracks: file.subtitles}]).map(group => group.tracks.length > 0 && <View key={group.label} style={{flexDirection: 'row', gap: 10, marginTop: 6}}><Text style={[theme.type.caption, {color: theme.colors.textTertiary, width: theme.isTV ? 160 : 76}]}>{group.label}</Text><View style={{flex: 1, gap: 4}}>{group.tracks.map(track => <View key={track.id} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{[track.name, ...track.detail, ...track.technical].map(part => <Text key={part.id} style={[theme.type.captionStrong, {color: theme.colors.text}]}>{part.text}</Text>)}</View>)}</View></View>)}
  </View>)}<Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.overlays.native.fileInfoNote')}</Text></View>;
}
function MenuCase({owner = false}: {owner?: boolean}) {
  const theme = useTheme();
  const opener = useRef<View>(null);
  const [open, setOpen] = useState(true);
  const [watched, setWatched] = useState(false);
  const [favorite, setFavorite] = useState(false);
  const [info, setInfo] = useState(false);
  const [toast, setToast] = useState<ToastMessage | null>(null);
  const acknowledge = (id: string, label: string) => setToast({id, message: label});
  const phoneItems: MenuEntry[] = [
    ...(theme.isTV ? [{id: 'start', label: t('action.startOver'), icon: 'i-restart' as const, onSelect: () => acknowledge('start', t('action.startOver'))}] : []),
    {id: 'watched', label: t(watched ? 'entry.markUnwatched' : 'entry.markWatched'), icon: 'i-check', onSelect: () => {setWatched(value => !value); acknowledge('watched', t(watched ? 'entry.markUnwatched' : 'entry.markWatched'));}},
    {id: 'favorite', label: t(favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), icon: 'i-heart', onSelect: () => {setFavorite(value => !value); acknowledge('favorite', t(favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites'));}},
    ...(owner ? [{id: 'rate', label: t('next.overlays.native.rate'), icon: 'i-star' as const, onSelect: () => acknowledge('rate', t('next.overlays.native.rate'))}] : []),
    {id: 'playlist', label: t('entry.addToPlaylist'), icon: 'i-list', onSelect: () => acknowledge('playlist', t('entry.addToPlaylist'))},
    {id: 'separator', separator: true},
    ...(owner ? [{id: 'version', label: t('next.overlays.native.playDifferentVersion'), icon: 'i-film' as const, onSelect: () => setInfo(true)}] : []),
    {id: 'info', label: t('next.kit.fileInfo'), icon: 'i-info', onSelect: () => setInfo(true)},
    ...(owner && !theme.isTV ? [{id: 'owner', section: true as const, label: t('next.overlays.native.ownerOnly'), tone: 'owner' as const}, {id: 'edit', label: t('next.overlays.native.editMetadata'), icon: 'i-edit' as const, tone: 'owner' as const, onSelect: () => acknowledge('edit', t('next.overlays.native.editMetadata'))}, {id: 'refresh', label: t('entry.refreshMetadata'), icon: 'i-reload' as const, tone: 'owner' as const, onSelect: () => acknowledge('refresh', t('entry.refreshMetadata'))}] : []),
  ];
  const items: MenuEntry[] = theme.isTV ? [
    {id: 'start', label: t('next.overlays.native.playFromBeginning'), icon: 'i-restart', onSelect: () => acknowledge('start', t('next.overlays.native.playFromBeginning'))},
    {id: 'watched', label: t(watched ? 'entry.markUnwatched' : 'entry.markWatched'), icon: 'i-check', onSelect: () => setWatched(value => !value)},
    {id: 'my-list', label: t('entry.addToWatchlist'), icon: 'i-plus', onSelect: () => acknowledge('my-list', t('next.overlays.native.addedToMyList'))},
    {id: 'season', label: t('next.overlays.native.goToSeason'), icon: 'i-list', onSelect: () => acknowledge('season', t('next.kit.season', {number: episode.season}))},
    {id: 'separator', separator: true},
    {id: 'version', label: t('next.overlays.native.playDifferentVersion'), icon: 'i-stack', onSelect: () => setInfo(true)},
    {id: 'info', label: t('next.kit.fileInfo'), icon: 'i-info', onSelect: () => setInfo(true)},
  ] : phoneItems;
  return <Scene><Button ref={opener} variant="glass" onPress={() => setOpen(true)}>{t('next.overlays.native.menu')}</Button>
    <Menu open={open} onOpenChange={setOpen} title={theme.isTV ? episode.name : owner ? film.name : memberEpisode.name} description={theme.isTV ? t('next.kit.remaining', {time: t('next.kit.duration.minutes', {minutes: 12})}) : undefined} contentStyle={theme.isTV ? {paddingTop: 24} : undefined} secondaryTitle={theme.isTV ? undefined : owner ? film.year : t('next.kit.episodeCode', {season: memberEpisode.season, episode: memberEpisode.number})} context={theme.isTV ? <Text style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{episode.show}{'  '}{t('next.kit.episodeCode', {season: episode.season, episode: episode.number})}</Text> : undefined} items={items} returnFocusRef={opener} initialFocusId={theme.isTV ? 'info' : undefined} testID="overlays-menu"/>
    <BottomSheet open={info} onOpenChange={setInfo} title={t('next.kit.fileInfo')} returnFocusRef={opener} detents={[554, 740]} testID="overlays-file-sheet"><FileContent/></BottomSheet>
    <ToastHost toast={toast} onDismiss={() => setToast(null)}/>
  </Scene>;
}
function OwnerMenu() {return <MenuCase owner/>;}
function MemberMenu() {return <MenuCase/>;}
function PopoverCase({modal = false}: {modal?: boolean}) {
  const anchor = useRef<View>(null);
  const selectedRow = useRef<View>(null);
  const [open, setOpen] = useState(true);
  const [selected, setSelected] = useState(0);
  const libraries = [{kind: 'movie', pivot: 'movies', count: 80}, {kind: 'tv', pivot: 'shows', count: 24}, {kind: 'anime', pivot: 'shows', count: 8}, {kind: 'music', pivot: 'albums', count: 10}, {kind: 'audiobook', pivot: 'books', count: 8}];
  const names = libraries.map(library => t('web.libraryKind.plural', {kind: library.kind}));
  return <Scene><View style={{paddingTop: 54, alignItems: 'center'}}><Button ref={anchor} variant="text" size="compact" style={{height: 36, paddingHorizontal: 6}} textStyle={{fontSize: 14.5}} onPress={() => setOpen(value => !value)}>{names[selected]}</Button></View>
    <Popover open={open} onOpenChange={setOpen} anchorRef={anchor} title={null} accessibilityLabel={t('next.overlays.native.popover')} initialFocusRef={selectedRow} placement="bottom-center" width={252} modal={modal} backdropOpacity={0.62} testID="overlays-popover">{libraries.map((library, index) => <MenuRow ref={selected === index ? selectedRow : undefined} key={library.kind} label={names[index]} selected={selected === index} trailing={t('lib.count', {count: library.count, pivot: library.pivot})} onPress={() => {setSelected(index); setOpen(false);}}/>)}</Popover>
  </Scene>;
}
function PanelCase() {
  const theme = useTheme();
  const opener = useRef<View>(null);
  const [open, setOpen] = useState(true);
  const [background, setBackground] = useState(false);
  return <Scene><View style={{height: 76, flexDirection: 'row', gap: 20, padding: 16}}><Button ref={opener} variant="text" onPress={() => setOpen(value => !value)}>{t('next.kit.fileInfo')}</Button><Button variant="glass" onPress={() => setBackground(value => !value)}>{t(background ? 'entry.removeFromFavorites' : 'entry.addToFavorites')}</Button></View>
    <SidePanel open={open} onOpenChange={setOpen} title={t('next.kit.fileInfo')} returnFocusRef={opener} topInset={76} width={theme.isTV ? 900 : 440} testID="overlays-panel"><ScrollView><FileContent/></ScrollView></SidePanel>
  </Scene>;
}
function SheetCase({full = false}: {full?: boolean}) {
  const opener = useRef<View>(null);
  const [open, setOpen] = useState(true);
  const [detent, setDetent] = useState(full ? 1 : 0);
  return <Scene><Button ref={opener} variant="glass" onPress={() => setOpen(true)}>{t('next.overlays.native.bottomSheet')}</Button>
    <BottomSheet open={open} onOpenChange={setOpen} title={t('next.kit.fileInfo')} detents={[422, 740]} detent={detent} onDetentChange={setDetent} returnFocusRef={opener} footer={<Button variant="primary" onPress={() => setOpen(false)}>{t('action.done')}</Button>} testID="overlays-detents"><FileContent/></BottomSheet>
  </Scene>;
}
function DialogCase() {
  const theme = useTheme();
  const opener = useRef<View>(null);
  const cancel = useRef<View>(null);
  const [open, setOpen] = useState(true);
  const [removed, setRemoved] = useState(false);
  return <Scene><Button ref={opener} variant="glass" onPress={() => {setRemoved(false); setOpen(true);}}>{t('next.overlays.native.dialog')}</Button>
    <Dialog open={open} onOpenChange={setOpen} title={t('confirm.removeDownload.title')} description={t('confirm.removeDownload.body', {title: film.name})} initialFocusRef={cancel} returnFocusRef={opener} footer={<View style={{flexDirection: 'row', gap: 10}}><Button ref={cancel} variant="glass" hasTVPreferredFocus onPress={() => setOpen(false)}>{t('action.cancel')}</Button><Button variant="text" style={{backgroundColor: `${theme.colors.record}24`}} textStyle={{color: theme.colors.record}} onPress={() => {setRemoved(true); setOpen(false);}}>{t('downloads.removeFromDevice')}</Button></View>} testID="overlays-dialog"><View/></Dialog>
    <ToastHost toast={removed ? {id: 'removed', message: t('downloads.removeFromDevice'), action: {label: t('action.undo'), onPress: () => setRemoved(false)}} : null} onDismiss={() => setRemoved(false)}/>
  </Scene>;
}
function ToastCase({artwork = false, withAction = false}: {artwork?: boolean; withAction?: boolean}) {
  const [artStatus, setArtStatus] = useState<'loading' | 'ready' | 'error'>('loading');
  const [toast, setToast] = useState<ToastMessage | null>({id: 'added', message: t('next.overlays.native.addedToMyList'), ...(withAction ? {action: {label: t('action.undo'), onPress: () => setToast(null)}} : {})});
  const [saved, setSaved] = useState(true);
  const update = () => {setSaved(value => !value); setToast({id: `saved-${Date.now()}`, message: t(saved ? 'entry.removeFromWatchlist' : 'next.overlays.native.addedToMyList'), action: {label: t('action.undo'), onPress: () => setSaved(saved)}});};
  return <Scene><View testID={`overlays-toast-art-status-${artStatus}`}/><Button variant="glass" onPress={update}>{t(saved ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist')}</Button><ToastHost toast={toast ? {...toast, leading: artwork ? <Artwork source={{uri: 'http://100.120.50.24:8787/assets/pool/movies/dune-part-two.jpg', cacheKey: 'reference:dune-part-two:sharp'}} sized width={28} height={42} style={{borderRadius: 5}} onLoad={() => setArtStatus('ready')} onError={() => setArtStatus('error')} testID="overlays-toast-artwork"/> : undefined} : null} onDismiss={() => setToast(null)} testID="overlays-toast"/></Scene>;
}
function BannerCase() {
  const [visible, setVisible] = useState(true);
  return <Scene><Button variant="glass" onPress={() => setVisible(value => !value)}>{t('next.overlays.native.banner')}</Button><Banner visible={visible} message={t('next.overlays.native.stillLoading', {server: fixture('next.overlays.native.fixture.server')})} topInset={96} testID="overlays-banner-quiet"/><Banner visible={visible} variant="notice" tone="info" message={t('next.overlays.native.stillLoading', {server: fixture('next.overlays.native.fixture.server')})} action={{label: t('action.tryAgain'), onPress: () => setVisible(false)}} topInset={160} testID="overlays-banner-notice"/></Scene>;
}
function ModalPopoverCase() {return <PopoverCase modal/>;}
function FullSheetCase() {return <SheetCase full/>;}
function ActionToastCase() {return <ToastCase withAction/>;}
function ArtToastCase() {return <ToastCase artwork withAction/>;}
function ReducedMenuCase() {return <ThemeProvider reduceMotion><MemberMenu/></ThemeProvider>;}
function ReducedSheetCase() {return <ThemeProvider reduceMotion><SheetCase/></ThemeProvider>;}
const galleries: readonly Gallery[] = [
  {id: 'overlays-menu-owner', title: 'next.overlays.native.menu', render: OwnerMenu, platforms: ['phone', 'tablet']},
  {id: 'overlays-menu-member', title: 'next.overlays.native.menu', render: MemberMenu, platforms: ['phone', 'tablet', 'tv']},
  {id: 'overlays-popover', title: 'next.overlays.native.popover', render: PopoverCase, platforms: ['phone', 'tablet']},
  {id: 'overlays-popover-modal', title: 'next.overlays.native.popover', render: ModalPopoverCase, platforms: ['phone', 'tablet']},
  {id: 'overlays-menu-reduced', title: 'next.overlays.native.menu', render: ReducedMenuCase, platforms: ['phone', 'tablet', 'tv']},
  {id: 'overlays-dialog', title: 'next.overlays.native.dialog', render: DialogCase, platforms: ['phone', 'tablet', 'tv']},
  {id: 'overlays-panel', title: 'next.overlays.native.sidePanel', render: PanelCase, platforms: ['tablet', 'tv']},
  {id: 'overlays-sheet', title: 'next.overlays.native.bottomSheet', render: SheetCase, platforms: ['phone']},
  {id: 'overlays-sheet-full', title: 'next.overlays.native.bottomSheet', render: FullSheetCase, platforms: ['phone']},
  {id: 'overlays-sheet-reduced', title: 'next.overlays.native.bottomSheet', render: ReducedSheetCase, platforms: ['phone']},
  {id: 'overlays-toast', title: 'next.overlays.native.toast', render: ToastCase, platforms: ['phone', 'tablet', 'tv']},
  {id: 'overlays-toast-action', title: 'next.overlays.native.toast', render: ActionToastCase, platforms: ['phone', 'tablet', 'tv']},
  {id: 'overlays-toast-artwork', title: 'next.overlays.native.toast', render: ArtToastCase, platforms: ['phone', 'tablet', 'tv']},
  {id: 'overlays-banner', title: 'next.overlays.native.banner', render: BannerCase, platforms: ['phone', 'tablet', 'tv']},
];
export default galleries;
