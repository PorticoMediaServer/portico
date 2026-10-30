import React, {forwardRef, useCallback, useEffect, useLayoutEffect, useId, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Share, Text, View, type TextInput} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {GroupEnterLifetime} from '../../../../../packages/client-core/src/group-session';
import type {MediaItem} from '../../../../../packages/client-core/src';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes';
import {groupTargetPositionUs, type GroupHostAuthority} from '../../../../../packages/client-core/src/social-playback';
import {Button, Focusable, Segmented, TextField} from '../../kit/controls';
import {FocusRegion} from '../../kit/focus';
import {Dialog, Banner, Menu} from '../../kit/overlays';
import {AmbientArt, Artwork, ProfileFace, StandardBackdrop} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {StateView} from '../../kit/states';
import {alpha, shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {tvFocusIds} from '../../shell/tv-focus';
import {NativeTVKeyboard} from '../destinations';
import {Screen, ScreenText, useScreenEntry} from './Screen';
import type {NativeSettingsLivePorts} from './ports';
import type {NativeTogetherController} from './together-controller';
import {memberNames, nativeTogetherLobbyFaces} from './together-presentation';
import {NativeTogetherTVRoom} from './TogetherTVRoom';
import type {NativeTogetherMedia} from './together-media';
import type {NativeTogetherPage} from './together-page';

export function NativeTogetherScreen({controller, media, ports, runtime, page, onBack}: {page: NativeTogetherPage; controller: NativeTogetherController; media: NativeTogetherMedia; ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime; onBack?: () => void}) {
  const theme = useTheme(), t = runtime.text.t, phone = !theme.isTV && theme.width < 600;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]);
  const region = ids.surface('together');
  const group = state.group;
  type Form = {kind: 'start' | 'join'; retired: boolean};
  const [form, setForm] = useState<Form>(), [name, setName] = useState(''), [code, setCode] = useState('');
  const [typing, setTyping] = useState<'name' | 'code' | 'who'>(), [typingRevision, setTypingRevision] = useState(0), [typingIntent, setTypingIntent] = useState<'enter' | 'restore'>('enter');
  const firstField = useRef<TextInput>(null);
  const firstTVField = useRef<View>(null);
  const profile = ports.context.account.profiles.find(value => value.id === controller.lifetime.authority.profileId);
  const faces = nativeTogetherLobbyFaces(state.directory[0], runtime.getSnapshot().profile);
  const [who, setWho] = useState(profile?.name ?? ''), [authority, setAuthority] = useState<GroupHostAuthority>('host-only');
  const startRef = useRef<View>(null), joinRef = useRef<View>(null);
  const sheet = form?.kind;
  const view = useMemo(() => ({active: true, binding: {controller, page}, form: undefined as Form | undefined, pending: undefined as {abort: AbortController; form?: Form} | undefined}), [controller, page]);
  const viewCurrent = useCallback((expected: Form | undefined) => {
    const local = () => view.active && view.form === expected && !expected?.retired && !page.signal.aborted && !controller.lifetime.signal.aborted;
    return local() && page.current() && local() && controller.lifetime.current() && local();
  }, [controller, page, view]);
  // SUP-006: only the server's wrong-code refusal for this join belongs on the field.
  const codeFailure = !theme.isTV && sheet === 'join' && state.joinFailure?.code === 'invite_not_found' && !!state.error;
  const closeForm = () => {
    if (!form || !viewCurrent(form)) return;
    form.retired = true; view.form = undefined;
    setForm(undefined); setTyping(undefined); controller.clearJoinFailure();
    if (view.pending?.form === form) {const original = view.pending; view.pending = undefined; original.abort.abort();}
  };
  const enter = async (work: (lifetime: GroupEnterLifetime) => Promise<boolean>, expected: Form | undefined) => {
    if (!viewCurrent(expected) || view.pending) return false;
    const flight = {abort: new AbortController(), form: expected}; view.pending = flight;
    const current = () => view.pending === flight && !flight.abort.signal.aborted && viewCurrent(expected)
      && view.pending === flight && !flight.abort.signal.aborted;
    const stop = () => {if (!current()) flight.abort.abort();};
    const offNavigation = ports.context.navigation.subscribe(stop), offAuthority = ports.context.subscribeAuthority(stop);
    const retire = () => flight.abort.abort(); page.signal.addEventListener('abort', retire, {once: true});
    try {if (!current()) return false; const accepted = await work({signal: flight.abort.signal, isCurrent: current}); return current() && accepted;}
    finally {page.signal.removeEventListener('abort', retire); offNavigation(); offAuthority(); if (view.pending === flight) view.pending = undefined;}
  };
  useLayoutEffect(() => {view.active = true; return () => {
    view.active = false; if (view.form) view.form.retired = true;
    const original = view.pending; view.pending = undefined; original?.abort.abort();
  };}, [view]);
  const run = (work: () => Promise<unknown>) => {if (!viewCurrent(form)) return; void work().catch(error => {if (viewCurrent(form)) runtime.reportError(error);});};
  // The refresh belongs to this page/controller opening, not to the form: it must not run again
  // when a field changes or a new handler identity is created. `view` is the private binding for
  // that opening and its `form` field is the exact form lifetime the opening holds, so the guard
  // reads the committed form without adding either the form state or a render ref to the trigger.
  useEffect(() => {
    const expected = view.form;
    if (!viewCurrent(expected)) return;
    void controller.refresh().catch(error => {if (viewCurrent(expected)) runtime.reportError(error);});
  }, [controller, runtime, view, viewCurrent]);
  const room = group && !['idle', 'left', 'joining'].includes(state.phase);
  const back = () => {
    if (!viewCurrent(form)) return;
    if (form) {closeForm(); return;}
    const original = view.pending; view.pending = undefined; original?.abort.abort(); if (viewCurrent(form)) onBack?.();
  };
  if (room && state.phase === 'ended') return <TogetherPage title={t('entry.group.together')} onBack={onBack ? back : undefined}><TogetherEndedScreen controller={controller} reason={group.endedReason} text={runtime.text} current={() => viewCurrent(form)}/></TogetherPage>;
  if (room) return <TogetherPage title={group.name} onBack={onBack ? back : undefined}>{theme.isTV ? <NativeTogetherTVRoom key={`${group.id}/${group.viewerMemberId}`} controller={controller} media={media} state={state} group={group} ports={ports} runtime={runtime}/> : <TogetherRoom controller={controller} ports={ports} runtime={runtime}/>}</TogetherPage>;
  const busy = state.busy || state.phase === 'joining';
  const fieldsRegion = ids.surface('together.form'), keyboard = ids.surface('together.keyboard');
  const type = (field: 'name' | 'code' | 'who') => {if (viewCurrent(form) && theme.isTV) {setTypingIntent(typing === field ? 'restore' : 'enter'); setTyping(field); setTypingRevision(value => value + 1);}};
  const openForm = (kind: Form['kind']) => {
    if (!viewCurrent(undefined) || view.pending) return;
    const defaultName = kind === 'start' && !name.trim() ? t('web.together.defaultName', {name: runtime.getSnapshot().profile.name}) : undefined;
    if (!viewCurrent(undefined)) return;
    const next: Form = {kind, retired: false}; view.form = next; setForm(next);
    if (defaultName !== undefined) setName(defaultName);
    setTyping(theme.isTV && kind === 'join' ? 'code' : undefined);
    if (theme.isTV && kind === 'join') {setTypingIntent('enter'); setTypingRevision(value => value + 1);}
  };
  return <TogetherPage title={t('entry.group.together')} settingsBack onBack={onBack ? back : undefined}><Screen title={phone ? undefined : t('entry.group.together')}>
    <ScrollView contentContainerStyle={{paddingHorizontal: theme.isTV ? 0 : phone ? 16 : 20, paddingTop: phone ? 6 : 0, paddingBottom: 64}}>
      {state.error && <Banner message={state.error} tone="warning"/>}
      <TogetherLobbyRegion controller={controller} region={region} rail={ids.rail}>
        <View style={{width: theme.isTV ? 1180 : '100%', backgroundColor: theme.colors.raised, borderRadius: theme.isTV ? 28 : 18, paddingHorizontal: theme.isTV ? 56 : phone ? 18 : 24, paddingTop: theme.isTV ? 48 : phone ? 20 : 24, paddingBottom: theme.isTV ? 52 : phone ? 18 : 24, gap: theme.isTV || phone ? 0 : 18, overflow: 'hidden'}}>
          <TogetherLobbyArt itemId={state.directory[0]?.timeline.itemId} controller={controller} ports={ports} runtime={runtime}/>
          <View pointerEvents="none" style={{position: 'absolute', inset: 0, borderRadius: theme.isTV ? 28 : 18, borderWidth: 1.5, borderColor: alpha(theme.colors.text, .08)}}/>
          <View testID="together-lobby-faces" style={{flexDirection: 'row', height: theme.isTV ? 56 : phone ? 36 : 32, marginBottom: phone ? 14 : 0}}>{faces.map((face, index) => <View key={face.id} accessibilityLabel={face.private ? t('together.privateViewer') : undefined} style={{marginLeft: index ? theme.isTV ? -14 : phone ? -9 : -8 : 0, borderRadius: theme.isTV ? 28 : phone ? 18 : 16}}>
            <View pointerEvents="none" style={{position: 'absolute', inset: phone ? -2.5 : -3, borderRadius: theme.isTV ? 31 : phone ? 20.5 : 19, borderWidth: phone ? 2.5 : 3, borderColor: theme.colors.raised}}/>{face.private ? <View style={{width: theme.isTV ? 56 : phone ? 36 : 32, height: theme.isTV ? 56 : phone ? 36 : 32, borderRadius: theme.isTV ? 28 : phone ? 18 : 16, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedHigh}}><Icon name="i-user" size={theme.isTV ? 28 : 18} color={theme.colors.textSecondary}/></View> : <ProfileFace initial={face.initial} color={face.color} source={face.source} size={theme.isTV ? 56 : phone ? 36 : 32} scaleOnFocus={false}/>}
          </View>)}</View>
          <Text style={[shellType(theme.isTV ? 62 : phone ? 22 : 30, 800, theme.isTV ? 64.48 : phone ? 25.3 : 'normal', theme.isTV ? -.04 : phone ? -.025 : -.035), {color: theme.colors.text, maxWidth: theme.isTV ? 920 : undefined, marginTop: theme.isTV ? 26 : 0, marginBottom: theme.isTV ? 14 : phone ? 6 : 0}]}>{t('web.together.tagline')}</Text>
          <Text style={[shellType(theme.isTV ? 26 : phone ? 14 : 15, 400, theme.isTV ? 39 : phone ? 21 : 23), {color: theme.colors.textSecondary, maxWidth: theme.isTV ? 820 : undefined, marginBottom: theme.isTV ? 38 : phone ? 16 : 0}]}>{t('web.together.taglineBody')}</Text>
          <View style={{flexDirection: phone ? 'column' : 'row', flexWrap: phone ? undefined : 'wrap', gap: theme.isTV ? 18 : 10, marginTop: theme.isTV || phone ? 0 : 8}}>
            <Button style={phone ? {alignSelf: 'stretch', height: 50, borderRadius: 10} : undefined} textStyle={phone ? shellType(15, 700) : undefined} ref={startRef} focusId="start" testID="together-start" disabled={busy} icon="i-people" onPress={() => openForm('start')}>{t('web.together.startTitle')}</Button>
            <Button style={phone ? {alignSelf: 'stretch', height: 50, borderRadius: 10} : undefined} textStyle={phone ? shellType(15, 700) : undefined} ref={joinRef} focusId="join" testID="together-join" disabled={busy} variant="glass" icon="i-kbd" onPress={() => openForm('join')}>{t('web.together.joinWithCode')}</Button>
          </View>
        </View>
        {state.directory.length > 0 && <View style={{width: theme.isTV ? 1180 : '100%', marginTop: theme.isTV ? 38 : 28, gap: theme.isTV ? 16 : 12}}>
          <Text style={[shellType(theme.isTV ? 20 : 15, 700), {color: theme.colors.textTertiary}]}>{t('web.together.yourGroups')}</Text>
          {state.directory.map(value => <Focusable key={value.id} focusId={`group:${value.id}`} testID={`together-group-${value.id}`} accessibilityRole="button" accessibilityLabel={value.name} onPress={() => run(() => enter(lifetime => controller.open(value.id, lifetime), undefined))} focusKind="row" style={{minHeight: theme.isTV ? 104 : undefined, paddingHorizontal: theme.isTV ? 28 : phone ? 18 : 16, paddingVertical: theme.isTV ? 0 : 16, borderRadius: theme.isTV ? 18 : 16, backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: alpha(theme.colors.line, .7), flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 22 : 20}}>
            <View style={{width: theme.isTV ? 56 : phone ? 36 : 32, height: theme.isTV ? 56 : phone ? 36 : 32, borderRadius: theme.isTV ? 14 : 8, backgroundColor: alpha(theme.colors.text, .08), alignItems: 'center', justifyContent: 'center'}}><Icon name="i-people" size={theme.isTV ? 32 : 24} color={theme.colors.textSecondary}/></View><View style={{flex: 1, gap: theme.isTV ? 3 : 8}}><Text style={[shellType(theme.isTV ? 28 : 15, 700), {color: theme.colors.text}]}>{value.name}</Text><SpacedStatus size={theme.isTV ? 22 : undefined} tertiary text={t('web.together.groupLine', {count: value.members.filter(member => member.state === 'joined').length, state: t(value.timeline.state === 'playing' ? 'web.together.state.playing' : value.timeline.state === 'paused' ? 'web.downloads.state.paused' : 'web.together.state.lobby')})}/></View>{value.permissions.isHost && <Text style={[shellType(theme.isTV ? 22 : 15, 600), {color: theme.colors.textSecondary}]}>{t('web.together.youHost')}</Text>}<Icon name="i-right" size={theme.isTV ? 28 : 24} color={theme.colors.textSecondary}/>
          </Focusable>)}
        </View>}
      </TogetherLobbyRegion>
      {state.directoryError && <Banner message={state.directoryError} tone="warning"/>}
    </ScrollView>
    <Dialog testID="together-form" open={sheet !== undefined} onOpenChange={open => {if (!open) closeForm();}} headerContext={<ScreenText secondary>{t('entry.group.together')}</ScreenText>} title={t(sheet === 'join' ? 'web.together.joinWithCode' : 'web.together.startTitle')} description={t(sheet === 'join' ? 'web.together.joinHelp' : 'web.together.startHelp')} placement={theme.isTV ? 'side' : undefined} width={theme.isTV ? sheet === 'join' || typing ? 1240 : 760 : undefined} initialFocusRef={theme.isTV ? typing ? undefined : firstTVField : firstField} returnFocusRef={sheet === 'join' ? joinRef : startRef} footer={<Button testID="together-submit" onFocus={() => {if (viewCurrent(form) && sheet === 'start') setTyping(undefined);}} loading={busy} disabled={busy || !who.trim() || (sheet === 'join' ? code.replace(/[\s-]/g, '').length !== 8 : !name.trim())} onPress={() => run(async () => {const ok = await enter(lifetime => sheet === 'join' ? controller.join(code, who, lifetime) : controller.create(name, who, authority, lifetime), form); if (ok && viewCurrent(form)) closeForm();})}>{t(sheet === 'join' ? 'web.together.join' : 'web.together.start')}</Button>}>
      {state.error && !codeFailure && <Banner message={state.error} tone="warning"/>}
      <View style={{flexDirection: theme.isTV && typing ? 'row' : 'column', gap: theme.isTV && typing ? 48 : 24}}>
        <FocusRegion id={fieldsRegion} entry={sheet === 'join' ? 'code' : 'name'} edges={typing ? {right: keyboard} : undefined} style={{gap: 24, flex: 1}}>
        {theme.isTV ? <>
          <TogetherTVField ref={firstTVField} focusId={sheet === 'join' ? 'code' : 'name'} testID={sheet === 'join' ? 'together-code' : 'together-name'} label={t(sheet === 'join' ? 'web.together.hostCode' : 'profile.name')} value={sheet === 'join' ? code : name} onPress={() => type(sheet === 'join' ? 'code' : 'name')}/>
          <TogetherTVField focusId="who" testID="together-display-name" label={t('web.together.yourName')} description={t('web.together.shownToOthers')} value={who} onPress={() => type('who')}/>
        </> : <>
          {sheet === 'join' ? <TextField ref={firstField} focusId="code" testID="together-code" label={t('web.together.hostCode')} value={code} error={codeFailure ? state.error : undefined} onChangeText={value => {if (viewCurrent(form)) {controller.clearJoinFailure(); setCode(value.toUpperCase());}}} autoCapitalize="characters" maxLength={12}/>
            : <TextField ref={firstField} focusId="name" testID="together-name" label={t('profile.name')} value={name} onChangeText={value => {if (viewCurrent(form)) setName(value);}} maxLength={80}/>}
          <TextField focusId="who" testID="together-display-name" label={t('web.together.yourName')} description={t('web.together.shownToOthers')} value={who} onChangeText={value => {if (viewCurrent(form)) setWho(value);}} maxLength={64}/>
        </>}
        {sheet === 'start' && <View style={{gap: theme.isTV ? 8 : 12}}><Text style={[shellType(theme.isTV ? 20 : 15, 700), {color: theme.colors.textTertiary}]}>{t('web.together.whoControls')}</Text><Segmented testID="together-authority" accessibilityLabel={t('web.together.whoControls')} value={authority} onValueChange={value => {if (viewCurrent(form)) setAuthority(value as GroupHostAuthority);}} items={(['host-only', 'anyone'] as const).map(value => ({value, label: t(value === 'host-only' ? 'web.together.onlyMe' : 'web.together.everyone')}))} itemProps={item => ({testID: `together-authority-${item.value}`, onFocus: () => {if (viewCurrent(form)) setTyping(undefined);}})}/></View>}
        </FocusRegion>{theme.isTV && typing && <NativeTVKeyboard id={keyboard} value={typing === 'code' ? code : typing === 'name' ? name : who} onChangeText={value => {if (!viewCurrent(form)) return; if (typing === 'code') setCode(value.toUpperCase().slice(0, 12)); else if (typing === 'name') setName(value.slice(0, 80)); else setWho(value.slice(0, 64));}} activation={{visible: sheet !== undefined, intent: typingIntent, revision: typingRevision}} leftRegionId={fieldsRegion} width={500} testID="together-keyboard"/>}
      </View>
    </Dialog>
  </Screen></TogetherPage>;
}

/** This region remounts on return from a room/ended screen. Its entry request must
 * follow that real native host's layout rather than reuse the old lobby's request. */
function TogetherLobbyRegion({controller, region, rail, children}: {controller: NativeTogetherController; region: string; rail: string; children: React.ReactNode}) {
  const theme = useTheme();
  const enter = useScreenEntry(theme.isTV ? {regionId: region, childId: 'start'} : undefined, controller.lifetime.current);
  return <FocusRegion id={region} entry="start" orientation="free" edges={{left: rail}} onLayout={enter}>{children}</FocusRegion>;
}

function TogetherPage({children, onBack, title, settingsBack = false}: {children: React.ReactNode; onBack?: () => void; title?: string; settingsBack?: boolean}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), phone = !theme.isTV && theme.width < 600;
  return <View style={{flex: 1}}><StandardBackdrop/>
    {phone && <View style={{paddingTop: insets.top, minHeight: insets.top + 46, paddingHorizontal: 10, justifyContent: 'center'}}>
      <Text accessibilityRole="header" pointerEvents="none" style={[shellType(17, 700), {position: 'absolute', left: 0, right: 0, bottom: 13, textAlign: 'center', color: theme.colors.text}]}>{title}</Text>
      {onBack && <Button variant="text" size="small" icon="i-left" accessibilityLabel={settingsBack ? defaultI18n.t('next.shell.settings') : defaultI18n.t('action.back')} testID="together-page-back" onPress={onBack} style={{alignSelf: 'flex-start', minHeight: 38, paddingHorizontal: 6, marginLeft: -4}} textStyle={[shellType(16, 600), {color: theme.colors.textSecondary}]}>{settingsBack ? defaultI18n.t('next.shell.settings') : undefined}</Button>}
    </View>}
    {children}
    {!theme.isTV && !phone && onBack && <Button variant="icon" icon="i-left" iconOnly accessibilityLabel={defaultI18n.t('action.back')} testID="together-page-back" onPress={onBack} style={{position: 'absolute', top: insets.top + 6, left: 10, zIndex: 10, height: 44, width: 44}}/>}
  </View>;
}

function TogetherEndedScreen({controller, reason, text, current}: {current: () => boolean; controller: NativeTogetherController; reason: string; text: NativeShellRuntime['text']}) {
  const theme = useTheme(), t = text.t;
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]), region = ids.surface('together.ended');
  const [height, setHeight] = useState(0);
  const enter = useScreenEntry(theme.isTV ? {regionId: region, childId: 'together-ended-lobby'} : undefined, controller.lifetime.current);
  const body = t(reason === 'host-ended' ? 'web.together.ended.host' : reason === 'host-unavailable' ? 'web.together.ended.away' : reason === 'empty' ? 'web.together.ended.empty' : 'web.together.ended.other');
  return <Screen style={theme.isTV ? {paddingLeft: 0, paddingRight: 0, paddingTop: 0, alignItems: 'center', justifyContent: 'center'} : undefined}>
    <FocusRegion id={region} entry="together-ended-lobby" edges={{left: ids.rail}} onLayout={event => {setHeight(event.nativeEvent.layout.height); enter();}} style={theme.isTV ? {width: 900, alignItems: 'center', transform: [{translateY: height * -.02}]} : undefined}>
      {theme.isTV ? <>
        <View testID="together-ended" style={{width: 112, height: 112, borderRadius: 56, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .08), marginBottom: 30}}><Icon name="i-people" size={54} color={theme.colors.textSecondary}/></View>
        <Text accessibilityRole="header" style={[shellType(54, 800, 58.32, -.035), {color: theme.colors.text, textAlign: 'center', marginBottom: 14}]}>{t('web.together.endedTitle')}</Text>
        <Text style={[shellType(26, 400, 39), {color: theme.colors.textSecondary, textAlign: 'center', marginBottom: 40}]}>{body}</Text>
        <Button focusId="together-ended-lobby" testID="together-ended-lobby" onPress={() => {if (current()) controller.dismiss();}}>{t('web.together.backToLobby')}</Button>
      </> : <StateView testID="together-ended" variant="centered" icon="i-people" title={t('web.together.endedTitle')} body={body} primaryAction={{id: 'lobby', testID: 'together-ended-lobby', label: t('web.together.backToLobby'), onPress: () => {if (current()) controller.dismiss();}}}/>}
    </FocusRegion>
  </Screen>;
}

/** tv-more13: focus reads the value; OK opens the actual shared keyboard. */
const TogetherTVField = forwardRef<View, {focusId: string; testID: string; label: string; description?: string; value: string; onPress: () => void}>(function TogetherTVField({focusId, testID, label, description, value, onPress}, ref) {
  const theme = useTheme();
  return <View style={{gap: 8}}>
    <Text style={[shellType(20, 700), {color: theme.colors.textTertiary}]}>{label}</Text>
    <Focusable ref={ref} focusId={focusId} testID={testID} accessibilityRole="button" accessibilityLabel={label} accessibilityValue={{text: value}} onPress={onPress} focusKind="row"
      style={{height: 72, paddingHorizontal: 22, justifyContent: 'center', borderRadius: 14, borderWidth: 1.5, borderColor: theme.colors.lineStrong, backgroundColor: alpha(theme.colors.text, .08)}}>
      <Text numberOfLines={1} style={[shellType(32, 600), {color: theme.colors.text}]}>{value}</Text>
    </Focusable>
    {description && <Text style={[shellType(20, 400), {color: theme.colors.textTertiary, marginTop: 4}]}>{description}</Text>}
  </View>;
});

/** One authorized current title, never a demo backdrop or a catalogue scan. */
function TogetherLobbyArt({itemId, controller, ports, runtime}: {itemId?: string; controller: NativeTogetherController; ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime}) {
  const [item, setItem] = useState<MediaItem>();
  useEffect(() => {
    setItem(undefined);
    if (!itemId) return;
    const abort = new AbortController();
    void controller.lifetime.request(signal => ports.context.api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}), 'GET', undefined, signal).then(parseMediaItem), abort.signal)
      .then(value => {if (!abort.signal.aborted && controller.lifetime.current() && value.id === itemId && value.available) setItem(value);}, () => {});
    return () => abort.abort();
  }, [itemId, controller, ports]);
  const source = item && item.id === itemId ? runtime.ambient(item.backdropUrl) : undefined;
  return source ? <AmbientArt testID="together-lobby-art" source={source} treatment="wash"/> : null;
}

function TogetherRoom({controller, ports, runtime}: {controller: NativeTogetherController; ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t, phone = !theme.isTV && theme.width < 600, gradientId = useId().replace(/:/g, '');
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), group = state.group!;
  const playback = useSyncExternalStore(ports.context.playback.subscribe, ports.context.playback.getSnapshot);
  const [items, setItems] = useState<Readonly<Record<string, MediaItem | null>>>({}), [confirm, setConfirm] = useState<'end' | 'leave'>();
  const [memberMenu, setMemberMenu] = useState<string>(), [queueMenu, setQueueMenu] = useState<string>();
  const menuReturn = useRef<View | null>(null), memberRefs = useRef(new Map<string, View>()), queueRefs = useRef(new Map<string, View>());
  const [clock, setClock] = useState(controller.serverNow);
  const cancel = useRef<View>(null), endRef = useRef<View>(null), leaveRef = useRef<View>(null);
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]), region = ids.surface('together.room');
  const canControl = group.permissions.canControl, playing = group.timeline.state === 'playing';
  const enter = useScreenEntry(theme.isTV ? {regionId: region, childId: canControl && group.timeline.itemId ? 'transport' : 'leave'} : undefined, controller.lifetime.current);
  const key = JSON.stringify([...new Set([group.timeline.itemId, ...(state.queue?.entries.filter(value => !value.unavailable).map(value => value.itemId) ?? [])].filter((id): id is string => !!id))]);
  useEffect(() => {
    let active = true;
    for (const id of JSON.parse(key) as string[]) void controller.lifetime.request(signal => ports.context.api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(id)}), 'GET', undefined, signal).then(parseMediaItem)).then(item => {if (active) setItems(value => ({...value, [id]: item}));}, () => {if (active && controller.lifetime.current()) setItems(value => ({...value, [id]: null}));});
    return () => {active = false;};
  }, [key, controller, ports]);
  useEffect(() => {void controller.refreshQueue().catch(error => {if (controller.lifetime.current()) runtime.reportError(error);});}, [controller, runtime, group.queueRevision]);
  useEffect(() => {const timer = setInterval(() => setClock(controller.serverNow()), 1000); return () => clearInterval(timer);}, [controller]);
  const run = (work: () => Promise<unknown>) => {controller.lifetime.assert(); void work().catch(error => {if (controller.lifetime.current()) runtime.reportError(error);});};
  const now = group.timeline.itemId ? items[group.timeline.itemId] : undefined;
  const title = !group.timeline.itemId ? t(group.timeline.currentEntryId ? 'web.together.hiddenTitle' : 'web.together.nothingChosen') : now === null ? t('web.together.hiddenTitle') : now?.title ?? t('web.together.loadingTitle');
  const art = now?.backdropUrl ? runtime.artwork(now.backdropUrl) : undefined;
  const members = group.members.filter(value => value.state === 'joined'), profile = runtime.getSnapshot().profile;
  const hostDeadline = group.timeline.state === 'playing' ? group.host.pauseAt : group.host.endAt;
  const hostCountdown = hostDeadline ? defaultI18n.clock(Math.max(0, Math.ceil((Date.parse(hostDeadline) - clock) / 1000))) : undefined;
  const nowAndTransport = <>
            <View testID="together-now-card" style={{borderRadius: phone ? 16 : 22, overflow: 'hidden', minHeight: theme.isTV ? 300 : phone ? 196 : 180, justifyContent: 'flex-end', padding: phone ? 14 : 30, backgroundColor: theme.colors.raised}}>
              {art && <View style={{position: 'absolute', inset: 0, opacity: phone ? 1 : .35}}><Artwork source={art}/></View>}
              {phone && <View pointerEvents="none" style={{position: 'absolute', inset: 0}}><Svg width="100%" height="100%" accessible={false}><Defs><LinearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.1}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.9}/></LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${gradientId})`}/></Svg></View>}
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: phone ? 0 : 6}}>{phone && <View style={{width: 7, height: 7, borderRadius: 4, backgroundColor: group.host.presence !== 'connected' ? theme.colors.amber : theme.colors.healthy}}/>}<Text style={[shellType(theme.isTV ? 22 : phone ? 12.5 : 13, phone ? 800 : 700), {color: group.host.presence !== 'connected' ? theme.colors.amber : phone ? theme.colors.healthy : theme.colors.accentStrong}]}>{t(group.host.presence !== 'connected' ? 'web.together.state.hostReconnecting' : playing ? 'web.together.state.playing' : 'web.downloads.state.paused')}</Text></View><Text style={[shellType(theme.isTV ? 40 : phone ? 20 : 25, 800, 'normal', phone ? -.02 : 0), {color: theme.colors.text, marginTop: phone ? 4 : 12, marginBottom: phone ? 2 : 12}]}>{title}</Text>
              {now?.duration ? (() => {const seconds = Number(groupTargetPositionUs(group.timeline, Date.parse(group.timeline.anchorAt), clock) / 1000000n);return <>
                <Text style={[shellType(phone ? 13 : 15, 400), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{t('web.together.positionOf', {position: defaultI18n.clock(seconds), duration: defaultI18n.clock(now.duration)})}</Text>
                <View style={{height: phone ? 3 : 4, borderRadius: 2, backgroundColor: alpha(theme.colors.text, .16), marginTop: 10, overflow: 'hidden'}}><View style={{height: phone ? 3 : 4, width: `${Math.max(0, Math.min(1, seconds / now.duration)) * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>
              </>;})() : null}
              <SpacedStatus text={group.readiness.aggregate === 'ready' ? t('web.together.allReady') : t('web.together.waiting', {ready: group.readiness.ready, total: group.readiness.memberCount, count: group.readiness.buffering + group.readiness.lagging + group.readiness.stale})}/>
            </View>
            <View testID="together-transport-actions" style={{flexDirection: phone ? 'column' : 'row', gap: phone ? 10 : 18}}>{canControl && group.timeline.currentEntryId && <Button style={phone ? {alignSelf: 'stretch', height: 50, borderRadius: 10} : undefined} textStyle={phone ? shellType(15, 700) : undefined} focusId="transport" testID="together-transport" icon={playing ? 'i-pause' : 'i-play'} disabled={state.busy} onPress={() => run(() => controller.transport(playing ? 'pause' : 'play'))}>{t(playing ? 'web.together.pauseAll' : group.readiness.ready < group.readiness.memberCount ? 'web.together.playAnyway' : 'web.together.playAll')}</Button>}
              {playback.fence && playback.playback.itemId === group.timeline.itemId && <Button style={phone ? {alignSelf: 'stretch', height: 50, borderRadius: 10} : undefined} textStyle={phone ? shellType(15, 700) : undefined} testID="together-open-player" variant="glass" icon="i-tv" onPress={() => {controller.lifetime.assert(); const current = ports.context.playback.getSnapshot(); if (current.fence && current.playback.itemId === group.timeline.itemId) ports.context.playback.open(current.fence);}}>{t('web.together.openPlayer')}</Button>}
            </View>
            {!canControl && <ScreenText secondary>{t('web.together.pill.hostControls')}</ScreenText>}
  </>;
  const queueAndExit = <>
            <ScreenText secondary>{t('player.queue')}</ScreenText>
            {!state.queue?.entries.length && <ScreenText secondary>{t(group.permissions.canManageQueue ? 'web.together.emptyQueueHost' : 'web.together.emptyQueue')}</ScreenText>}
            {(state.queue?.entries ?? []).map(value => <Focusable key={value.entryId} ref={node => {if (node) queueRefs.current.set(value.entryId, node); else queueRefs.current.delete(value.entryId);}} testID={`together-queue-${value.entryId}`} accessibilityRole={group.permissions.canManageQueue || canControl ? 'button' : 'text'} accessibilityLabel={value.unavailable || !value.itemId || items[value.itemId] === null ? t('web.together.notAvailable') : items[value.itemId]?.title ?? t('web.together.loadingTitle')} onPress={group.permissions.canManageQueue || canControl ? () => {menuReturn.current = queueRefs.current.get(value.entryId) ?? null; setQueueMenu(value.entryId);} : undefined} focusKind="row" style={{backgroundColor: theme.colors.raised, borderRadius: 12, padding: 18}}><ScreenText>{value.unavailable || !value.itemId || items[value.itemId] === null ? t('web.together.notAvailable') : items[value.itemId]?.title ?? t('web.together.loadingTitle')}</ScreenText></Focusable>)}
            <View style={{flexDirection: 'row', gap: 18, marginTop: 36}}><Button ref={leaveRef} focusId="leave" testID="together-leave" variant="glass" onPress={() => group.permissions.isHost && members.length > 1 ? setConfirm('leave') : run(() => controller.leave())}>{t('web.together.leave')}</Button>{group.permissions.isHost && <Button ref={endRef} testID="together-end" variant="glass" onPress={() => setConfirm('end')}>{t('web.together.endAll')}</Button>}</View>
  </>;
  const peopleAndInvite = <><ScreenText secondary>{t('web.together.people', {count: members.length})}</ScreenText>
            <View style={{backgroundColor: theme.colors.raised, borderRadius: phone ? 14 : 20, overflow: 'hidden'}}>{members.map((member, index) => {
              const names = memberNames(member, t('together.privateViewer'));
              const primary = member.private || member.id !== group.viewerMemberId ? names.primary : t('web.together.you', {name: names.primary});
              return <Focusable key={member.id} ref={node => {if (node) memberRefs.current.set(member.id, node); else memberRefs.current.delete(member.id);}} testID={`together-member-${member.id}`} accessibilityRole={group.permissions.isHost && member.role !== 'host' && member.presence === 'connected' && member.readiness === 'ready' ? 'button' : 'text'} accessibilityLabel={names.secondary ? `${primary} ${names.secondary}` : primary} onPress={group.permissions.isHost && member.role !== 'host' && member.presence === 'connected' && member.readiness === 'ready' ? () => {menuReturn.current = memberRefs.current.get(member.id) ?? null; setMemberMenu(member.id);} : undefined} focusKind="row" style={{flexDirection: 'row', paddingHorizontal: phone ? 14 : 20, paddingVertical: phone ? 9 : 20, gap: phone ? 12 : 16, alignItems: 'center'}}>{phone && index > 0 && <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 62, right: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>}{member.private ? <View style={{width: phone ? 34 : 32, height: phone ? 34 : 32, borderRadius: phone ? 17 : 16, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedHigh}}><Icon name="i-user" size={18} color={theme.colors.textSecondary}/></View> : <ProfileFace size={phone ? 34 : 32} initial={[...member.displayName.trim()][0] ?? ''} color={member.id === group.viewerMemberId ? profile.color : undefined} source={member.id === group.viewerMemberId ? profile.source : undefined} scaleOnFocus={false}/>}<View style={{flex: 1}}><ScreenText>{primary}</ScreenText>{names.secondary && <Text style={[shellType(phone ? 12.5 : 15, phone ? 500 : 400), {color: phone ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{names.secondary}</Text>}{member.role === 'host' && <Text style={[shellType(phone ? 12.5 : 15, phone ? 500 : 400), {color: phone ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{t('web.together.pill.host')}</Text>}</View><Text style={[shellType(phone ? 12.5 : 15, phone ? 700 : 400), {color: phone ? member.presence === 'connected' && member.readiness === 'ready' ? theme.colors.healthy : theme.colors.amber : theme.colors.textSecondary}]}>{t(member.presence !== 'connected' ? 'web.together.away' : member.readiness === 'ready' ? 'web.together.inStep' : member.readiness === 'buffering' ? 'web.together.loading' : 'web.together.catchingUp')}</Text></Focusable>;})}</View>
            {group.permissions.isHost && <><ScreenText secondary>{t('web.together.invite')}</ScreenText><View testID="together-invite-card" style={{backgroundColor: theme.colors.raised, padding: phone ? 16 : 30, borderRadius: phone ? 14 : 20, gap: phone ? 0 : 18}}>
              {state.invite && Date.parse(state.invite.expiresAt) > clock && state.invite.uses < state.invite.maxUses && <><Text style={[shellType(theme.isTV ? 70 : phone ? 30 : 34, 800), {color: theme.colors.text, letterSpacing: phone ? 4.2 : 8, fontVariant: ['tabular-nums']}]}>{state.invite.code.slice(0, 4)} {state.invite.code.slice(4)}</Text><Text style={[shellType(phone ? 12.5 : 15, 400, phone ? 18.125 : 'normal'), {color: phone ? theme.colors.textTertiary : theme.colors.textSecondary, marginTop: phone ? 6 : 0, marginBottom: phone ? 12 : 0}]}>{t('web.together.inviteHelpJoin', {uses: state.invite.maxUses - state.invite.uses, time: defaultI18n.time(state.invite.expiresAt)})}</Text></>}
              <View style={{flexDirection: phone ? 'row' : 'column', flexWrap: 'wrap', gap: phone ? 8 : 18}}>
              {!theme.isTV && state.invite && Date.parse(state.invite.expiresAt) > clock && state.invite.uses < state.invite.maxUses && <Button variant="glass" size={phone ? 'small' : undefined} icon={phone ? 'i-share' : undefined} testID="together-share-code" onPress={() => {controller.lifetime.assert(); void Share.share({message: state.invite!.code}).catch(runtime.reportError);}}>{t('web.together.shareCode')}</Button>}
              <Button variant="glass" size={phone ? 'small' : undefined} testID="together-new-code" disabled={state.busy} onPress={() => run(() => controller.invite())}>{t(state.invite ? 'web.together.newCode' : 'web.together.getCode')}</Button>
              </View>
            </View></>}
  </>;
  return <Screen title={!theme.isTV && theme.width < 600 ? undefined : group.name}>
    <ScrollView contentContainerStyle={{paddingHorizontal: theme.isTV ? 0 : phone ? 16 : 20, paddingTop: phone ? 6 : 0, paddingBottom: 64}}>
      {state.error && <Banner message={state.error} tone="warning"/>}
      {state.phase === 'reconnecting' && <Banner message={t('web.together.reconnecting')} tone="warning"/>}
      {group.host.presence !== 'connected' && <Banner message={[t('web.together.hostDropped'), hostCountdown ? t(group.timeline.state === 'playing' ? 'web.together.pausesIn' : 'web.together.endsIn', {time: hostCountdown}) : undefined].filter(Boolean).join(' ')} tone="warning"/>}
      {state.refused && group.permissions.isHost && canControl && <Banner message={t('web.together.refused')} tone="warning" action={{label: t('web.together.goAhead'), onPress: () => run(() => controller.transport(state.refused!.command, {...state.refused!.input, allowUnavailable: true}))}}/>}
      <FocusRegion id={region} entry={canControl && group.timeline.itemId ? 'transport' : 'leave'} edges={{left: ids.rail}} onLayout={enter}>
        <View style={{flexDirection: phone ? 'column' : 'row', gap: theme.isTV ? 96 : phone ? 12 : 24}}>
          {phone ? <>{nowAndTransport}{peopleAndInvite}{queueAndExit}</> : <>
            <View style={{flex: 1, gap: 22}}>{nowAndTransport}{queueAndExit}</View>
            <View style={{width: theme.isTV ? 680 : 340, gap: 18}}>{peopleAndInvite}</View>
          </>}
        </View>
      </FocusRegion>
    </ScrollView>
    <Menu testID="together-member-menu" open={memberMenu !== undefined} onOpenChange={open => {if (!open) setMemberMenu(undefined);}} title={(() => {const member = members.find(value => value.id === memberMenu); return member ? memberNames(member, t('together.privateViewer')).primary : undefined;})()} presentation={theme.isTV ? 'sideSheet' : 'sheet'} returnFocusRef={menuReturn} items={memberMenu && group.permissions.isHost && members.some(member => member.id === memberMenu && member.role !== 'host' && member.presence === 'connected' && member.readiness === 'ready') ? [{id: 'host', label: t('web.together.makeHost'), disabled: state.busy, onSelect: () => run(() => controller.makeHost(memberMenu))}] : []}/>
    <Menu testID="together-queue-menu" open={queueMenu !== undefined} onOpenChange={open => {if (!open) setQueueMenu(undefined);}} title={queueMenu ? items[state.queue?.entries.find(value => value.entryId === queueMenu)?.itemId ?? '']?.title ?? t('web.together.notAvailable') : undefined} presentation={theme.isTV ? 'sideSheet' : 'sheet'} returnFocusRef={menuReturn} items={queueMenu ? [
      ...(canControl && state.queue?.entries.some(value => value.entryId === queueMenu && !value.unavailable && !!value.itemId) ? [{id: 'watch', label: t('web.together.watchThis'), disabled: state.busy, onSelect: () => run(() => controller.transport('load', {entryId: queueMenu}))}] : []),
      ...(group.permissions.canManageQueue ? [{id: 'remove', label: t('web.together.removeFromQueue'), disabled: state.busy, onSelect: () => run(() => controller.queueRemove(queueMenu))}] : []),
    ] : []}/>
    <Dialog open={confirm !== undefined} onOpenChange={open => {if (!open) setConfirm(undefined);}} title={t(confirm === 'end' ? 'confirm.endGroup.title' : 'confirm.leaveAsHost.title')} description={t(confirm === 'end' ? 'confirm.endGroup.body' : 'confirm.leaveAsHost.body')} initialFocusRef={cancel} returnFocusRef={confirm === 'end' ? endRef : leaveRef} testID="together-confirm" footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: theme.isTV ? 18 : 16}}><Button ref={cancel} variant="glass" onPress={() => setConfirm(undefined)}>{t('action.cancel')}</Button><Button variant="text" style={{backgroundColor: alpha(theme.colors.record, .14)}} textStyle={{color: theme.colors.record}} disabled={state.busy} onPress={() => run(async () => {const ended = confirm === 'end' ? await controller.end() : (await controller.leave(), true); if (ended && controller.lifetime.current()) setConfirm(undefined);})}>{t(confirm === 'end' ? 'confirm.endGroup.action' : 'confirm.leaveAsHost.action')}</Button></View>}><View/></Dialog>
  </Screen>;
}

/** The approved frames separate these catalogue facts with space, never a middle dot. */
function SpacedStatus({text, size, tertiary = false}: {text: string; size?: number; tertiary?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 16}}>{text.split(' · ').map((part, index) => <Text key={index} style={[shellType(size ?? (theme.isTV ? 24 : 15), 400), {color: tertiary ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{part}</Text>)}</View>;
}
