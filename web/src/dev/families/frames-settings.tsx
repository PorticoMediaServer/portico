import {useRef, useState, type ReactNode} from 'react';
import {createI18n, formatMessage, type MessageId} from '@i18n';
import {framesFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures.ts';
import type {InboxSnapshot} from '@core/inbox';
import {settingsFramesFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/settings-frames-fixtures.ts';
import {shellFixtures, shellReference as sample} from '../../../../packages/i18n/src/catalog/en-US/next/dev/shell-fixtures';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {Button, Select, Switch, Tabs, TextField} from '../../kit/controls';
import {Segmented} from '../../kit/controls/Choices';
import {PageStateFrame, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, StateRow} from '../../kit/frames';
import {MenuGroup, MenuItem, MenuSeparator, Popover} from '../../kit/overlays';
import {useFrameForm} from '../../kit/overlays';
import {LocalServerSignIn, PorticoServerSignIn, ServerProfilePIN, ServerProfiles} from '../../kit/accounts';
import {ServerChallenge, TwoStepCode, twoStepReady} from '../../kit/accounts/ServerChallenge';
import {AccountMenu, ChoiceMatrix, DefaultHint, DeviceRow, NotificationItem, PersonRow, ProfileAvatar, ServerHeader, SettingsNav, SettingsProfileCard, SettingsRow, ShortcutTiles, SubtitlePreview, type ShellAction} from '../../kit/shell';
import {NotificationsPanel} from '../../kit/shell/NotificationsPanel';
import {SettingsAdvanced} from '../../kit/settings/SettingsGroups';
import {DetailsPanel, DownloadsStillPlay} from '../../kit/states';
import {PosterCard} from '../../kit/cards';
import {Page, PosterRow, Window, useReferenceArtwork} from './frames';

/** Family E (round 8): Settings, sign-in, account, notifications, page states and every overlay task drawn over a real
 * page, each by the R9 table in packages/design `overlay-forms.ts`. The same ids as the native gallery's. Serve-only. */
type FixtureName = keyof typeof settingsFramesFixtureMessages extends `next.settingsFrames.fixture.${infer Name}` ? Name : never;
const f = (name: FixtureName) => formatMessage(settingsFramesFixtureMessages[`next.settingsFrames.fixture.${name}`]);
const none = () => {};
const serverName = shellFixtures['next.dev.shell.serverName'];
const serverFacts = [
  {id: 'uptime', text: shellFixtures['next.dev.shell.uptime'], role: 'supplement' as const},
  {id: 'storage', text: shellFixtures['next.dev.shell.storage'], role: 'supplement' as const},
  {id: 'database', text: shellFixtures['next.dev.shell.database'], role: 'supplement' as const},
];
const seconds = sample.seconds;
const t = createI18n().t;
type FrameFixture = keyof typeof framesFixtureMessages extends `next.frames.fixture.${infer Name}` ? Name : never;
const fx = (name: FrameFixture) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
const shortcutItems: ShellAction[] = [
  {id: 'profile', icon: 'i-switch', label: t('next.shell.switchProfile'), onAction: none},
  {id: 'server', icon: 'i-server', label: t('next.shell.switchServer'), onAction: none},
  {id: 'notifications', icon: 'i-bell', label: t('next.shell.notifications'), onAction: none},
  {id: 'together', icon: 'i-people', label: t('next.shell.watchTogether'), onAction: none},
];

/** The Settings index (desktop-settings f1, phone-app 16, phone-web 9): the shell's own `SettingsNav` as the frame's
 * list, with the profile card and the quick tiles at phone width. */
function Index({selected, query = '', onQuery, onOpen}: {selected: string; query?: string; onQuery?: (value: string) => void; onOpen: (id: string) => void}) {
  const phone = useFrameForm() === 'phone';
  return <SettingsNav layout={phone ? 'phone' : 'desktop'} accountName={sample.current.name} serverName={serverName} viewerRole="owner" account={sample.accountNav} server={sample.serverNav}
    selectedId={selected} onNavigate={onOpen} query={query} onQueryChange={onQuery ?? none} matches={query ? sample.settingsSearch : undefined} selectedSearchResult={query ? {pageId: 'languages', rowId: 'show-subtitles'} : undefined} onSearchResult={none} />;
}
/** desktop-settings f1 and phone-app 18: Saved, section help, a switch with dependent rows under it, inline selects, the
 * three-column choice matrix with Changed and Reset, a collapsed Advanced row and the subtitle preview. */
function PlaybackSections() {
  const art = useReferenceArtwork(), [autoplay, setAutoplay] = useState(true), [credits, setCredits] = useState(false), [wait, setWait] = useState('10');
  const [watching, setWatching] = useState('3'), [back, setBack] = useState('10'), [forward, setForward] = useState('30'), [advanced, setAdvanced] = useState(false);
  const [skips, setSkips] = useState<Record<string, string>>(Object.fromEntries(sample.skipRows.map(row => [row.id, row.value])));
  const after = [{value: '3', label: f('afterThree')}, {value: 'never', label: f('never')}], back10 = seconds.filter(option => option.value !== '0'), forward30 = [...back10, {value: '30', label: f('thirtySeconds')}];
  return <>
    <SectionCard label={fx('nextEpisode')}>
      <SettingsRow label={sample.rows.autoplay} help={sample.rows.autoplayHelp}><Switch checked={autoplay} onCheckedChange={setAutoplay} aria-label={sample.rows.autoplay} /></SettingsRow>
      <SettingsRow dependent label={sample.rows.wait} help={sample.rows.waitHelp}><Select aria-label={sample.rows.wait} options={seconds} value={wait} onValueChange={setWait} /></SettingsRow>
      <SettingsRow dependent label={sample.rows.credits} help={sample.rows.creditsHelp}><Switch checked={credits} onCheckedChange={setCredits} aria-label={sample.rows.credits} /></SettingsRow>
      <SettingsRow label={f('stillWatching')} help={f('stillWatchingHelp')}><Select aria-label={f('stillWatching')} options={after} value={watching} onValueChange={setWatching} /></SettingsRow>
    </SectionCard>
    <SectionCard label={sample.skipping} help={f('skippingHelp')}>
      <ChoiceMatrix label={sample.skipping} choices={sample.skipOptions} rows={sample.skipRows.map(row => ({...row, value: skips[row.id]!, hint: row.id === 'recaps' && skips[row.id] !== 'ask'
        ? <DefaultHint variant="changed" value={sample.skipOptions[0].label} effectiveSource="profile" onReset={() => setSkips({...skips, [row.id]: 'ask'})} /> : undefined}))} onChange={(id, value) => setSkips({...skips, [id]: value})} />
      <SettingsRow label={f('skipBack')} help={f('skipBackHelp')}><Select aria-label={f('skipBack')} options={back10} value={back} onValueChange={setBack} /></SettingsRow>
      <SettingsRow label={f('skipForward')} help={f('skipForwardHelp')}><Select aria-label={f('skipForward')} options={forward30} value={forward} onValueChange={setForward} /></SettingsRow>
    </SectionCard>
    <SectionCard label={f('appearance')} help={f('appearanceHelp')}>
      <SubtitlePreview source={art(sample.subtitleAsset)} size="medium" background="translucent" />
      <SettingsRow label={f('textSize')}><Select aria-label={f('textSize')} options={[{value: 'medium', label: f('medium')}]} value="medium" /></SettingsRow>
      <SettingsAdvanced open={advanced} onOpenChange={setAdvanced} summary={[f('advancedSummary')]}><SettingsRow label={f('skipBack')}><Select aria-label={f('skipBack')} options={back10} value={back} onValueChange={setBack} /></SettingsRow></SettingsAdvanced>
    </SectionCard>
  </>;
}
const accountTitle = () => fx('playback');
function SettingsList({search = false}: {search?: boolean}) {
  const phone = useFrameForm() === 'phone', [page, setPage] = useState<string | undefined>(), [query, setQuery] = useState(search ? sample.settingsQuery : '');
  const playback = <SettingsPageFrame title={accountTitle()} status="saved" back={{label: t('next.shell.settings'), onPress: () => setPage(undefined)}} intro={fx('playbackIntro')}><PlaybackSections /></SettingsPageFrame>;
  return <Page current="settings"><SettingsListFrame bar={{title: t('next.shell.settings'), action: {label: t('next.overlays.done'), onPress: none}}}
    profile={<SettingsProfileCard profile={sample.current} description={sample.accountDescription} onPress={none} />} shortcuts={<ShortcutTiles compact items={shortcutItems} />}
    list={<Index selected={page ?? (phone ? '' : 'playback')} query={query} onQuery={setQuery} onOpen={setPage} />} page={page || !phone ? playback : undefined} /></Page>;
}
/** An owner page (owner-people, desktop-settings f4-f10): the server strip, header buttons, page tabs with counts and a
 * list of accounts and a device waiting for approval. */
function PeopleSections() {
  const [device, setDevice] = useState<'pending' | 'allowed' | 'blocked'>('pending');
  return <>
    <SectionCard label={f('waiting')}><DeviceRow name={sample.device} icon="i-tv" profile={sample.accountPerson} facts={sample.deviceFacts} state={device} status={device === 'pending' ? undefined : device === 'allowed' ? sample.approved : sample.blocked}
      actions={device === 'pending' ? [{id: 'block', label: sample.block, onAction: () => setDevice('blocked')}, {id: 'approve', label: sample.approve, icon: 'i-check', variant: 'primary', onAction: () => setDevice('allowed')}] : []} /></SectionCard>
    <SectionCard label={f('accounts')}>{sample.people.slice(0, 4).map(person => <PersonRow key={person.person.id} person={person.person} role={person.role as 'owner' | 'administrator' | 'member'} self={'self' in person && person.self} facts={person.facts} activity={person.activity}
      actions={[{id: 'more', label: sample.moreSam, icon: 'i-more', iconOnly: true, onAction: none}]} />)}</SectionCard>
  </>;
}
function OwnerPage({back}: {back?: () => void}) {
  const [tab, setTab] = useState('accounts');
  return <SettingsPageFrame title={f('people')} back={back ? {label: t('next.shell.settings'), onPress: back} : undefined} intro={f('peopleIntro')}
    actions={<><Button size="small" onClick={none}>{f('addAccount')}</Button><Button size="small" variant="primary" icon="i-plus" onClick={none}>{f('invite')}</Button></>}
    server={<ServerHeader serverName={serverName} viewerRole="owner" audience="owner" health="normal" facts={serverFacts} />}
    tabs={<Tabs aria-label={f('people')} value={tab} onValueChange={setTab} items={[{value: 'accounts', label: `${f('accounts')} 5`}, {value: 'invitations', label: `${f('invitations')} 2`}, {value: 'devices', label: `${f('devices')} 3`}]} />}>
    <PeopleSections /></SettingsPageFrame>;
}
function SettingsOwner() {
  const phone = useFrameForm() === 'phone', [page, setPage] = useState<string | undefined>(phone ? 'people' : undefined);
  return <Page current="settings"><SettingsListFrame bar={{title: t('next.shell.settings'), action: {label: t('next.overlays.done'), onPress: none}}}
    list={<Index selected="people" onOpen={setPage} />} page={page || !phone ? <OwnerPage back={() => setPage(undefined)} /> : undefined} /></Page>;
}
/** A settings page shown alone (tablets SC-8): Back, the gutter and the page's column limit. */
const SettingsAlone = () => <Page current="settings"><OwnerPage back={none} /></Page>;

/** The owner's Integrations page (owner-server 16, 23, phone 38-40) with its two switches (definition 13 rules 33 and 37;
 * client-core `IntegrationSwitchesSource`): the Jellyfin-compatible routes and Assistants, each off until the owner turns
 * it on. No mock-up draws the switches: they lead the Assistants tab as one "Connections" section of switch rows. A save
 * the server refused leaves the switch as it was and says so as rule 9's error row in that section (`failed`). */
function IntegrationsSections({failed = false}: {failed?: boolean}) {
  const [routes, setRoutes] = useState(true), [assistants, setAssistants] = useState(!failed);
  return <>
    <SectionCard label={f('connections')}>
      <SettingsRow label={f('compatibleRoutes')} help={f('compatibleRoutesHelp')}><Switch checked={routes} onCheckedChange={setRoutes} aria-label={f('compatibleRoutes')} /></SettingsRow>
      <SettingsRow label={f('assistantsSwitch')} help={f('assistantsHelp')}><Switch checked={assistants} onCheckedChange={setAssistants} aria-label={f('assistantsSwitch')} /></SettingsRow>
      {failed && <StateRow kind="error" icon="i-warn" title={f('notSaved')} reason={f('notSavedReason')} action={{label: fx('tryAgain'), onPress: none}} />}
    </SectionCard>
    <SectionCard label={t('api.mcp.title')} help={t('api.mcp.lede')}>
      <SettingsRow stacked label={t('api.mcp.address')} help={t('api.mcp.addressHelp')}><div style={{display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 'var(--space-sm)'}}><code style={{overflowWrap: 'anywhere'}}>{f('assistantAddressValue')}</code><Button size="xs" icon="i-copy" onClick={none}>{t('web.twoStep.copy')}</Button></div></SettingsRow>
      <SettingsRow label={t('api.mcp.key')} help={t('api.mcp.keyHelp')}><Button size="xs" onClick={none}>{t('next.settingsSpec.server.integrations.groups.2.rows.1.control.label')}</Button></SettingsRow>
    </SectionCard>
  </>;
}
function IntegrationsPage({failed = false, back}: {failed?: boolean; back?: () => void}) {
  const [tab, setTab] = useState('assistants');
  return <SettingsPageFrame title={f('integrations')} back={back ? {label: t('next.shell.settings'), onPress: back} : undefined} intro={f('integrationsIntro')}
    server={<ServerHeader serverName={serverName} viewerRole="owner" audience="owner" health="normal" facts={serverFacts} />}
    tabs={<Tabs aria-label={f('integrations')} value={tab} onValueChange={setTab} items={[{value: 'keys', label: f('apiKeys')}, {value: 'tokens', label: f('memberTokens')}, {value: 'webhooks', label: `${f('webhooks')} 1`}, {value: 'assistants', label: t('api.mcp.tab')}]} />}>
    <IntegrationsSections failed={failed} /></SettingsPageFrame>;
}
function SettingsIntegrations({failed = false}: {failed?: boolean}) {
  const phone = useFrameForm() === 'phone', [page, setPage] = useState<string | undefined>('integrations');
  return <Page current="settings"><SettingsListFrame bar={{title: t('next.shell.settings'), action: {label: t('next.overlays.done'), onPress: none}}}
    list={<Index selected="integrations" onOpen={setPage} />} page={page || !phone ? <IntegrationsPage failed={failed} back={() => setPage(undefined)} /> : undefined} /></Page>;
}

/** The two-step code box (security STATUS "Needs the visual rebuild"; account-10 frames 3 and 4, account-31 frame 3): a
 * signed-in person whose sign-in has not shown a code is asked for one before a guarded action (approving a TV, creating
 * a key). A short task by R9 over the page it guards: the six boxes, "Lost your phone? Use a recovery code" (which swaps
 * the boxes for the recovery field), Continue once six digits are in. `refused`: the code was wrong. */
function TwoStepBox({refused = false}: {refused?: boolean}) {
  const [code, setCode] = useState(refused ? '482913' : '482'), [recovery, setRecovery] = useState(false);
  return <Page current="settings"><SettingsListFrame bar={{title: t('next.shell.settings')}} list={<Index selected="integrations" onOpen={none} />} page={<IntegrationsPage />} />
    <SheetFrame open onOpenChange={none} task="task" title={t(recovery ? 'services.signin.recovery' : 'web.direct.codeTitle')} action={{label: t('action.continue'), disabled: !twoStepReady(code, recovery), onPress: none}}>
      <p>{t(recovery ? 'services.signin.recoveryBody' : 'services.signin.twoStep')}</p>
      <TwoStepCode value={code} onChange={setCode} recovery={recovery} onRecoveryChange={setRecovery} error={refused ? f('codeWrong') : undefined} />
    </SheetFrame></Page>;
}

/** Every overlay task over a real page: the Settings page, or a library row whose card opened the menu. */
function OverPage({children, settings = false}: {children: ReactNode; settings?: boolean}) {
  return <Page current={settings ? 'settings' : 'movies'}>{settings ? <SettingsListFrame bar={{title: t('next.shell.settings')}} list={<Index selected="playback" onOpen={none} />}
    page={<SettingsPageFrame title={accountTitle()} intro={fx('playbackIntro')}><PlaybackSections /></SettingsPageFrame>} />
    : <div style={{paddingTop: 'var(--frame-title-top)'}}><PosterRow title={f('recent')} fresh /><PosterRow title={fx('newOnServer')} /></div>}{children}</Page>;
}
function TaskSheet() {
  const [kind, setKind] = useState('m3u'), [address, setAddress] = useState(f('sourceUrlValue')), [name, setName] = useState(f('sourceNameValue'));
  return <OverPage settings><SheetFrame open onOpenChange={none} task="task" title={fx('addSource')} action={{label: fx('addSource'), onPress: none}}>
    <div style={{display: 'grid', gap: 'var(--space-sm)'}}>
      <Segmented aria-label={f('sourceType')} value={kind} onValueChange={setKind} items={[{value: 'm3u', label: f('m3u')}, {value: 'tuner', label: f('tuner')}]} />
      <TextField label={f('sourceUrl')} value={address} onChange={event => setAddress(event.currentTarget.value)} />
      <TextField label={f('sourceName')} value={name} onChange={event => setName(event.currentTarget.value)} />
      <Select label={f('whoSees')} options={[{value: 'all', label: f('everyone')}]} value="all" />
    </div>
  </SheetFrame></OverPage>;
}
function ConfirmSheet() {
  return <OverPage settings><SheetFrame open onOpenChange={none} task="confirm" title={f('recordingTitle')}
    foot={<><Button onClick={none}>{t('next.overlays.cancel')}</Button><Button variant="destructive" onClick={none}>{f('delete')}</Button></>}><p>{f('recordingBody')}</p></SheetFrame></OverPage>;
}
function Facts({rows}: {rows: readonly [FixtureName, FixtureName][]}) {
  return <DetailsPanel audience="viewer" facts={rows.map(([label, value]) => ({id: label, label: f(label), value: f(value)}))} />;
}
function DetailsSheet() {
  return <OverPage><SheetFrame open onOpenChange={none} task="details" title={f('fileInfo')} subtitle={f('fileTitle')}>
    <SectionCard label={f('version1')}><Facts rows={[['videoFact', 'videoValue'], ['audioFact', 'audioValue'], ['sizeFact', 'sizeValue']]} /></SectionCard>
    <SectionCard label={f('version2')}><Facts rows={[['videoFact', 'video1080'], ['audioFact', 'audio1080'], ['sizeFact', 'size1080']]} /></SectionCard>
  </SheetFrame></OverPage>;
}
/** A card's More menu: a sheet at phone width, a popover beside the card wider (tablet f9, desktop-detail f2). */
function ActionsMenu() {
  const art = useReferenceArtwork(), anchor = useRef<HTMLDivElement>(null);
  return <Page current="movies"><div style={{padding: 'var(--frame-title-top) var(--page-gutter) 0', display: 'flex', gap: 'var(--space-sm)'}}>
    {['alien', 'blade-runner', 'dune'].map((id, index) => <div key={id} ref={index === 0 ? anchor : undefined} style={{width: 'var(--card-poster-width)'}}><PosterCard model={{title: index === 0 ? f('cardTitle') : id, caption: []}} artwork={art(`pool/movies/${id}.jpg`)} onNavigate={none} /></div>)}
  </div>
    <SheetFrame open onOpenChange={none} task="actions" title={f('cardTitle')} subtitle={f('cardFacts')} anchorRef={anchor}>
      <MenuGroup><MenuItem icon="i-play">{fx('play')}</MenuItem><MenuItem icon="i-plus">{f('addMyList')}</MenuItem><MenuItem icon="i-check">{f('markWatched')}</MenuItem><MenuItem icon="i-stack">{f('addCollection')}</MenuItem></MenuGroup>
      <MenuSeparator /><MenuItem icon="i-info">{f('fileInfo')}</MenuItem>
      <MenuGroup label={f('ownerOnly')} ownerOnly><MenuItem icon="i-edit">{f('editDetails')}</MenuItem><MenuItem icon="i-trash" destructive>{f('deleteMedia')}</MenuItem></MenuGroup>
    </SheetFrame></Page>;
}
/** A short choice drops from its trigger and leaves it in view (phone-app 4b, 18): the sort order of a library. */
function ChoicePopover() {
  const anchor = useRef<HTMLDivElement>(null), [order, setOrder] = useState('added');
  const options = [['title', 'sortTitle'], ['added', 'sortAdded'], ['released', 'sortReleased'], ['rating', 'sortRating']] as const;
  return <Page current="movies"><div style={{padding: 'var(--frame-title-top) var(--page-gutter) var(--space-sm)'}}><div ref={anchor} style={{display: 'inline-block'}}><Button size="small" icon="i-sort">{f(options.find(([value]) => value === order)![1])}</Button></div></div><PosterRow title={f('recent')} fresh />
    <SheetFrame open onOpenChange={none} task="choice" title={f('sortBy')} anchorRef={anchor}>
      {options.map(([value, label]) => <MenuItem key={value} selected={order === value} onSelect={() => setOrder(value)}>{f(label)}</MenuItem>)}
    </SheetFrame></Page>;
}

/** Sign-in and its steps: the kit's own pages on the frame's stage (auth.html, auth-4, auth-4b, auth-6). */
const remembered = [{accountId: 'nana', username: 'nana_r', displayName: 'Nana'}, {accountId: 'theo', username: 'theo', displayName: 'Theo'}];
const SignInServer = () => <LocalServerSignIn serverName={f('serverName')} initialUsername="nana_r" remembered={remembered} onSignIn={none} onForgotPassword={none} onJoin={none} />;
const SignInLocked = () => <LocalServerSignIn serverName={f('serverName')} initialUsername="nana_r" remembered={remembered} retryAt={Date.now() + 32_000} onSignIn={none} onForgotPassword={none} onJoin={none} />;
const SignInCode = () => <ServerChallenge onCode={none} onBack={none} />;
const SignInPortico = () => <PorticoServerSignIn serverName={f('serverName')} onContinue={none} onRecovery={none} />;
const pickerProfiles = [
  {id: 'demo', name: 'Demo', eligible: true, primary: true, art: 'blue'},
  {id: 'alex', name: 'Alex', eligible: true, pinRequired: true, art: 'violet'},
  {id: 'kids', name: 'Kids', eligible: true, art: 'mint'},
  {id: 'sam', name: 'Sam', eligible: true, art: 'coral'},
] as const;
const WhosWatching = () => <ServerProfiles serverName={f('serverName')} profiles={pickerProfiles} onSelect={none} onSwitchServer={none} remember={false} onRememberChange={none} />;
const ProfilePIN = () => <ServerProfilePIN serverName={f('serverName')} profile={pickerProfiles[1]} onComplete={none} onOtherProfiles={none} onForgotPIN={none} onSwitchServer={none} remember={false} onRememberChange={none} autoFocus={false} />;

/** The account menu open under the avatar over a page (desktop-search menu; a sheet at phone width, phone-app r7). */
function Account() {
  const [open, setOpen] = useState(true);
  return <OverPage><div style={{position: 'absolute', top: 18, right: 'var(--page-gutter)', zIndex: 1}}><AccountMenu open={open} onOpenChange={setOpen} trigger={<ProfileAvatar initial={sample.current.initial} name={sample.current.name} notificationDot />} current={sample.current} primary accountFacts={sample.accountFacts} profiles={sample.profiles} notificationCount={3}
    actions={{manageProfiles: none, addProfile: none, switchProfile: none, switchServer: none, settings: none, notifications: none, watchTogether: none, downloads: none, feedback: none, help: none, signOut: none, forgetRememberedProfile: none}} /></div></OverPage>;
}
/** The Notifications panel under the bell (desktop-search notif) and, at phone width, the page in the Settings sheet (phone-app 21). */
const inbox = {phase: 'ready', audience: 'profile', audiences: ['profile', 'account-admin'], view: 'unread', items: [], counts: {unread: 2, read: 4, archived: 1, total: 7}, unread: 3, unreadBy: {'account-admin': 1}, revision: 1, more: false, busy: false} as unknown as InboxSnapshot;
function NotificationList() {
  const art = useReferenceArtwork(), phone = useFrameForm() === 'phone';
  return <NotificationsPanel snapshot={inbox} pages={phone} header={!phone} onAudience={none} onView={none} onReadAll={none} onMore={none}>
    {sample.notifications.slice(0, 2).map((note, index) => <NotificationItem key={note.id} layout={phone ? 'phone' : 'desktop'} title={note.title} body={note.body} unread={index === 0} when={[{id: 'when', text: note.when, role: 'context'}]}
      subject={'asset' in note ? {kind: 'artwork', source: art(note.asset)} : {kind: 'device', icon: 'i-tv'}} actions={[{id: 'open', label: note.action, onAction: none}]} onMarkRead={none} onArchive={none} onMoveToInbox={none} />)}
  </NotificationsPanel>;
}
function Notifications() {
  const phone = useFrameForm() === 'phone', bell = useRef<HTMLDivElement>(null);
  if (phone) return <Page current="settings"><SettingsPageFrame title={f('notifications')} back={{label: t('next.shell.settings'), onPress: none}} action={{label: f('markAllRead'), onPress: none}}><NotificationList /></SettingsPageFrame></Page>;
  return <OverPage><div ref={bell} style={{position: 'absolute', top: 18, right: 'calc(var(--page-gutter) + 132px)', width: 40, height: 40}} />
    <Popover open onOpenChange={none} anchorRef={bell} title={f('notifications')} placement="bottom-end" width={452}><NotificationList /></Popover></OverPage>;
}

/** Frame 9 in full (states 3, 4a, 4c): retry countdown, what still works, the buttons, Details open for the owner, help. */
function CantReach() {
  const art = useReferenceArtwork(), [open, setOpen] = useState(true);
  return <Page current="home"><PageStateFrame icon="i-srvoff" tone="warning" title={fx('cantReach')} body={fx('cantReachBody')}
    retry={{progress: .4, label: f('retryIn'), detail: fx('retryDetail')}}
    downloads={<DownloadsStillPlay covers={['pool/shows/slow-horses.jpg', 'pool/movies/dune.jpg', 'pool/movies/alien.jpg'].map(art)} title={f('downloadsPlay')} detail={f('downloadsTitles')} onOpen={none} />}
    primaryAction={{id: 'retry', label: f('tryAgainNow'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'switch', label: fx('switchServer'), icon: 'i-swap', onPress: none}}
    details={{open, onOpenChange: setOpen, content: <DetailsPanel audience="owner" facts={[{id: 'home', label: f('homeAddress'), status: {label: f('noAnswer'), tone: 'warning'}}, {id: 'secure', label: f('secureAddress'), status: {label: f('noAnswer'), tone: 'warning'}}, {id: 'device', label: f('thisComputer'), status: {label: f('online'), tone: 'healthy'}}]}
      owner={{ownerMark: null, ownerTag: null, paragraphs: [f('ownerHelp')], codes: ['SRV-OFFLINE'], actions: [{id: 'copy', label: f('copyDetails'), onPress: none}]}} />}}
    help={f('stateHelp')} /></Page>;
}
const NotFound = () => <Page current="movies"><PageStateFrame icon="i-film" title={f('notFound')} body={f('notFoundBody')} primaryAction={{id: 'home', label: f('goHome'), onPress: none}} /></Page>;
const WentWrong = () => <Page current="home"><PageStateFrame icon="i-warn" tone="error" title={f('wentWrong')} body={f('wentWrongBody')} primaryAction={{id: 'retry', label: fx('tryAgain'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'home', label: f('goHome'), onPress: none}} /></Page>;
const PlaybackFailed = () => <Window><PageStateFrame icon="i-warn" tone="error" title={f('playbackFailed')} body={f('playbackFailedBody')} primaryAction={{id: 'version', label: f('otherVersion'), onPress: none}} secondaryAction={{id: 'retry', label: fx('tryAgain'), onPress: none}} /></Window>;

const section = (id: string, label: string, render: () => ReactNode, height = 900): GalleryCase => ({id, labelKey: `next.platform.frame.${label}` as MessageId, width: 1440, height, render});
export default defineGalleryFamily({id: 'frames-settings', labelKey: 'next.platform.frame.family', cases: [
  section('settings-index', 'settingsList', () => <SettingsList />, 1400),
  section('settings-search', 'settingsList', () => <SettingsList search />),
  section('settings-owner', 'settingsPage', SettingsOwner),
  section('settings-alone', 'settingsPage', SettingsAlone),
  section('settings-integrations', 'settingsPage', () => <SettingsIntegrations />, 1180), section('settings-integrations-failed', 'settingsPage', () => <SettingsIntegrations failed />),
  section('overlay-task', 'sheet', TaskSheet), section('overlay-confirm', 'sheet', ConfirmSheet), section('overlay-details', 'sheet', DetailsSheet),
  section('overlay-actions', 'sheet', ActionsMenu), section('overlay-choice', 'sheet', ChoicePopover),
  section('overlay-two-step', 'sheet', () => <TwoStepBox />), section('overlay-two-step-refused', 'sheet', () => <TwoStepBox refused />),
  section('sign-in-server', 'signIn', SignInServer), section('sign-in-locked', 'signIn', SignInLocked), section('sign-in-code', 'signIn', SignInCode), section('sign-in-portico', 'signIn', SignInPortico),
  section('whos-watching', 'signIn', WhosWatching), section('profile-pin', 'signIn', ProfilePIN),
  section('account-menu', 'sheet', Account), section('notifications', 'sheet', Notifications),
  section('state-cant-reach', 'state', CantReach), section('state-not-found', 'state', NotFound), section('state-went-wrong', 'state', WentWrong), section('state-playback-failed', 'state', PlaybackFailed),
]});
