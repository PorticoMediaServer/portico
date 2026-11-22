import React, {useRef} from 'react';
import {Modal, ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {bytesText} from '../../../../../packages/client-core/src/presentation/kit-model/technical';
import type {NativeShellRuntime} from '../../shell/runtime';
import {shellLayout} from '../../shell/model';
import {Button, TextField} from '../controls';
import {Artwork} from '../artwork';
import {StateView} from '../states';
import {BottomSheet, Dialog, Menu} from '../overlays';
import {TouchMenuHeader} from '../overlays/Menu';
import {FocusBoundary, PanelBackground} from '../overlays/surface';
import {TVPanelSlide, TVPanelScrim, useTVPanelInteractive, useTVPanelModal} from '../overlays/TVPanelMotion';
import {useOverlayLifecycle} from '../overlays/OverlayHost';
import {textLines, useTheme} from '../theme';
import {shellType} from '../shell/visual';
import {NativeFileInfoContent} from '../title/FileInfoContent';
import {overlayPaintKey, overlayPaintPermit, RetainedOverlay, type OverlayPaintPermit} from '../overlays/SurfacePaintHost';
import type {NativeWatchOverlay, NativeWatchPresenters} from '../../screens/watch/presenters';
import type {useWatchManage} from '../../pages/shared/player/useWatchOverlays';
import type {watchTitleMenu} from '../../pages/shared/player/watch-title-menu';

const paintTypes = [React.Fragment, View, Text, ScrollView, Button, TextField, Artwork, StateView, BottomSheet, Dialog, Menu, TouchMenuHeader, NativeFileInfoContent];
/** An overlay paints only while its presenter still holds this capture (and, with a runtime, while the shell and route do). */
export const watchPaintPermit = (value: NativeWatchOverlay, presenters: NativeWatchPresenters, runtime?: NativeShellRuntime): OverlayPaintPermit =>
  overlayPaintPermit(() => presenters.current(value.capture), [listener => presenters.subscribe(listener),
    ...(runtime ? [(listener: () => void) => runtime.subscribe(listener), ...(runtime.navigation ? [(listener: () => void) => runtime.navigation!.subscribe(listener)] : [])] : [])]);
const named = (entry: NativeWatchOverlay['capture']['entry']) => entry.displayTitle ?? entry.title;

/** The authored File info container; the native video stays in its persistent
 * host below this overlay. TV chrome alone uses the1920 canvas once. */
export function WatchFileInfoSurface({value, presenters}: {value: NativeWatchOverlay; presenters: NativeWatchPresenters}) {
  const theme = useTheme(), window = useWindowDimensions(), done = useRef<View>(null), t = defaultI18n.t;
  const interactive = useTVPanelInteractive(), modal = useTVPanelModal();
  React.useEffect(() => {if (theme.isTV && interactive) done.current?.requestTVFocus?.();}, [theme.isTV, interactive]);
  useOverlayLifecycle(theme.isTV, true, presenters.close, undefined, value.anchorRef, 0, 'watch-file-info');
  const action = <Button ref={done} testID="watch-file-info-done" variant={theme.isTV ? 'primary' : 'glass'} size={theme.isTV ? 'hero' : 'small'} preferredFocus={interactive} onPress={presenters.close}>{t('next.overlays.done')}</Button>;
  if (theme.isTV) {
    const canvas = shellLayout(window.width, window.height, true);
    return <Modal key={modal.key} transparent visible={modal.visible} onDismiss={modal.onDismiss} onShow={modal.onShow} animationType="none" onRequestClose={presenters.close} statusBarTranslucent navigationBarTranslucent>
      <View style={StyleSheet.absoluteFill} accessibilityViewIsModal><TVPanelScrim color="#04090d9e"/>
        <View pointerEvents="box-none" style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height, transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
          <TVPanelSlide width={900} style={StyleSheet.absoluteFill}><FocusBoundary modal testID="watch-file-info-tv" style={{position: 'absolute', right: 0, top: 0, bottom: 0, width: 900, paddingTop: 64, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}>
            <PanelBackground/>
            <Text style={[shellType(22, 700, 33, 0, theme.textScale), {color: theme.colors.textSecondary}]}>{t('next.overlays.fileInfo')}</Text>
            <Text style={[shellType(46, 800, 48, -.035, theme.textScale), {color: theme.colors.text, marginTop: 18}]}>{named(value.capture.entry)}</Text>
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
      <Text style={[shellType(21, 800, 'normal', -.02, theme.textScale), {color: theme.colors.text}]}>{t('next.overlays.fileInfo')}</Text>
      <Text {...textLines(1)} style={[theme.type.caption, shellType(14, 400, 'normal', 0, 1), {color: theme.colors.textTertiary, flex: 1}]}>{named(value.capture.entry)}</Text>{action}
    </View>} contentStyle={{paddingHorizontal: 16, paddingTop: 8, flex: 1}}>{value.content}</BottomSheet></RetainedOverlay>;
}

/** The delete, split and merge dialog over the player, drawn from `useWatchManage`. */
export function WatchManageDialog({value, presenters, entry, name, words, setWords, file, heading, failure, deletion, splitCandidates, mergeExplanation}: ReturnType<typeof useWatchManage>) {
  const theme = useTheme(), t = defaultI18n.t;
  const body = <View style={{gap: 16}}>
    {failure && <StateView variant="inline" {...failure} body={value.phase === 'delete' && value.refusal || failure.body}/>}
    {value.phase === 'delete' ? <>
      {!value.preview && !value.error && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('deleteMedia.checking')}</Text>}
      {!!value.preview?.deleteRefusal && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{value.preview.deleteRefusal}</Text>}
      {value.preview && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}><Text style={{color: theme.colors.text, fontFamily: theme.type.bodyStrong.fontFamily}}>{t('web.deleteVersion.files', {count: value.preview.files, size: bytesText(value.preview.bytes, t) ?? ''})}</Text>{value.preview.deleteRefusal ? '' : ` ${t('web.deleteVersion.disk')}`}</Text>}
      {file?.path && <Text selectable style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{file.path}</Text>}
      {/* What the delete would take (web #3407, rule 65), only when it can happen: under a refusal nothing goes. */}
      {value.preview && !value.preview.deleteRefusal && <>
        {deletion.length
          ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('web.deleteVersion.titlesGo', {count: deletion.length})} {deletion.join(', ')}</Text>
          : <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('web.deleteVersion.titleStays', {title: value.preview.title})}</Text>}
        {value.preview.listEntries > 0 && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('web.deleteVersion.listEntries', {count: value.preview.listEntries})}</Text>}
        {value.preview.otherPeople > 0 && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('web.deleteVersion.otherPeople', {count: value.preview.otherPeople})}</Text>}
      </>}
      {value.preview && !value.preview.deleteRefusal && <TextField testID="watch-delete-version-confirmation" label={t('deleteMedia.typeTitle')}
        description={value.preview.title} value={value.confirmation} onChangeText={presenters.confirmation} editable={!value.busy} autoCapitalize="none" autoCorrect={false}/>}
    </> : value.phase === 'split-pick' ? splitCandidates.map(candidate => <Button key={candidate.occurrence} focusId={`watch-split-file-${candidate.id}`} testID={`watch-split-file-${candidate.id}`} variant="glass" onPress={() => presenters.chooseSplit(candidate.occurrence)}>{candidate.file?.path ?? t('next.kit.version', {number: candidate.ordinal})}</Button>)
      : value.phase === 'split' ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{name}{'\n'}{file?.path}{'\n\n'}{t('title.split.body')}</Text>
        : !value.other ? <><TextField testID="watch-merge-search" label={t('title.merge.find')} value={words} onChangeText={setWords} maxLength={200} autoCorrect={false}/>
          {value.candidates.map(other => <Button key={other.id} focusId={`watch-merge-candidate-${other.id}`} testID={`watch-merge-candidate-${other.id}`} variant="glass" onPress={() => presenters.chooseMerge(other)}>{named(other)}</Button>)}
          {value.searchReady && !value.candidates.length && !value.error && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('title.merge.none')}</Text>}</>
          : <><Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('web.merge.keep')}</Text>{[entry, value.other].map(other => <Button key={other.id} focusId={`watch-merge-keep-${other.id}`} testID={`watch-merge-keep-${other.id}`} variant={value.keep === other.id ? 'primary' : 'glass'} accessibilityState={{selected: value.keep === other.id}} onPress={() => presenters.keep(other.id)} disabled={value.busy}>{named(other)}</Button>)}
            <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{mergeExplanation}</Text></>}
  </View>;
  const footer = <View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 10}}>
    <Button focusId="watch-manage-cancel" testID="watch-manage-cancel" variant="glass" disabled={value.busy} onPress={presenters.backToInfo}>{t('action.cancel')}</Button>
    {value.phase === 'delete' ? !value.preview?.deleteRefusal && <Button focusId="watch-delete-version" testID="watch-delete-version" variant="danger" disabled={value.busy || !value.preview || value.confirmation.trim() !== value.preview.title} onPress={() => {void presenters.deleteVersion();}}>{t('action.delete')}</Button>
      : value.phase === 'split' ? <Button focusId="watch-split-confirm" testID="watch-split-confirm" disabled={value.busy} onPress={() => {void presenters.split();}}>{t('title.split.confirm')}</Button>
        : value.phase === 'merge' && value.other ? <Button focusId="watch-merge-confirm" testID="watch-merge-confirm" disabled={value.busy} onPress={() => {void presenters.merge();}}>{t('web.selection.merge')}</Button> : null}
  </View>;
  return <RetainedOverlay key={overlayPaintKey(value.capture)} permit={watchPaintPermit(value, presenters)} allowedTypes={paintTypes}><Dialog open title={heading} description={name} onOpenChange={open => {if (!open) presenters.close();}} footer={footer} dismissOnBack={!value.busy} dismissOnOutsidePress={!value.busy} showClose={false} testID="watch-file-manage">
    <ScrollView contentContainerStyle={{paddingVertical: 16}}>{body}</ScrollView>
  </Dialog></RetainedOverlay>;
}

/** The title menu over the player, drawn from `watchTitleMenu`: the TV side sheet's context line and runtime, the phone's
 * header with the poster, and the grouped or listed rows. */
export function WatchTitleMenu({menu, presenters, runtime}: {menu: ReturnType<typeof watchTitleMenu>; presenters: NativeWatchPresenters; runtime: NativeShellRuntime}) {
  const theme = useTheme();
  const {value, subject, art, kind, year, items, page, tv, series, code, movie, description, close} = menu;
  const contextStyle = (weight: 600 | 700, color: string) => [shellType(22, weight, 'normal', 0, theme.textScale), {color}];
  const context = series || code
    ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 16}}>
      {series ? <Text style={contextStyle(700, theme.colors.textSecondary)}>{series}</Text> : null}
      {code ? <Text style={contextStyle(600, theme.colors.textTertiary)}>{code}</Text> : null}
    </View>
    : movie
      ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 16}}>
        <Text style={contextStyle(700, theme.colors.textSecondary)}>{movie.kind}</Text>
        {movie.year === undefined ? null : <Text style={contextStyle(600, theme.colors.textTertiary)}>{movie.year}</Text>}
      </View>
      : undefined;
  return <RetainedOverlay key={overlayPaintKey(value.capture)} permit={watchPaintPermit(value, presenters, runtime)} allowedTypes={paintTypes}><Menu open testID="watch-title-menu" title={subject} items={items} touchContext={!tv} anchorRef={value.anchorRef} returnFocusRef={value.anchorRef} context={context}
    description={description}
    descriptionTextStyle={tv ? shellType(22, 400, 31.9, 0, 1) : undefined}
    form={page ? 'groups' : 'list'} secondaryTitle={page ? year : undefined}
    touchHeader={<TouchMenuHeader title={subject} artwork={art ? <Artwork source={art} width={40} height={60}/> : undefined} kind={kind} year={year}/>}
    onOpenChange={open => {if (!open) close();}}/></RetainedOverlay>;
}
