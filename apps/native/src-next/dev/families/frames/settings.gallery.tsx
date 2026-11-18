import React, {useEffect, useRef, useState, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n, formatMessage} from '../../../../../../packages/i18n/src';
import {framesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures';
import {settingsFramesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/settings-frames-fixtures';
import {shellReference as sample} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/shell-fixtures';
import {Button, Chip, Switch, Tabs, TextField} from '../../../kit/controls';
import {ProfileFace, ProfilePicker, type ProfileOption} from '../../../kit/artwork';
import {Artwork} from '../../../kit/artwork/Artwork';
import {Icon} from '../../../kit/Icon';
import {PosterCard} from '../../../kit/cards';
import {PageStateFrame, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, SignInFrame} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {Banner, MenuRow, Popover} from '../../../kit/overlays';
import {OverlaysProvider} from '../../../kit/overlays/OverlayHost';
import {AccountMenu, ChoiceMatrix, DefaultHint, NotificationItem, ProfileAvatar, ServerHeader, SettingsNav, SettingsProfileCard, SettingsRow, ShortcutTiles} from '../../../kit/shell';
import {LegalAgreement} from '../../../screens/accounts/LegalAgreement';
import {PhonePINEntry} from '../../../screens/accounts/PhonePINEntry';
import {PhoneProfilePIN} from '../../../screens/accounts/PhoneProfilePIN';
import {PhoneWhosWatching} from '../../../screens/accounts/PhoneWhosWatching';
import {RememberProfileRow} from '../../../screens/accounts/RememberProfileRow';
import {DownloadsStillPlay} from '../../../kit/states/DownloadsStillPlay';
import {useTheme} from '../../../kit/theme';
import type {IconName} from '../../../kit/Icon';
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
/** The Settings list as drawn per form: phone-app 16/17 (the profile is the card above, so no Profile row; Language &
 * region, Privacy, This device; server rows carry their values), desktop-settings f1 and tablet f12 (the full list with
 * counts). */
const navPage = (page: {id: string; label: string; icon: string; trailing?: string; count?: number; warning?: boolean; cluster?: string}) =>
  ({id: page.id, label: page.label, icon: page.icon as IconName, stateValue: page.trailing ?? (page.count === undefined ? undefined : String(page.count)), majorIssue: page.warning, cluster: page.cluster as never});
function Index({selected, onOpen, query = ''}: {selected: string; onOpen: (id: string) => void; query?: string}) {
  const {form} = useFrameMetrics(), phone = form === 'phone';
  const matches = sample.settingsSearch.flatMap(page => page.rows.map(row => ({address: page.id, pageTitle: page.label, rowId: row.id, label: row.label})));
  return <SettingsNav accountPages={(phone ? sample.phoneAccountNav : sample.accountNav).map(navPage)} serverPages={(phone ? sample.phoneServerNav : sample.serverNav).map(navPage)} viewerRole="owner" serverName={fx('serverName')}
    selectedId={selected} onNavigate={onOpen} mode={form === 'tv' ? 'account' : 'combined'}
    searchResults={query ? {matches, query, selectedRowId: 'show-subtitles', onNavigate: none} : undefined}/>;
}
/** phone-app 16, desktop-settings f2: a search field with its glyph, no label over it. */
function Search({value, onChange}: {value: string; onChange: (value: string) => void}) {
  const frame = useFrameMetrics();
  return <View style={{marginHorizontal: frame.form === 'phone' ? frame.gutter : 0, marginBottom: frame.headingGap}}><TextField variant="search" leadingIcon="i-search" accessibilityLabel={t('next.shell.searchSettings')} placeholder={t('next.shell.searchSettings')} value={value} onChangeText={onChange}/></View>;
}
function SettingsIndex({search = false}: {search?: boolean}) {
  const {form} = useFrameMetrics(), wide = form !== 'phone', [page, setPage] = useState<string>(), [query, setQuery] = useState(search ? sample.settingsQuery : '');
  return <Window><SettingsListFrame open onOpenChange={none} bar={{title: t('next.shell.settings'), action: {label: t('next.overlays.done'), onPress: none}}} search={<Search value={query} onChange={setQuery}/>}
    profile={query ? undefined : <SettingsProfileCard profile={profile} description={f('signedIn')} onPress={none}/>} shortcuts={query ? undefined : <ShortcutTiles actions={tiles}/>}
    list={<Index selected={page ?? (wide ? 'playback' : '')} onOpen={setPage} query={query}/>}
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

/** An anchored overlay opens once the page has settled under it, as a real one opens on a press: opened at mount it
 * measures its trigger before the gallery has placed the case and lands off it. */
function useSettled() {
  const [settled, setSettled] = useState(false);
  useEffect(() => { const id = setTimeout(() => setSettled(true), 400); return () => clearTimeout(id); }, []);
  return settled;
}
/** Every overlay task over a real page (phones cause 9): a library row whose first card opened the menu. */
function Over({children}: {children: (anchor: React.RefObject<View | null>) => ReactNode}) {
  const anchor = useRef<View>(null), theme = useTheme(), width = theme.spec.card.width.poster[theme.column];
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
  <SectionCard label={f('version2')}><SettingsRow first label={f('videoFact')} value={f('video1080')} readable/><SettingsRow label={f('audioFact')} value={f('audio1080')} readable/><SettingsRow label={f('sizeFact')} value={f('size1080')} readable/></SectionCard>
</SheetFrame>}</Over>;
const OverlayActions = () => { const settled = useSettled(); return <Over>{anchor => <SheetFrame kind="task" task="actions" open={settled} onOpenChange={none} title={f('cardTitle')} subtitle={f('cardFacts')} anchorRef={anchor}>
  {([['i-play', fx('play')], ['i-plus', f('addMyList')], ['i-check', f('markWatched')], ['i-info', f('fileInfo')]] as const).map(([icon, label]) => <MenuRow key={label} icon={icon} label={label} onPress={none}/>)}
  <MenuRow icon="i-trash" label={f('deleteMedia')} destructive onPress={none}/>
</SheetFrame>}</Over>; };
/** A short choice drops from its own trigger and leaves it in view (phone-app 4b, 18): the library's sort order. */
function OverlayChoice() {
  const anchor = useRef<View>(null), theme = useTheme(), settled = useSettled(), [order, setOrder] = useState<'sortTitle' | 'sortAdded' | 'sortReleased' | 'sortRating'>('sortAdded');
  return <OverlaysProvider><Page destination={{kind: 'libraries'}}><View style={{paddingTop: 60, gap: theme.space.sm}}>
    <View style={{paddingHorizontal: theme.gutter, alignItems: 'flex-start'}}><View ref={anchor} collapsable={false}><Chip icon="i-sort" trailingIcon="i-down" onPress={none}>{f(order)}</Chip></View></View>
    <PosterRow title={f('recent')} fresh/><PosterRow title={fx('newOnServer')}/></View></Page>
    <SheetFrame kind="task" task="choice" open={settled} onOpenChange={none} title={f('sortBy')} anchorRef={anchor}>
      {(['sortTitle', 'sortAdded', 'sortReleased', 'sortRating'] as const).map(name => <MenuRow key={name} label={f(name)} selected={name === order} onPress={() => setOrder(name)}/>)}
    </SheetFrame></OverlaysProvider>;
}

/** Frame 9 in full (states 4a, 4c): retry line, what still works, the buttons, help. On a tablet at the gutter (SC-11). */
const CantReach = () => <Page destination={{kind: 'home'}}><PageStateFrame icon="i-srvoff" tone="warning" title={fx('cantReach')} body={fx('cantReachBody')} retry={{progress: .4, label: f('retryIn')}}
  downloads={<DownloadsStillPlay covers={['pool/shows/slow-horses.jpg', 'pool/movies/dune.jpg', 'pool/movies/alien.jpg'].map(asset)} title={f('downloadsPlay')} detail={f('downloadsTitles')} onPress={none}/>}
  primaryAction={{id: 'retry', label: f('tryAgainNow'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'switch', label: fx('switchServer'), icon: 'i-swap', onPress: none}} help={f('stateHelp')}/></Page>;
const NotFound = () => <Page destination={{kind: 'libraries'}}><PageStateFrame icon="i-film" title={f('notFound')} body={f('notFoundBody')} primaryAction={{id: 'home', label: f('goHome'), onPress: none}}/></Page>;

/** Sign-in and its steps on the frame (auth.html 4, 4b, 6; auth-6 mobile web): the server's own account with the saved
 * accounts as chips, the password's Show, the links and the terms sentence; locked out with its countdown; the two-step
 * code boxes with the recovery link; a server that uses Portico Accounts handing off. */
const remembered = [{id: 'nana', name: 'Nana', initial: 'N', color: 'coral' as const}, {id: 'theo', name: 'Theo', initial: 'T', color: 'mint' as const}];
function Terms() { const theme = useTheme(); return <LegalAgreement onOpenLink={none} style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]} linkStyle={{color: theme.colors.accentStrong}}/>; }
function Links({children}: {children: ReactNode}) { return <View style={{alignItems: 'flex-start', gap: 4}}>{children}</View>; }
function QuietLink({children}: {children: string}) { const theme = useTheme(); return <Button variant="link" textStyle={{color: theme.colors.text}} onPress={none}>{children}</Button>; }
function SignInServer({locked = false}: {locked?: boolean}) {
  const [chosen, setChosen] = useState('nana'), [password, setPassword] = useState(''), [shown, setShown] = useState(false), theme = useTheme();
  return <Window><SignInFrame kicker={t('accounts.signIn.to')} title={f('serverName')} lede={t('accounts.signIn.localBody')} terms={<Terms/>}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>{remembered.map(person => <Chip key={person.id} selected={chosen === person.id} onPress={() => setChosen(person.id)}
      prefix={<ProfileFace initial={person.initial} color={person.color} size={28} scaleOnFocus={false}/>}>{person.name}</Chip>)}</View>
    {locked && <Banner placement="inline" variant="notice" tone="warning" icon="i-clock" message={<><Text style={{fontWeight: '700'}}>{t('accounts.signIn.rateLimitedTitle')}</Text> {t('accounts.signIn.rateLimitedBody')}</>}/>}
    <TextField appearance="auth" label={t('accounts.signIn.username')} value={remembered.find(person => person.id === chosen)!.id === 'nana' ? 'nana_r' : 'theo'} onChangeText={none}/>
    <TextField appearance="auth" label={t('accounts.signIn.password')} value={password} onChangeText={setPassword} secureTextEntry={!shown} invalid={locked}
      trailing={<Button variant="text" size="compact" icon={shown ? 'i-eyeoff' : 'i-eye'} onPress={() => setShown(!shown)}>{t(shown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')}</Button>}/>
    <Button align="stretch" variant="primary" disabled={locked} onPress={none}>{locked ? t('services.signin.tryIn', {time: '0:23'}) : t('accounts.signIn.submit')}</Button>
    <Links><Button variant="link" onPress={none}>{t('auth.forgotPassword')}</Button><QuietLink>{t('web.directJoin.have')}</QuietLink></Links>
  </SignInFrame></Window>;
}
function SignInCode() {
  const [code, setCode] = useState('');
  return <Window><SignInFrame title={t('web.direct.codeTitle')} lede={t('services.signin.twoStep')}>
    <PhonePINEntry length={6} label={t('web.twoStep.code')} value={code} onChange={setCode} autoFocus={false}/>
    <RecoveryLine/>
    <Button align="stretch" variant="primary" disabled={code.length < 6} onPress={none}>{t('action.continue')}</Button>
    <Links><Button variant="link" onPress={none}>{t('auth.backToSignIn')}</Button></Links>
  </SignInFrame></Window>;
}
function RecoveryLine() {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 4}}><Text style={[theme.type.rowHelp]}>{t('services.signin.lostPhone')}</Text><Button variant="link" size="compact" onPress={none}>{t('services.signin.recovery')}</Button></View>;
}
const SignInPortico = () => <Window><SignInFrame kicker={t('accounts.signIn.to')} title={f('serverName')} lede={t('accounts.signIn.handoffReturnBody')} terms={<Terms/>}>
  <Button align="stretch" variant="primary" onPress={none}>{t('accounts.signIn.handoff')}</Button>
  <Links><QuietLink>{t('accounts.signIn.cannotUsePortico')}</QuietLink></Links>
</SignInFrame></Window>;

/** Who's watching and a profile's PIN (auth-15, final3-auth-05): the app's own phone pages over the kit picker. */
const pickerProfiles: readonly ProfileOption[] = [{id: 'demo', name: 'Demo', initial: 'D', color: 'blue', primary: true}, {id: 'alex', name: 'Alex', initial: 'A', color: 'violet', pinRequired: true}, {id: 'kids', name: 'Kids', initial: 'K', color: 'mint'}, {id: 'sam', name: 'Sam', initial: 'S', color: 'coral'}];
function WhosWatching() {
  const [remember, setRemember] = useState(false);
  return <Window><PhoneWhosWatching testID="frames-whos-watching" picker={<ProfilePicker profiles={pickerProfiles} onSelect={none} onManage={none} onAdd={none} title={t('accounts.profiles.onServer', {server: f('serverName')})}/>}>
    <RememberProfileRow checked={remember} onChange={setRemember} focusId="frames-whos-watching-remember" marginTop={24}/>
  </PhoneWhosWatching></Window>;
}
function ProfilePIN() {
  const [pin, setPin] = useState(''), [remember, setRemember] = useState(false), alex = pickerProfiles[1]!;
  return <Window><PhoneProfilePIN testID="frames-profile-pin" profile={{name: alex.name, face: {initial: alex.initial, color: alex.color}}} value={pin} onChange={setPin} onSubmit={none} busy={false} rejected={false}
    remember={<RememberProfileRow checked={remember} onChange={setRemember} focusId="frames-profile-pin-remember"/>} onForgot={none} onBack={none}/></Window>;
}

/** The account menu open under the avatar over a page (desktop-search menu; tv-focus 9), and Notifications: on a
 * phone the page in the Settings sheet (phone-app 21: tabs, For you, Server · Owner only, raised cards), wider the
 * panel under the bell (desktop-search notif). */
const accountActions = ([['notifications', 'i-bell', 'next.shell.notifications'], ['server', 'i-server', 'next.shell.switchServer'], ['settings', 'i-settings', 'next.shell.settings'], ['together', 'i-people', 'next.shell.watchTogether'],
  ['downloads', 'i-dl', 'next.shell.downloads'], ['feedback', 'i-flag', 'next.shell.feedback'], ['help', 'i-help', 'next.shell.help'], ['signout', 'i-out', 'next.shell.signOut']] as const).map(([id, icon, key]) => ({id, icon, label: t(key), onPress: none}));
const me: ProfileOption = {id: 'demo', name: sample.current.name, initial: sample.current.initial, color: 'blue', primary: true};
const others: readonly ProfileOption[] = [me, {id: 'alex', name: 'Alex', initial: 'A', color: 'violet', pinRequired: true}, {id: 'kids', name: 'Kids', initial: 'K', color: 'mint'}];
function TopRight({children}: {children: ReactNode}) {
  const insets = useSafeAreaInsets(), frame = useFrameMetrics();
  return <View style={{position: 'absolute', top: insets.top + 8, right: frame.gutter, flexDirection: 'row', gap: 12, alignItems: 'center'}}>{children}</View>;
}
function Account() {
  const avatar = useRef<View>(null), [open, setOpen] = useState(false), theme = useTheme(), settled = useSettled();
  useEffect(() => setOpen(settled), [settled]);
  return <OverlaysProvider><Page destination={{kind: 'home'}}><View style={{paddingTop: 60}}><PosterRow title={f('recent')} fresh/><PosterRow title={fx('newOnServer')}/></View></Page>
    <TopRight><ProfileAvatar ref={avatar} profile={me} unread size={theme.isTV ? 64 : 34} onPress={() => setOpen(true)}/></TopRight>
    <AccountMenu profile={me} accountLine={f('serverName')} open={open} onOpenChange={setOpen} anchorRef={avatar} viewerRole="owner" profiles={{profiles: others, selectedId: 'demo', onSelect: none, onManage: none}}
      actions={theme.isTV ? accountActions.filter(action => ['server', 'notifications', 'together'].includes(action.id)) : accountActions}
      serverAction={{id: 'server-settings', label: t('next.shell.serverSettings'), icon: 'i-server', onPress: none}}/></OverlaysProvider>;
}
function NotificationList() {
  const theme = useTheme(), [tab, setTab] = useState('unread'), {form} = useFrameMetrics();
  const item = (note: typeof sample.notifications[number] | typeof sample.phoneNotifications[number], index: number) => <NotificationItem key={note.id} separator={index > 0} notification={{id: note.id, read: index > 0, archived: false, severity: 'warning' in note ? 'warning' : 'info'} as never}
    title={note.title} body={note.body} when={note.when} menuLabel={t('next.shell.native.moreOptions')} onMenu={none} actions={[{id: 'open', label: note.action, onPress: none}]}
    subject={'asset' in note ? <View style={{width: 44, height: 66, borderRadius: 6, overflow: 'hidden'}}><Artwork source={asset(note.asset)}/></View>
      : <View style={{width: 44, height: 44, borderRadius: 10, backgroundColor: theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'}}><Icon name={'icon' in note ? note.icon as IconName : 'i-tv'} size={theme.scale.icon.secondary.size} color={'warning' in note ? theme.colors.amber : theme.colors.textSecondary}/></View>}/>;
  const phone = form === 'phone', mine = phone ? sample.phoneNotifications.slice(0, 1) : sample.notifications.slice(0, 2), server = phone ? sample.phoneNotifications.slice(1) : sample.notifications.slice(2);
  return <View style={{gap: theme.space.sm}}>
    <Tabs value={tab} onValueChange={setTab} items={[{value: 'unread', label: `${f('unread')} 3`}, {value: 'all', label: f('all')}, {value: 'archived', label: f('archived')}]}/>
    <SectionCard label={f('forYou')}>{mine.map(item)}</SectionCard>
    <SectionCard label={`${f('server')} · ${f('ownerOnly')}`}>{server.map(item)}</SectionCard>
  </View>;
}
function Notifications() {
  const {form} = useFrameMetrics(), bell = useRef<View>(null), theme = useTheme(), settled = useSettled();
  // A phone pushes Notifications inside the Settings sheet (phone-app 21; R9 `notifications`, phone).
  if (form === 'phone') return <Window><SettingsListFrame open onOpenChange={none} bar={{title: t('next.shell.settings'), action: {label: t('next.overlays.done'), onPress: none}}} list={<Index selected="" onOpen={none}/>}
    page={<SettingsPageFrame title={f('notifications')} back={{label: t('next.shell.settings'), onPress: none}} action={{label: f('markAllRead'), onPress: none}}><NotificationList/></SettingsPageFrame>}/></Window>;
  return <OverlaysProvider><Page destination={{kind: 'home'}}><View style={{paddingTop: 60}}><PosterRow title={f('recent')} fresh/><PosterRow title={fx('newOnServer')}/></View></Page>
    <TopRight><View ref={bell} collapsable={false} style={{width: theme.targets.touch, height: theme.targets.touch, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-bell" size={theme.scale.icon.secondary.size} color={theme.colors.text}/></View><ProfileAvatar profile={me} unread size={34}/></TopRight>
    <Popover open={settled} onOpenChange={none} anchorRef={bell} title={f('notifications')} placement="bottom-end" width={452} modal backdropOpacity={.5}><NotificationList/></Popover></OverlaysProvider>;
}
const WentWrong = () => <Page destination={{kind: 'home'}}><PageStateFrame icon="i-warn" tone="error" title={f('wentWrong')} body={f('wentWrongBody')} primaryAction={{id: 'retry', label: fx('tryAgain'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'home', label: f('goHome'), onPress: none}}/></Page>;
const PlaybackFailed = () => <Window><PageStateFrame icon="i-warn" tone="error" title={f('playbackFailed')} body={f('playbackFailedBody')} primaryAction={{id: 'version', label: f('otherVersion'), onPress: none}} secondaryAction={{id: 'retry', label: fx('tryAgain'), onPress: none}}/></Window>;

const whole = {screen: true} as const;
const touch = ['phone', 'tablet'] as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-settings-index', title: 'next.platform.frame.settingsList', render: SettingsIndex, ...whole},
  {id: 'frames-settings-search', title: 'next.platform.frame.settingsList', render: () => <SettingsIndex search/>, ...whole},
  {id: 'frames-settings-owner', title: 'next.platform.frame.settingsPage', render: SettingsOwner, ...whole},
  {id: 'frames-settings-alone', title: 'next.platform.frame.settingsPage', render: SettingsAlone, ...whole},
  {id: 'frames-overlay-task', title: 'next.platform.frame.sheet', render: OverlayTask, platforms: touch, ...whole},
  {id: 'frames-overlay-confirm', title: 'next.platform.frame.sheet', render: OverlayConfirm, platforms: touch, ...whole},
  {id: 'frames-overlay-details', title: 'next.platform.frame.sheet', render: OverlayDetails, platforms: touch, ...whole},
  {id: 'frames-overlay-actions', title: 'next.platform.frame.sheet', render: OverlayActions, platforms: touch, ...whole},
  {id: 'frames-overlay-choice', title: 'next.platform.frame.sheet', render: OverlayChoice, platforms: touch, ...whole},
  {id: 'frames-sign-in-server', title: 'next.platform.frame.signIn', render: () => <SignInServer/>, platforms: touch, ...whole},
  {id: 'frames-sign-in-locked', title: 'next.platform.frame.signIn', render: () => <SignInServer locked/>, platforms: touch, ...whole},
  {id: 'frames-sign-in-code', title: 'next.platform.frame.signIn', render: SignInCode, platforms: touch, ...whole},
  {id: 'frames-sign-in-portico', title: 'next.platform.frame.signIn', render: SignInPortico, platforms: touch, ...whole},
  {id: 'frames-whos-watching', title: 'next.platform.frame.signIn', render: WhosWatching, platforms: touch, ...whole},
  {id: 'frames-profile-pin', title: 'next.platform.frame.signIn', render: ProfilePIN, platforms: touch, ...whole},
  {id: 'frames-account-menu', title: 'next.platform.frame.sheet', render: Account, ...whole},
  {id: 'frames-notifications', title: 'next.platform.frame.sheet', render: Notifications, platforms: touch, ...whole},
  {id: 'frames-state-cant-reach', title: 'next.platform.frame.state', render: CantReach, ...whole},
  {id: 'frames-state-not-found', title: 'next.platform.frame.state', render: NotFound, ...whole},
  {id: 'frames-state-went-wrong', title: 'next.platform.frame.state', render: WentWrong, ...whole},
  {id: 'frames-state-playback-failed', title: 'next.platform.frame.state', render: PlaybackFailed, ...whole},
];
export default galleries;
