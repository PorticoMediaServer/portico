import React, {useRef, useState, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n, formatMessage} from '../../../../../../packages/i18n/src';
import {framesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures';
import {settingsFramesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/settings-frames-fixtures';
import {Button, Switch, Tabs, TextField} from '../../../kit/controls';
import {PosterCard} from '../../../kit/cards';
import {PageStateFrame, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {MenuRow} from '../../../kit/overlays';
import {OverlaysProvider} from '../../../kit/overlays/OverlayHost';
import {ChoiceMatrix, DefaultHint, ServerHeader, SettingsNav, SettingsProfileCard, SettingsRow, ShortcutTiles} from '../../../kit/shell';
import {DownloadsStillPlay} from '../../../kit/states/DownloadsStillPlay';
import {useTheme} from '../../../kit/theme';
import type {Gallery} from '../../gallery-registry';
import {Page, PosterRow, Window, asset, settingsPages} from './frames.gallery';

/** Family E (round 8): Settings, overlays by task (R9, packages/design `overlay-forms.ts`) over a real page, and the
 * page states, with the web gallery's case ids. Development builds only. */
type Name = keyof typeof settingsFramesFixtureMessages extends `next.settingsFrames.fixture.${infer N}` ? N : never;
type FrameName = keyof typeof framesFixtureMessages extends `next.frames.fixture.${infer N}` ? N : never;
const f = (name: Name) => formatMessage(settingsFramesFixtureMessages[`next.settingsFrames.fixture.${name}`]);
const fx = (name: FrameName) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
const t = defaultI18n.t;
const none = () => {};
const profile = {id: 'demo', name: f('profileName'), initial: f('profileName')[0]!, color: 'blue' as const};
const skipOptions = [{value: 'ask', label: fx('ask')}, {value: 'auto', label: f('skipAuto')}, {value: 'off', label: f('never')}];

/** desktop-settings f1, phone-app 18, tablet f12: Saved, section help, a switch with dependent rows, values, the choice
 * matrix with Changed and Reset. */
function PlaybackSections() {
  const [autoplay, setAutoplay] = useState(true), [credits, setCredits] = useState(false);
  const [skips, setSkips] = useState<Record<string, string>>({intros: 'ask', credits: 'ask', recaps: 'auto'});
  return <>
    <SectionCard label={fx('nextEpisode')}>
      <SettingsRow first label={fx('autoplay')} control={<Switch checked={autoplay} onCheckedChange={setAutoplay} accessibilityLabel={fx('autoplay')}/>}/>
      <SettingsRow dependent continuesBranch label={fx('wait')} description={fx('waitHelp')} value={fx('waitValue')} onPress={none}/>
      <SettingsRow dependent label={f('creditsRow')} control={<Switch checked={credits} onCheckedChange={setCredits} accessibilityLabel={f('creditsRow')}/>}/>
      <SettingsRow label={f('stillWatching')} description={f('stillWatchingHelp')} value={f('afterThree')} onPress={none}/>
    </SectionCard>
    <SectionCard label={fx('skipping')} help={f('skippingHelp')}>
      <ChoiceMatrix options={skipOptions} rows={([['intros', fx('intros')], ['credits', fx('credits')], ['recaps', f('recaps')]] as const).map(([id, label]) => ({id, label, value: skips[id]!,
        defaultHint: id === 'recaps' && skips[id] !== 'ask' ? <DefaultHint value={fx('ask')} changed onReset={() => setSkips({...skips, [id]: 'ask'})}/> : undefined}))} onChange={(id, value) => setSkips({...skips, [id]: value})}/>
      <SettingsRow label={f('skipBack')} description={f('skipBackHelp')} value={fx('waitValue')} onPress={none}/>
      <SettingsRow label={f('skipForward')} description={f('skipForwardHelp')} value={f('thirtySeconds')} onPress={none}/>
    </SectionCard>
  </>;
}
const tiles = ([['profiles', 'i-swap', 'next.shell.switchProfile'], ['servers', 'i-server', 'next.shell.switchServer'], ['notifications', 'i-bell', 'next.shell.notifications'], ['together', 'i-people', 'next.shell.watchTogether']] as const)
  .map(([id, icon, key]) => ({id, icon, label: t(key), onPress: none, trailing: id === 'notifications' ? <Badge/> : undefined}));
function Badge() { const theme = useTheme(); return <Text style={[theme.type.captionStrong, {color: theme.colors.white}]}>{f('badge')}</Text>; }
function Index({selected, onOpen}: {selected: string; onOpen: (id: string) => void}) {
  const {form} = useFrameMetrics();
  return <SettingsNav accountPages={settingsPages.account} serverPages={settingsPages.server} viewerRole="owner" serverName={fx('serverName')} selectedId={selected} onNavigate={onOpen} mode={form === 'tv' ? 'account' : 'combined'}
/>;
}
function Search() { return <View style={{marginHorizontal: 16, marginBottom: 10}}><TextField label={t('next.shell.searchSettings')} placeholder={t('next.shell.searchSettings')} value="" onChangeText={none}/></View>; }
function SettingsIndex() {
  const {form} = useFrameMetrics(), wide = form !== 'phone', [page, setPage] = useState<string>();
  return <Window><SettingsListFrame open onOpenChange={none} bar={{title: t('next.shell.settings'), action: {label: t('next.overlays.done'), onPress: none}}} search={<Search/>}
    profile={<SettingsProfileCard profile={profile} description={f('signedIn')} onPress={none}/>} shortcuts={<ShortcutTiles actions={tiles}/>}
    list={<Index selected={page ?? (wide ? 'playback' : '')} onOpen={setPage}/>}
    page={page || wide ? <SettingsPageFrame title={fx('playback')} status="saved" back={{label: t('next.shell.settings'), onPress: () => setPage(undefined)}} intro={fx('playbackIntro')}><PlaybackSections/></SettingsPageFrame> : undefined}/></Window>;
}
function OwnerPage({back}: {back?: () => void}) {
  const [tab, setTab] = useState('accounts'), {form} = useFrameMetrics();
  return <SettingsPageFrame title={f('people')} back={back ? {label: t('next.shell.settings'), onPress: back} : undefined} action={{label: f('invite'), onPress: none}} intro={f('peopleIntro')}
    actions={form === 'phone' ? undefined : <><Button variant="glass" size="small" onPress={none}>{f('addAccount')}</Button><Button variant="primary" size="small" icon="i-plus" onPress={none}>{f('invite')}</Button></>}
    server={<ServerHeader serverName={fx('serverName')} viewerRole="owner" audience="owner" variant={form === 'phone' ? 'phone' : 'page'}/>}
    tabs={<Tabs value={tab} onValueChange={setTab} items={[{value: 'accounts', label: `${f('accounts')} 5`}, {value: 'invitations', label: `${f('invitations')} 2`}, {value: 'devices', label: `${f('devices')} 3`}]}/>}>
    <SectionCard label={f('waiting')}><SettingsRow first icon="i-tv" label={f('device')} description={f('deviceFacts')} control={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" size="small" onPress={none}>{f('block')}</Button><Button variant="primary" size="small" onPress={none}>{f('approve')}</Button></View>}/></SectionCard>
    <SectionCard label={f('accounts')}>{([f('person1'), f('person2'), f('person3')]).map((name, index) => <SettingsRow key={name} first={index === 0} icon="i-person" label={name} navigates onPress={none}/>)}</SectionCard>
  </SettingsPageFrame>;
}
function SettingsOwner() {
  const {form} = useFrameMetrics(), [page, setPage] = useState<string | undefined>('people');
  return <Window><SettingsListFrame open onOpenChange={none} bar={{title: t('next.shell.settings'), action: {label: t('next.overlays.done'), onPress: none}}}
    list={<Index selected="people" onOpen={setPage}/>} page={page || form !== 'phone' ? <OwnerPage back={() => setPage(undefined)}/> : undefined}/></Window>;
}
/** A settings page shown alone (tablets SC-8): Back, the gutter and the page's column limit. */
const SettingsAlone = () => <Page destination={{kind: 'settings'}}><OwnerPage back={none}/></Page>;

/** Every overlay task over a real page (phones cause 9): a library row whose first card opened the menu. */
function Over({children}: {children: (anchor: React.RefObject<View | null>) => ReactNode}) {
  const anchor = useRef<View>(null), theme = useTheme(), width = theme.spec.card.width.poster[theme.platform];
  return <OverlaysProvider><Page destination={{kind: 'libraries'}}><View style={{paddingTop: 60, gap: 24}}>
    <View style={{flexDirection: 'row', gap: 12, paddingHorizontal: theme.gutter}}>
      <View ref={anchor} collapsable={false}><PosterCard title={f('cardTitle')} artwork={asset('pool/movies/alien.jpg')} width={width} onNavigate={none}/></View>
      <PosterCard title={fx('spirited')} artwork={asset('pool/movies/spirited-away.jpg')} width={width} onNavigate={none}/>
    </View><PosterRow title={f('recent')} fresh/></View></Page>{children(anchor)}</OverlaysProvider>;
}
const OverlayTask = () => <Over>{() => <SheetFrame kind="task" task="task" open onOpenChange={none} title={fx('addSource')} action={{label: fx('addSource'), onPress: none}}>
  <View style={{gap: 12, paddingHorizontal: 16}}><TextField label={f('sourceUrl')} value={f('sourceUrlValue')} onChangeText={none}/><TextField label={f('sourceName')} value={f('sourceNameValue')} onChangeText={none}/></View>
</SheetFrame>}</Over>;
const OverlayConfirm = () => <Over>{() => <SheetFrame kind="task" task="confirm" open onOpenChange={none} title={f('recordingTitle')}
  foot={<><Button variant="glass" onPress={none}>{t('next.overlays.cancel')}</Button><Button variant="danger" onPress={none}>{f('delete')}</Button></>}><Body text={f('recordingBody')}/></SheetFrame>}</Over>;
function Body({text}: {text: string}) { const theme = useTheme(); return <Text style={[theme.type.intro, {color: theme.colors.textSecondary, paddingHorizontal: 16}]}>{text}</Text>; }
const OverlayDetails = () => <Over>{() => <SheetFrame kind="task" task="details" open onOpenChange={none} title={f('fileInfo')} subtitle={f('fileTitle')}>
  <SectionCard label={f('version1')}><SettingsRow first label={f('videoFact')} value={f('videoValue')} readable/><SettingsRow label={f('audioFact')} value={f('audioValue')} readable/><SettingsRow label={f('sizeFact')} value={f('sizeValue')} readable/></SectionCard>
</SheetFrame>}</Over>;
const OverlayActions = () => <Over>{anchor => <SheetFrame kind="task" task="actions" open onOpenChange={none} title={f('cardTitle')} subtitle={f('cardFacts')} anchorRef={anchor}>
  {([['i-play', fx('play')], ['i-plus', f('addMyList')], ['i-check', f('markWatched')], ['i-info', f('fileInfo')]] as const).map(([icon, label]) => <MenuRow key={label} icon={icon} label={label} onPress={none}/>)}
  <MenuRow icon="i-trash" label={f('deleteMedia')} destructive onPress={none}/>
</SheetFrame>}</Over>;
const OverlayChoice = () => <Over>{anchor => <SheetFrame kind="task" task="choice" open onOpenChange={none} title={f('sortBy')} anchorRef={anchor}>
  {(['sortTitle', 'sortAdded', 'sortReleased', 'sortRating'] as const).map(name => <MenuRow key={name} label={f(name)} selected={name === 'sortAdded'} onPress={none}/>)}
</SheetFrame>}</Over>;

/** Frame 9 in full (states 4a, 4c): retry line, what still works, the buttons, help. On a tablet at the gutter (SC-11). */
const CantReach = () => <Page destination={{kind: 'home'}}><PageStateFrame icon="i-srvoff" tone="warning" title={fx('cantReach')} body={fx('cantReachBody')} retry={{progress: .4, label: f('retryIn')}}
  downloads={<DownloadsStillPlay covers={['pool/shows/slow-horses.jpg', 'pool/movies/dune.jpg', 'pool/movies/alien.jpg'].map(asset)} title={f('downloadsPlay')} detail={f('downloadsTitles')} onPress={none}/>}
  primaryAction={{id: 'retry', label: f('tryAgainNow'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'switch', label: fx('switchServer'), icon: 'i-swap', onPress: none}} help={f('stateHelp')}/></Page>;
const NotFound = () => <Page destination={{kind: 'libraries'}}><PageStateFrame icon="i-film" title={f('notFound')} body={f('notFoundBody')} primaryAction={{id: 'home', label: f('goHome'), onPress: none}}/></Page>;

const whole = {screen: true} as const;
const touch = ['phone', 'tablet'] as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-settings-index', title: 'next.platform.frame.settingsList', render: SettingsIndex, ...whole},
  {id: 'frames-settings-owner', title: 'next.platform.frame.settingsPage', render: SettingsOwner, ...whole},
  {id: 'frames-settings-alone', title: 'next.platform.frame.settingsPage', render: SettingsAlone, ...whole},
  {id: 'frames-overlay-task', title: 'next.platform.frame.sheet', render: OverlayTask, platforms: touch, ...whole},
  {id: 'frames-overlay-confirm', title: 'next.platform.frame.sheet', render: OverlayConfirm, platforms: touch, ...whole},
  {id: 'frames-overlay-details', title: 'next.platform.frame.sheet', render: OverlayDetails, platforms: touch, ...whole},
  {id: 'frames-overlay-actions', title: 'next.platform.frame.sheet', render: OverlayActions, platforms: touch, ...whole},
  {id: 'frames-overlay-choice', title: 'next.platform.frame.sheet', render: OverlayChoice, platforms: touch, ...whole},
  {id: 'frames-state-cant-reach', title: 'next.platform.frame.state', render: CantReach, ...whole},
  {id: 'frames-state-not-found', title: 'next.platform.frame.state', render: NotFound, ...whole},
];
export default galleries;
