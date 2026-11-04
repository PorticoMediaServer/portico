import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Modal, Platform, ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {deletionAllowed} from '../../../../../packages/client-core/src/administration';
import {mergeExplanation} from '../../../../../packages/client-core/src/title-identity';
import {nativeFileInfoFile, nativeSplitCandidates} from './file-info';
import {durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import type {NativeShellRuntime} from '../../shell/runtime';
import {shellLayout} from '../../shell/model';
import {Button, TextField} from '../../kit/controls';
import {Artwork} from '../../kit/artwork';
import {StateView} from '../../kit/states';
import {BottomSheet, Dialog, Menu, type MenuAction} from '../../kit/overlays';
import {titleMenuItems} from '../../kit/overlays/title-menu';
import type {IconName} from '../../kit/Icon';
import type {EntryActionId} from '../../../../../packages/client-core/src/presentation/entry-actions';
import {nativeWatchMenuGroups} from './menu-model';
import {TouchMenuHeader} from '../../kit/overlays/Menu';
import {FocusBoundary, PanelBackground} from '../../kit/overlays/surface';
import {TVPanelTransition, TVPanelSlide, TVPanelScrim, useTVPanelPresence, useTVPanelInteractive, useTVPanelModal} from '../../kit/overlays/TVPanelMotion';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {nativeType, useTheme} from '../../kit/theme';
import type {NativeScreenServices} from '../screen-services';
import type {NativeWatchOverlay, NativeWatchPresenters} from './presenters';
import {NativeVersionChooser} from './VersionChooser';
import {NativeDiscTitlesSheet} from './DiscTitlesSheet';
import {NativeFileInfoContent} from './FileInfoContent';
import {overlayPaintKey, overlayPaintPermit, RetainedOverlay, type OverlayPaintPermit} from '../../kit/overlays/SurfacePaintHost';

const named = (entry: NativeWatchOverlay['capture']['entry']) => entry.displayTitle ?? entry.title;
const paintTypes = [React.Fragment, View, Text, ScrollView, Button, TextField, Artwork, StateView, BottomSheet, Dialog, Menu, TouchMenuHeader, NativeFileInfoContent];
const watchPaintPermit = (value: NativeWatchOverlay, presenters: NativeWatchPresenters, runtime?: NativeShellRuntime): OverlayPaintPermit =>
  overlayPaintPermit(() => presenters.current(value.capture), [listener => presenters.subscribe(listener),
    ...(runtime ? [(listener: () => void) => runtime.subscribe(listener), ...(runtime.navigation ? [(listener: () => void) => runtime.navigation!.subscribe(listener)] : [])] : [])]);


/** The authored File info container; the native video stays in its persistent
 * host below this overlay. TV chrome alone uses the1920 canvas once. */
function FileInfoSurface({value, presenters}: {value: NativeWatchOverlay; presenters: NativeWatchPresenters}) {
  const theme = useTheme(), window = useWindowDimensions(), done = useRef<View>(null), t = defaultI18n.t;
  const interactive = useTVPanelInteractive(), modal = useTVPanelModal();
  React.useEffect(() => {if (theme.isTV && interactive) done.current?.requestTVFocus?.();}, [theme.isTV, interactive]);
  useOverlayLifecycle(theme.isTV, true, presenters.close, undefined, value.anchorRef, 0, 'watch-file-info');
  const action = <Button ref={done} testID="watch-file-info-done" variant={theme.isTV ? 'primary' : 'glass'} size={theme.isTV ? 'hero' : 'small'} preferredFocus={interactive} onPress={presenters.close}>{t('next.overlays.done')}</Button>;
  if (theme.isTV) {
    const canvas = shellLayout(window.width, window.height, true);
    return <Modal transparent visible={modal.visible} onDismiss={modal.onDismiss} animationType="none" onRequestClose={presenters.close} statusBarTranslucent navigationBarTranslucent>
      <View style={StyleSheet.absoluteFill} accessibilityViewIsModal><TVPanelScrim color="#04090d9e"/>
        <View pointerEvents="box-none" style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height, transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
          <TVPanelSlide width={900} style={StyleSheet.absoluteFill}><FocusBoundary modal testID="watch-file-info-tv" style={{position: 'absolute', right: 0, top: 0, bottom: 0, width: 900, paddingTop: 64, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}>
            <PanelBackground/>
            <Text style={[nativeType({size: 22, weight: 700, tracking: 0, lineHeight: 33}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('next.overlays.fileInfo')}</Text>
            <Text style={[nativeType({size: 46, weight: 800, tracking: -.035, lineHeight: 48}, theme.textScale), {color: theme.colors.text, marginTop: 18}]}>{named(value.capture.entry)}</Text>
            <View style={{flex: 1, marginTop: 14}}>{value.content}</View>
            <View style={{marginTop: 24, height: 72, alignItems: 'flex-start'}}>{action}</View>
          </FocusBoundary></TVPanelSlide>
        </View>
      </View>
    </Modal>;
  }
  return <RetainedOverlay key={overlayPaintKey(value.capture)} permit={watchPaintPermit(value, presenters)} allowedTypes={paintTypes}><BottomSheet testID="watch-file-info-phone" open onOpenChange={open => {if (!open) presenters.close();}} title={t('next.overlays.fileInfo')}
    showClose={false} initialFocusRef={done} returnFocusRef={value.anchorRef} scrollable={false} detents={[Math.max(1, window.height - 104)]}
    headerContent={<View style={{paddingHorizontal: 20, paddingTop: 10, paddingBottom: 14, flexDirection: 'row', alignItems: 'center', gap: 10}}>
      <Text style={[nativeType({size: 21, weight: 800, tracking: -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('next.overlays.fileInfo')}</Text>
      <Text numberOfLines={1} style={[theme.type.caption, {color: theme.colors.textTertiary, flex: 1, fontSize: 14}]}>{named(value.capture.entry)}</Text>{action}
    </View>} contentStyle={{paddingHorizontal: 16, paddingTop: 8, flex: 1}}>{value.content}</BottomSheet></RetainedOverlay>;
}

function ManageSurface({value, presenters, common}: {value: NativeWatchOverlay; presenters: NativeWatchPresenters; common: NativeScreenServices}) {
  const theme = useTheme(), t = defaultI18n.t, [words, setWords] = useState('');
  useEffect(() => {if (value.phase !== 'merge' || value.other) return; const timer = setTimeout(() => {void presenters.search(words);}, 300); return () => clearTimeout(timer);}, [presenters, value.phase, value.other, words]);
  const snapshot = value.owner?.session.getSnapshot(), entry = value.capture.entry;
  const file = snapshot && value.fileId ? nativeFileInfoFile(snapshot, value.fileId)?.file : undefined;
  const heading = value.phase === 'delete' ? t('title.deleteVersion.title') : value.phase === 'split' || value.phase === 'split-pick' ? t('title.split.title') : t(value.other ? 'web.merge.title' : 'title.merge.action');
  const from = value.other && (value.keep === value.other.id ? entry : value.other);
  const retry = () => {if (value.phase === 'delete') void presenters.previewDelete(); else if (value.phase === 'merge') void presenters.search(words); else void presenters.split();};
  const body = <View style={{gap: 16}}>
    {!!value.error && <StateView variant="inline" {...common.error(value.error, retry, 'detail')}/>}
    {value.phase === 'delete' ? <>
      {!value.preview && !value.error && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('deleteMedia.checking')}</Text>}
      {value.preview && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('title.deleteVersion.body', {size: value.preview.totalBytesText})}</Text>}
      {file?.path && <Text selectable style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{file.path}</Text>}
      {value.preview && (deletionAllowed(value.preview) ? <TextField testID="watch-delete-version-confirmation" label={t(value.preview.confirmationKind === 'count' ? 'deleteMedia.typeCount' : 'deleteMedia.typeTitle', {count: value.preview.confirmation})}
        description={value.preview.confirmationKind === 'title' ? value.preview.confirmation : undefined} value={value.confirmation} onChangeText={presenters.confirmation} editable={!value.busy} autoCapitalize="none" autoCorrect={false}/>
        : <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('deleteMedia.disabled')}</Text>)}
    </> : value.phase === 'split-pick' ? (snapshot ? nativeSplitCandidates(snapshot) : []).filter(candidate => candidate.file?.available !== false).map(candidate => <Button key={candidate.occurrence} focusId={`watch-split-file-${candidate.id}`} testID={`watch-split-file-${candidate.id}`} variant="glass" onPress={() => presenters.chooseSplit(candidate.occurrence)}>{candidate.file?.path ?? t('next.kit.version', {number: candidate.ordinal})}</Button>)
      : value.phase === 'split' ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{named(entry)}{'\n'}{file?.path}{'\n\n'}{t('title.split.body')}</Text>
        : !value.other ? <><TextField testID="watch-merge-search" label={t('title.merge.find')} value={words} onChangeText={setWords} maxLength={200} autoCorrect={false}/>
          {value.candidates.map(other => <Button key={other.id} focusId={`watch-merge-candidate-${other.id}`} testID={`watch-merge-candidate-${other.id}`} variant="glass" onPress={() => presenters.chooseMerge(other)}>{named(other)}</Button>)}
          {value.searchReady && !value.candidates.length && !value.error && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('title.merge.none')}</Text>}</>
          : <><Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('web.merge.keep')}</Text>{[entry, value.other].map(other => <Button key={other.id} focusId={`watch-merge-keep-${other.id}`} testID={`watch-merge-keep-${other.id}`} variant={value.keep === other.id ? 'primary' : 'glass'} accessibilityState={{selected: value.keep === other.id}} onPress={() => presenters.keep(other.id)} disabled={value.busy}>{named(other)}</Button>)}
            <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t(mergeExplanation(entry.kind), {title: named(from!)})}</Text></>}
  </View>;
  const footer = <View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 10}}>
    <Button focusId="watch-manage-cancel" testID="watch-manage-cancel" variant="glass" disabled={value.busy} onPress={presenters.backToInfo}>{t('action.cancel')}</Button>
    {value.phase === 'delete' ? <Button focusId="watch-delete-version" testID="watch-delete-version" variant="primary" disabled={value.busy || !value.preview || !deletionAllowed(value.preview) || value.confirmation.trim() !== value.preview.confirmation} onPress={() => {void presenters.deleteVersion();}}>{t('action.delete')}</Button>
      : value.phase === 'split' ? <Button focusId="watch-split-confirm" testID="watch-split-confirm" disabled={value.busy} onPress={() => {void presenters.split();}}>{t('title.split.confirm')}</Button>
        : value.phase === 'merge' && value.other ? <Button focusId="watch-merge-confirm" testID="watch-merge-confirm" disabled={value.busy} onPress={() => {void presenters.merge();}}>{t('web.selection.merge')}</Button> : null}
  </View>;
  return <RetainedOverlay key={overlayPaintKey(value.capture)} permit={watchPaintPermit(value, presenters)} allowedTypes={paintTypes}><Dialog open title={heading} description={named(entry)} onOpenChange={open => {if (!open) presenters.close();}} footer={footer} dismissOnBack={!value.busy} dismissOnOutsidePress={!value.busy} showClose={false} testID="watch-file-manage">
    <ScrollView contentContainerStyle={{paddingHorizontal: theme.isTV ? 0 : 20, paddingVertical: 16}}>{body}</ScrollView>
  </Dialog></RetainedOverlay>;
}

export function NativeWatchOverlays({presenters, runtime, common}: {presenters: NativeWatchPresenters; runtime: NativeShellRuntime; common: NativeScreenServices}) {
  const live = useSyncExternalStore(presenters.subscribe, presenters.getSnapshot), t = defaultI18n.t, theme = useTheme();
  const current = live && presenters.overlayCurrent(live) ? live : undefined;
  const moving = theme.isTV && current && ['versions', 'file-info', 'disc-titles'].includes(current.phase) ? current : undefined;
  const presence = useTVPanelPresence(moving, runtime);
  const value = current ?? presence.value;
  if (!value) return null;
  const transition = (node: React.ReactNode) => theme.isTV ? <TVPanelTransition key={value.phase} modal open={presence.open} onExited={presence.exited}>{node}</TVPanelTransition> : node;
  if (value.phase === 'disc-titles' && value.disc) return transition(<NativeDiscTitlesSheet controller={value.disc} paintPermit={watchPaintPermit(value, presenters, runtime)} subject={named(value.capture.entry)} close={() => presenters.closeDisc(value)}
    renderError={(error, retry) => <StateView variant="inline" {...common.error(error, retry, 'detail')}/>}/>);
  if (value.phase === 'versions') return transition(<NativeVersionChooser value={value} paintPermit={watchPaintPermit(value, presenters, runtime)} choose={id => {void presenters.chooseVersion(value, id);}} close={() => presenters.closeVersions(value)}/>);
  if (value.phase === 'file-info') return transition(<FileInfoSurface value={value} presenters={presenters}/>);
  if (value.phase !== 'menu' && value.phase !== 'file-pending' && value.phase !== 'versions-pending') return <ManageSurface value={value} presenters={presenters} common={common}/>;
  if (value.phase === 'file-pending' || value.phase === 'versions-pending') return null;
  const entry = value.capture.entry, art = runtime.artwork(entry.posterUrl), subject = named(entry);
  const tv = Platform.isTV, menu = value.actions?.entry;
  const kindKey = entry.kind === 'movie' ? 'search.kind.movie' : entry.kind === 'show' ? 'search.kind.show' : entry.kind === 'artist' ? 'search.kind.artist' : entry.kind === 'album' ? 'search.kind.album' : undefined;
  // Title rows come from the one shared model and renderer (also Home's More menu); this menu
  // adds only what belongs to a screen's own surface: Start radio, Play a different version…,
  // File info, Select, Disc titles and the caller's extras (select.html 17 and 23, tablet.html 9).
  const together = tv ? presenters.togetherActions(value) : undefined;
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
  const items = titleMenuItems(groups, {tv, run, busy: value.busy, afterPlay: radio ? [radio] : undefined, related: season ? [season] : undefined, surface, afterDetails: tv ? [version, fileInfo].filter((item): item is MenuAction => !!item) : undefined, trailing: extras});
  // tv-detail.html frame 7 and playback-info.html frame 17 draw the side sheet's
  // small context line above the title, from the entry's own facts only.
  const series = tv && entry.kind === 'episode' ? entry.show?.name : undefined;
  const code = tv && entry.kind === 'episode' && entry.seasonNumber !== undefined && entry.episodeNumber !== undefined
    ? t('next.kit.episodeCode', {season: entry.seasonNumber, episode: entry.episodeNumber}) : undefined;
  const contextStyle = (weight: 600 | 700, color: string) => [nativeType({size: 22, weight, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color}];
  const context = !tv ? undefined : series || code
    ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 16}}>
      {series ? <Text style={contextStyle(700, theme.colors.textSecondary)}>{series}</Text> : null}
      {code ? <Text style={contextStyle(600, theme.colors.textTertiary)}>{code}</Text> : null}
    </View>
    : entry.kind === 'movie'
      ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 16}}>
        <Text style={contextStyle(700, theme.colors.textSecondary)}>{t('search.kind.movie')}</Text>
        {entry.year === undefined ? null : <Text style={contextStyle(600, theme.colors.textTertiary)}>{defaultI18n.number(entry.year, {useGrouping: false})}</Text>}
      </View>
      : undefined;
  // playback-info.html frame 17 draws the film's runtime under the title at 22/1.45.
  return <RetainedOverlay key={overlayPaintKey(value.capture)} permit={watchPaintPermit(value, presenters, runtime)} allowedTypes={paintTypes}><Menu open testID="watch-title-menu" title={subject} items={items} touchContext={!Platform.isTV} anchorRef={value.anchorRef} returnFocusRef={value.anchorRef} context={context}
    description={tv && entry.kind === 'movie' && entry.duration ? durationText(entry.duration, t) : undefined}
    descriptionTextStyle={tv ? {fontSize: 22, lineHeight: 31.9} : undefined}
    touchHeader={<TouchMenuHeader title={subject} artwork={art ? <Artwork source={art} width={40} height={60}/> : undefined} kind={kindKey ? t(kindKey) : undefined} year={entry.year === undefined ? undefined : defaultI18n.number(entry.year, {useGrouping: false})}/>}
    onOpenChange={open => {if (!open) presenters.closeMenu(value);}}/></RetainedOverlay>;
}
