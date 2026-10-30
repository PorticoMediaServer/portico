import {useEffect, useMemo, useState, type ReactNode} from 'react';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {createI18n} from '@i18n';
import {playbackIdentity, titleFactParts} from '@core/presentation/kit-model';
import {shellFixtures, shellReference as f, shellReferenceSamples} from '../../../../packages/i18n/src/catalog/en-US/next/dev/shell-fixtures';
import {OwnerTag, ServerHeader, ServerMark, EditorHeader, ProfileAvatar, ProfileAvatarFace, AccountMenu, NotificationItem, WatchTogetherPill, SettingsNav, SettingsSheet, ShortcutTiles, LargeTitleHeader, PushedNavBar, SettingsRow, DefaultHint, ChoiceMatrix, ChoiceCards, LanguageList, SubtitlePreview, StatTiles, BarList, PersonRow, DeviceRow, WaitingNotice, SegmentWindows, TopBarActivity, ActivityPanel, type ShellAction, type ProfileAvatarSize, type ActivityPanelView, type ActivityStreamView, type LanguageValue, type SegmentMark, type SegmentEdit, type ServerRole} from '../../kit/shell';
import {Artwork} from '../../kit/artwork/Artwork';
import {Switch} from '../../kit/controls/Fields';
import {Select} from '../../kit/controls/Select';
import {createArtworkRuntime} from '../../kit/artwork/source';
import styles from '../../kit/shell/gallery.module.css';
import activityStyles from '../../kit/shell/activity.module.css';
import {activityR7Cases} from '../ActivityReference';

const t = createI18n().t;
const facts = [
  {id: 'uptime', text: shellFixtures['next.dev.shell.uptime'], role: 'supplement' as const},
  {id: 'storage', text: shellFixtures['next.dev.shell.storage'], role: 'supplement' as const},
  {id: 'database', text: shellFixtures['next.dev.shell.database'], role: 'supplement' as const},
];
function OwnerState() { return <div className={styles.stateStage}><div className={styles.stateIdentity}><OwnerTag variant="state" identity={t('next.shell.serverSettings')} /></div></div>; }
function OwnerDetails() { return <div className={styles.detailStage}><div className={styles.detailHeading}><ServerMark />{t('next.shell.forOwner')}<OwnerTag /></div></div>; }
function CompactOwner() { return <div className={styles.compactStage}><div className={styles.compactHeading}><ServerMark size={22} />{t('next.shell.forOwner')}<OwnerTag /></div></div>; }
function HeaderOwner() { return <div className={styles.headerStage}><div className={styles.headerPosition}><ServerHeader serverName={shellFixtures['next.dev.shell.serverName']} viewerRole="owner" audience="owner" health="normal" facts={facts} /></div></div>; }
function HeaderShared() { return <div className={styles.headerStage}><div className={styles.headerPosition}><ServerHeader serverName={shellFixtures['next.dev.shell.serverName']} viewerRole="owner" audience="administrators" health="normal" facts={facts} /></div></div>; }
function HeaderAdministrator() { return <div className={styles.headerStage}><div className={styles.headerPosition}><ServerHeader serverName={shellFixtures['next.dev.shell.serverName']} viewerRole="administrator" audience="administrators" health="attention" facts={facts} /></div></div>; }

function useAction() { const [last, setLast] = useState(''); return {act: (label: string) => () => setLast(label), output: <output className={styles.actionOutput} hidden={!last}>{last}</output>}; }
function Stage({children, output, raised = false, padded = false}: {children: ReactNode; output?: ReactNode; raised?: boolean; padded?: boolean}) { return <div className={styles.componentStage} data-raised={raised || undefined} data-padded={padded || undefined}>{children}{output}</div>; }
function useReferenceArt(asset: string) {
  const runtime = useMemo(() => createArtworkRuntime({scopeKey: 'dev-shell-reference', classify: () => ({retryable: false}), policy: {concurrency: 1}, load: ({url}, signal) => new Promise((resolve, reject) => {
    const image = new Image(); let settled = false;
    const clear = () => { image.onload = null; image.onerror = null; signal.removeEventListener('abort', abort); };
    const abort = () => { if (settled) return; settled = true; clear(); image.src = ''; reject(new DOMException('Aborted', 'AbortError')); };
    image.onload = () => { void image.decode().then(() => { if (settled) return; settled = true; clear(); resolve({src: url, width: image.naturalWidth, height: image.naturalHeight}); }, error => { if (!settled) { settled = true; clear(); reject(error); } }); };
    image.onerror = () => { if (!settled) { settled = true; clear(); reject(new Error('Reference artwork could not load.')); } };
    signal.addEventListener('abort', abort, {once: true}); if (signal.aborted) { abort(); return; } image.src = url;
  })}), []);
  useEffect(() => () => runtime.dispose(), [runtime]);
  return useMemo(() => runtime.source({url: f.artworkBase + asset, cacheKey: asset}), [runtime, asset]);
}
function AvatarCase({locked = false, current = false, restricted = false, size = 48}: {locked?: boolean; current?: boolean; restricted?: boolean; size?: ProfileAvatarSize}) {
  const a = useAction(), profile = locked ? f.profiles[0] : restricted ? {...f.people[2].person, art: 'coral' as const} : f.current;
  return <Stage output={a.output}><div className={styles.avatarStage}><ProfileAvatar initial={profile.initial} name={profile.name} art={profile.art} size={size} locked={locked} current={current} disabled={restricted} onClick={a.act(f.current.name)} /></div></Stage>;
}
function EditorThumb({asset}: {asset: string}) { const source = useReferenceArt(asset); return <Artwork source={source} alt="" width={40} height={60} />; }
function EditorCase({layout = 'desktop'}: {layout?: 'desktop' | 'phone'}) {
  const a = useAction();
  return <Stage raised output={a.output}><EditorHeader audience={{authority: 'owner'}} layout={layout} title={f.editor.title} description={layout === 'desktop' ? f.editor.description : f.editor.phoneDescription} onClose={a.act(t('next.shell.close'))}>{layout === 'desktop' && <div className={styles.editorThumbs}>{f.editor.assets.map(asset => <EditorThumb key={asset} asset={asset} />)}<span>{f.editor.targets}</span></div>}</EditorHeader></Stage>;
}
function AccountCase({phone = false}: {phone?: boolean}) {
  const [open, setOpen] = useState(true), a = useAction();
  return <div className={styles.overlayStage}><div className={styles.accountTrigger} data-phone={phone || undefined}><AccountMenu open={open} onOpenChange={setOpen} trigger={<ProfileAvatar initial={f.current.initial} name={f.current.name} />} current={f.current} primary accountFacts={f.accountFacts} profiles={f.profiles} notificationCount={phone ? f.notifications.length : undefined} actions={{manageProfiles: a.act(t('next.shell.manage')), addProfile: a.act(t('next.shell.add')), switchProfile: profile => a.act(profile.name)(), switchServer: a.act(t('next.shell.switchServer')), settings: a.act(t('next.shell.settings')), notifications: phone ? a.act(t('next.shell.notifications')) : undefined, watchTogether: a.act(t('next.shell.watchTogether')), downloads: a.act(t('next.shell.downloads')), feedback: a.act(t('next.shell.feedback')), help: a.act(t('next.shell.help')), signOut: a.act(t('next.shell.signOut'))}} /></div>{a.output}</div>;
}
function NotificationCase({index = 0, unread = true, archived = false, layout = 'desktop'}: {index?: number; unread?: boolean; archived?: boolean; layout?: 'desktop' | 'phone'}) {
  const source = useReferenceArt(index === 1 ? f.notifications[1].asset : f.notifications[0].asset);
  const note = layout === 'phone' ? f.phoneNotifications[index]! : f.notifications[index]!;
  const phone = f.phoneNotifications[index]!;
  const [read, setRead] = useState(!unread), [archive, setArchive] = useState(archived), a = useAction();
  return <Stage raised output={a.output}><NotificationItem layout={layout} title={note.title} body={note.body} when={[{id: 'when', text: note.when, role: 'context'}, ...('about' in note ? [{id: 'about', text: note.about, role: 'context' as const}] : [])]} unread={!read} archived={archive}
    subject={layout === 'phone' ? {kind: 'device', icon: phone.icon, warning: 'warning' in phone && phone.warning} : index < 2 ? {kind: 'artwork', source, position: index === 0 ? '50% 58%' : 'center 20%'} : {kind: 'device', icon: 'i-tv'}}
    actions={[{id: 'open', label: note.action, onAction: a.act(note.action)}, ...('secondAction' in note ? [{id: 'review', label: note.secondAction, onAction: a.act(note.secondAction)}] : [])]} onMarkRead={() => setRead(true)} onArchive={() => setArchive(true)} onMoveToInbox={() => setArchive(false)} /></Stage>;
}
function TogetherCase({size = 'web', status = 'in-step', compact = false}: {size?: 'web' | 'phone' | 'tv'; status?: 'in-step' | 'waiting' | 'catching-up'; compact?: boolean}) { const a = useAction(); return <Stage padded output={a.output}><WatchTogetherPill name={f.room} participants={f.participants} count={f.participants.length} status={status} waitingFor={f.waitingFor} size={size} compact={compact} onOpen={a.act(f.room)} /></Stage>; }
function NavCase({role = 'owner', search = false, majorIssue = false}: {role?: ServerRole; search?: boolean; majorIssue?: boolean}) {
  const [query, setQuery] = useState(search ? f.settingsQuery : ''), [selected, setSelected] = useState('dashboard');
  const [selectedResult, setSelectedResult] = useState({pageId: 'languages', rowId: 'show-subtitles'}), a = useAction();
  return <Stage output={a.output}><SettingsNav accountName={f.current.name} serverName={shellFixtures['next.dev.shell.serverName']} viewerRole={role} account={f.accountNav} server={f.serverNav.map(entry => ({...entry, majorIssue: majorIssue && entry.id === 'storage'}))} selectedId={selected} selectedSearchResult={selectedResult} onNavigate={setSelected} query={query} onQueryChange={setQuery} matches={query ? f.settingsSearch : undefined} onSearchResult={(pageId, rowId) => { setSelectedResult({pageId, rowId}); a.act(f.settingsSearch.find(group => group.id === pageId)?.rows.find(row => row.id === rowId)?.label ?? t('next.shell.settings'))(); }} /></Stage>;
}
function shortcuts(act: (label: string) => () => void): ShellAction[] { return [{id: 'profile', icon: 'i-switch', label: t('next.shell.switchProfile'), onAction: act(t('next.shell.switchProfile'))}, {id: 'server', icon: 'i-server', label: t('next.shell.switchServer'), onAction: act(t('next.shell.switchServer'))}, {id: 'notifications', icon: 'i-bell', label: t('next.shell.notifications'), onAction: act(t('next.shell.notifications'))}, {id: 'together', icon: 'i-people', label: t('next.shell.watchTogether'), onAction: act(t('next.shell.watchTogether'))}]; }
function ShortcutCase() { const a = useAction(); return <Stage output={a.output}><ShortcutTiles compact items={shortcuts(a.act)} /></Stage>; }
function SheetCase({role = 'owner', majorIssue = false}: {role?: ServerRole; majorIssue?: boolean}) { const [open, setOpen] = useState(true), [query, setQuery] = useState(''), a = useAction(); return <div className={styles.phoneStage}><SettingsSheet open={open} onOpenChange={setOpen} detents={[782]} detent={0} profile={f.current} accountDescription={f.accountDescription} onProfile={a.act(f.current.name)} query={query} onQueryChange={setQuery} shortcuts={shortcuts(a.act)}><SettingsNav layout="phone" accountName={f.current.name} serverName={shellFixtures['next.dev.shell.serverName']} viewerRole={role} account={f.phoneAccountNav} server={f.phoneServerNav.map(entry => ({...entry, stateValue: entry.id === 'remote' ? entry.trailing : undefined, majorIssue: majorIssue && entry.id === 'storage'}))} selectedId="" query={query} onQueryChange={setQuery} onNavigate={id => a.act([...f.phoneAccountNav, ...f.phoneServerNav].find(entry => entry.id === id)?.label ?? t('next.shell.settings'))()} onSearchResult={(_page, id) => a.act(id)()} /></SettingsSheet>{a.output}</div>; }
function HeaderCase({pushed = false}: {pushed?: boolean}) { const a = useAction(); return <Stage output={a.output}>{pushed ? <PushedNavBar safeArea={false} title={f.accountNav[1].label} backLabel={t('next.shell.settings')} onBack={a.act(t('next.shell.settings'))} /> : <LargeTitleHeader title={t('next.shell.settings')} />}</Stage>; }
function RowCase({found = false}: {found?: boolean}) { const [on, setOn] = useState(true), [credits, setCredits] = useState(false), [wait, setWait] = useState('10'); return <div className={styles.box}><SettingsRow label={f.rows.autoplay} help={f.rows.autoplayHelp}><Switch checked={on} onCheckedChange={setOn} aria-label={f.rows.autoplay} /></SettingsRow>{on && <><SettingsRow label={f.rows.wait} help={f.rows.waitHelp} dependent found={found}><Select options={f.seconds} value={wait} onValueChange={setWait} aria-label={f.rows.wait} /></SettingsRow><SettingsRow label={f.rows.credits} help={f.rows.creditsHelp} dependent><Switch checked={credits} onCheckedChange={setCredits} aria-label={f.rows.credits} /></SettingsRow></>}</div>; }
function DefaultCase() { const [source, setSource] = useState('profile'); return <Stage raised><DefaultHint value={f.skipOptions[0].label} effectiveSource={source} onReset={() => setSource('default')} /></Stage>; }
function MatrixCase() {
  const [values, setValues] = useState<Record<string, string>>(Object.fromEntries(f.skipRows.map(row => [row.id, row.value])));
  return <ChoiceMatrix label={f.skipping} choices={f.skipOptions} rows={f.skipRows.map(row => ({...row, value: values[row.id]!, hint: row.id === 'recaps' && <DefaultHint variant="changed" value={f.skipOptions[0].label} effectiveSource={values[row.id] === f.skipOptions[0].value ? 'default' : 'profile'} onReset={() => setValues({...values, [row.id]: f.skipOptions[0].value})} />}))} onChange={(id, value) => setValues({...values, [id]: value})} />;
}
function CardsCase() { const [value, setValue] = useState('auto'); return <ChoiceCards label={f.method} options={f.methodOptions} value={value} onChange={setValue} />; }
function LanguagesCase() { const [languages, setLanguages] = useState<LanguageValue[]>([...f.languages]); return <Stage raised><LanguageList languages={languages} onRemove={id => setLanguages(values => values.filter(value => value.id !== id))} onMove={(id, direction) => setLanguages(values => { const result = [...values], from = result.findIndex(value => value.id === id), to = from + direction; if (from >= 0 && to >= 0 && to < result.length) [result[from], result[to]] = [result[to]!, result[from]!]; return result; })} onAdd={() => setLanguages(values => values.some(value => value.id === f.optionalLanguage.id) ? values : [...values, f.optionalLanguage])} /></Stage>; }
function SubtitleCase({size = 'medium', background = 'translucent'}: {size?: 'small' | 'medium' | 'large' | 'extra-large'; background?: 'none' | 'translucent' | 'opaque'}) { const source = useReferenceArt(f.subtitleAsset); return <SubtitlePreview source={source} size={size} background={background} />; }
function HealthCase() { return <StatTiles variant="health" items={f.metrics.map(item => ({...item, samples: shellReferenceSamples(item.now, item.seed, item.wobble), current: item.now, ceiling: item.peak}))} />; }
function StatsCase({segments = false}: {segments?: boolean}) { return <StatTiles variant={segments ? 'segments' : 'statistics'} items={segments ? f.segmentStatistics : f.statistics} />; }
function BarsCase({people = false}: {people?: boolean}) { return <Stage raised><BarList variant={people ? 'people' : 'titles'} title={people ? f.personBarTitle : f.barTitle} items={people ? f.personBars.map(person => ({...person, label: <><ProfileAvatarFace initial={person.initial} art={person.art} size={22} appearance="session" />{person.name}</>})) : f.bars} maximum={Math.max(...(people ? f.personBars : f.bars).map(item => item.value))} /></Stage>; }
function PeopleCase({role = 'member', disabled = false, index}: {role?: ServerRole; disabled?: boolean; index?: number}) {
  const a = useAction(), person = index !== undefined ? f.people[index]! : disabled ? f.people[4] : f.people.find(person => person.role === role)!;
  const isDisabled = 'disabled' in person && person.disabled;
  return <div className={styles.box}><PersonRow person={person.person} role={person.role} self={'self' in person && person.self} disabled={isDisabled} facts={person.facts} activity={person.activity} playing={!isDisabled} actions={[...(person.role !== 'owner' && !isDisabled ? [{id: 'limits', label: f.limits, onAction: a.act(f.limits)}] : []), {id: 'more', label: person.more, icon: 'i-more', iconOnly: true, onAction: a.act(person.more)}]} />{a.output}</div>;
}
function DeviceCase({state = 'pending'}: {state?: 'pending' | 'allowed' | 'blocked'}) { const [current, setCurrent] = useState(state), a = useAction(); return <div className={styles.box}><DeviceRow name={f.device} icon="i-tv" profile={f.accountPerson} facts={f.deviceFacts} state={current} status={current !== 'pending' ? current === 'allowed' ? f.approved : f.blocked : undefined} actions={current === 'pending' ? [{id: 'block', label: f.block, onAction: () => setCurrent('blocked')}, {id: 'approve', label: f.approve, icon: 'i-check', variant: 'primary', onAction: () => setCurrent('allowed')}] : [{id: 'more', label: f.moreSam, icon: 'i-more', iconOnly: true, onAction: a.act(f.moreSam)}]} />{a.output}</div>; }
function WaitingCase() { const a = useAction(); return <Stage output={a.output}><WaitingNotice icon="i-shield" action={{id: 'review', label: f.review, onAction: a.act(f.review)}}><b>{f.waitingNotice}</b> {f.waitingBody}</WaitingNotice></Stage>; }
function SegmentCase({example}: {example?: 0 | 1 | 2}) {
  const original = example === undefined ? {duration: f.segmentDuration, opening: f.opening, ending: f.ending} : f.segmentExamples[example];
  const [opening, setOpening] = useState<SegmentMark[]>([...original.opening]), [ending, setEnding] = useState<SegmentMark[]>([...original.ending]);
  const [edit, setEdit] = useState<SegmentEdit | undefined>(example === undefined ? {id: f.opening[1].id, opening: 1, start: f.opening[1].start, end: f.opening[1].end, kind: f.opening[1].kind} : undefined), a = useAction();
  const change = (update: (mark: SegmentMark) => SegmentMark) => { setOpening(values => values.map(update)); setEnding(values => values.map(update)); };
  return <Stage raised output={a.output}><SegmentWindows duration={original.duration} opening={opening} ending={ending} edit={edit} onEdit={mark => setEdit({id: mark.id, opening: (edit?.opening ?? 0) + 1, start: mark.start, end: mark.end, kind: mark.kind})} onChange={value => setEdit(current => current && current.opening === value.opening ? value : current)}
    onSave={draft => { if (edit && draft.opening === edit.opening) change(mark => mark.id === draft.id ? {...mark, start: draft.start, end: draft.end} : mark); setEdit(undefined); }} onCancel={token => setEdit(current => current && current.opening === token ? undefined : current)} onPreview={(mark, start) => a.act(`${mark.label}: ${start}`)()} onDismiss={mark => a.act(`Dismiss ${mark.label}`)()} onApprove={mark => change(value => value.id === mark.id ? {...value, suggested: false, trust: f.approved, trustTone: 'neutral'} : value)} /></Stage>;
}
function ActivityCase({mode = 'watching', trigger = false, initialOpen = false, paused = false, role = 'owner', long = false, sparse}: {mode?: 'watching' | 'busy' | 'quiet' | 'update' | 'loading' | 'error' | 'discovery' | 'overflow'; trigger?: boolean; initialOpen?: boolean; paused?: boolean; role?: ServerRole; long?: boolean; sparse?: 'username' | 'details'}) {
  const [open, setOpen] = useState(initialOpen), a = useAction();
  const streams = f.streams.map<ActivityStreamView & {startedAt: string}>((stream, index) => ({...stream, paused, identity: index === 0 ? playbackIdentity({kind: 'video', item: {id: stream.id, kind: 'episode', title: f.streams[0].episodeTitle, show: {id: f.streams[0].showId, title: stream.title}, episode: {mode: 'seasonal', season: f.streams[0].season, episode: f.streams[0].episode}}}, 'full', {t}) : {id: stream.id, kind: 'video', title: stream.title, subtitle: titleFactParts({year: f.streams[1].year}, {t})}, conversion: index === 1 ? {id: 'conversion', text: f.conversion, role: 'status'} : undefined, onOpen: a.act(stream.title)}));
  const overflow = Array.from({length: f.activityCounts.overflowStreams}, (_, index) => ({...streams[index % streams.length]!, id: `session-${index}`, startedAt: new Date(Date.UTC(2026, 9, 22, 12, 0, index)).toISOString(), conversion: index < 7 ? streams[1]!.conversion : undefined, problem: index === 0 ? {id: 'buffering', text: f.activityProblem, role: 'status' as const} : undefined}));
  // DEV catalogue models the published server selection over its complete synthetic set.
  const selected = [...overflow].sort((a, b) => Number(!a.problem) - Number(!b.problem) || Number(!a.conversion) - Number(!b.conversion) || Date.parse(b.startedAt) - Date.parse(a.startedAt) || a.id.localeCompare(b.id)).slice(0, 3);
  const visible = mode === 'overflow' ? selected : [...streams].sort((a, b) => Number(!a.conversion) - Number(!b.conversion) || Date.parse(b.startedAt) - Date.parse(a.startedAt));
  const presented = visible.map(stream => sparse === 'details' ? {...stream, username: undefined, identity: undefined, title: undefined, name: '', device: '', initial: '', positionMs: undefined, durationMs: undefined} : sparse === 'username' ? {...stream, username: undefined} : stream);
  const displayTasks = mode === 'overflow' ? Array.from({length: 3}, (_, index) => ({...f.tasks[index % f.tasks.length]!, id: `task-${index}`})) : f.tasks;
  const idle = mode === 'quiet' || mode === 'update', view: ActivityPanelView = {serverName: shellFixtures['next.dev.shell.serverName'], health: 'normal', streamCount: idle || mode === 'busy' ? 0 : mode === 'overflow' ? overflow.length : visible.length, streamOverflow: mode === 'overflow' ? {count: overflow.length - selected.length, convertingCount: overflow.filter(stream => stream.conversion).length - selected.filter(stream => stream.conversion).length} : undefined, taskCount: idle ? 0 : mode === 'overflow' ? f.activityCounts.overflowTasks : f.tasks.length, streams: idle || mode === 'busy' ? [] : long ? presented.map(stream => ({...stream, username: f.longUsername, name: f.longProfile, device: f.longDevice})) : presented, scans: idle ? [] : [{...f.scan, icon: 'i-film', total: mode === 'discovery' ? undefined : f.scan.total, onOpen: a.act(f.scan.title)}], tasks: idle ? [] : displayTasks.map(task => ({...task, onOpen: a.act(task.title)})), update: {available: mode === 'watching' || mode === 'update' || mode === 'overflow', latest: f.latest, current: f.currentVersion, onOpen: a.act(t('next.shell.updateAvailable'))}, lastScan: f.lastScan, phase: mode === 'loading' ? 'loading' : mode === 'error' ? 'error' : 'ready', onRetry: a.act(t('next.shell.tryAgain')), onDashboard: section => a.act(section ? `${t('next.shell.dashboard')}: ${section}` : t('next.shell.dashboard'))()};
  return <Stage output={a.output}>{trigger ? <div className={styles.activityTrigger}><TopBarActivity viewerRole={role} view={view} open={open} onOpenChange={setOpen} /></div> : <div className={activityStyles.popover}><ActivityPanel viewerRole={role} view={view} /></div>}</Stage>;
}

const extraCases: GalleryCase[] = [
  {id: 'settings-nav-major-r9', labelKey: 'next.shell.settings', width: 296, height: 1320, render: () => <NavCase majorIssue />},
  {id: 'settings-sheet-major-r9', labelKey: 'next.shell.settings', width: 390, height: 844, render: () => <SheetCase majorIssue />},
  ...activityR7Cases,
  {id: 'profile-current', labelKey: 'next.shell.currentProfile', width: 64, height: 64, render: () => <AvatarCase current />},
  {id: 'profile-pin', labelKey: 'next.shell.protected', width: 64, height: 64, render: () => <AvatarCase locked />},
  ...([84, 168] as const).flatMap(size => [{id: `profile-${size}`, labelKey: 'next.shell.currentProfile' as const, width: size + 32, height: size + 32, render: () => <AvatarCase size={size} current={size === 84} />}, {id: `profile-${size}-pin`, labelKey: 'next.shell.protected' as const, width: size + 32, height: size + 32, render: () => <AvatarCase size={size} locked />}, {id: `profile-${size}-restricted`, labelKey: 'next.shell.currentProfile' as const, width: size + 32, height: size + 32, render: () => <AvatarCase size={size} restricted />}]),
  {id: 'editor-header-desktop', labelKey: 'next.shell.server', width: 600, height: 200, render: EditorCase},
  {id: 'editor-header-phone', labelKey: 'next.shell.server', width: 390, height: 150, render: () => <EditorCase layout="phone" />},
  {id: 'account-menu', labelKey: 'next.shell.switchTo', width: 1440, height: 760, render: AccountCase},
  {id: 'account-menu-phone', labelKey: 'next.shell.notifications', width: 390, height: 844, render: () => <AccountCase phone />},
  ...([0, 1, 2] as const).map(index => ({id: `notification-${index}`, labelKey: 'next.shell.notifications' as const, width: 452, height: 210, render: () => <NotificationCase index={index} />})),
  ...([0, 1, 2] as const).map(index => ({id: `notification-phone-${index}`, labelKey: 'next.shell.notifications' as const, width: 358, height: 220, render: () => <NotificationCase index={index} layout="phone" />})),
  {id: 'notification-read', labelKey: 'next.shell.notifications', width: 452, height: 210, render: () => <NotificationCase unread={false} />},
  {id: 'notification-archived', labelKey: 'next.shell.notifications', width: 452, height: 210, render: () => <NotificationCase unread={false} archived />},
  ...(['web', 'phone', 'tv'] as const).flatMap(size => (['in-step', 'waiting', 'catching-up'] as const).map(status => ({id: `together-${size}-${status}`, labelKey: 'next.shell.watchTogether' as const, width: size === 'tv' ? 420 : 320, height: size === 'tv' ? 96 : 70, render: () => <TogetherCase size={size} status={status} />}))),
  {id: 'together-compact', labelKey: 'next.shell.watchTogether', width: 200, height: 70, render: () => <TogetherCase compact />},
  ...(['owner', 'administrator', 'member'] as const).map(role => ({id: `settings-nav-${role}`, labelKey: 'next.shell.settings' as const, width: 296, height: 1320, render: () => <NavCase role={role} />})),
  {id: 'settings-search', labelKey: 'next.shell.searchSettings', width: 296, height: 500, render: () => <NavCase search />},
  {id: 'settings-search-member', labelKey: 'next.shell.searchSettings', width: 296, height: 500, render: () => <NavCase search role="member" />},
  {id: 'settings-sheet-member', labelKey: 'next.shell.settings', width: 390, height: 844, render: () => <SheetCase role="member" />},
  {id: 'settings-sheet', labelKey: 'next.shell.settings', width: 390, height: 844, render: SheetCase},
  {id: 'shortcut-tiles', labelKey: 'next.shell.settings', width: 390, height: 98, render: ShortcutCase},
  {id: 'large-title', labelKey: 'next.shell.settings', width: 390, height: 110, render: HeaderCase},
  {id: 'pushed-nav', labelKey: 'next.shell.settings', width: 390, height: 96, render: () => <HeaderCase pushed />},
  {id: 'settings-row', labelKey: 'next.shell.settings', width: 952, height: 330, render: RowCase},
  {id: 'settings-row-found', labelKey: 'next.shell.settings', width: 952, height: 330, render: () => <RowCase found />},
  {id: 'default-hint', labelKey: 'next.shell.default', width: 400, height: 50, render: DefaultCase},
  {id: 'choice-matrix', labelKey: 'next.shell.settings', width: 952, height: 230, render: MatrixCase},
  {id: 'choice-matrix-phone', labelKey: 'next.shell.settings', width: 390, height: 230, render: MatrixCase},
  {id: 'choice-cards', labelKey: 'next.shell.settings', width: 952, height: 280, render: CardsCase},
  {id: 'languages', labelKey: 'next.shell.settings', width: 952, height: 85, render: LanguagesCase},
  ...(['small', 'medium', 'large', 'extra-large'] as const).flatMap(size => (['none', 'translucent', 'opaque'] as const).map(background => ({id: `subtitle-${size}-${background}`, labelKey: 'next.shell.subtitlePreview' as const, width: 952, height: 360, render: () => <SubtitleCase size={size} background={background} />}))),
  {id: 'health-tiles', labelKey: 'next.shell.settings', width: 952, height: 216, render: HealthCase},
  {id: 'stat-tiles', labelKey: 'next.shell.settings', width: 952, height: 105, render: StatsCase},
  {id: 'segment-tiles', labelKey: 'next.shell.settings', width: 952, height: 90, render: () => <StatsCase segments />},
  {id: 'bar-list-people', labelKey: 'next.shell.settings', width: 420, height: 180, render: () => <BarsCase people />},
  {id: 'bar-list', labelKey: 'next.shell.settings', width: 420, height: 200, render: BarsCase},
  ...(['owner', 'administrator', 'member'] as const).map(role => ({id: `person-${role}`, labelKey: 'next.shell.account' as const, width: 952, height: 100, render: () => <PeopleCase role={role} />})),
  {id: 'person-local', labelKey: 'next.shell.account', width: 952, height: 100, render: () => <PeopleCase index={3} />},
  {id: 'person-disabled', labelKey: 'next.shell.account', width: 952, height: 100, render: () => <PeopleCase disabled />},
  ...(['pending', 'allowed', 'blocked'] as const).map(state => ({id: `device-${state}`, labelKey: 'next.shell.settings' as const, width: 952, height: 90, render: () => <DeviceCase state={state} />})),
  {id: 'waiting-notice', labelKey: 'next.shell.settings', width: 952, height: 85, render: WaitingCase},
  {id: 'segment-windows', labelKey: 'next.shell.settings', width: 672, height: 300, render: SegmentCase},
  {id: 'segment-approved', labelKey: 'next.shell.settings', width: 672, height: 300, render: () => <SegmentCase example={0} />},
  {id: 'segment-chapter', labelKey: 'next.shell.settings', width: 672, height: 300, render: () => <SegmentCase example={2} />},
  {id: 'segment-suggested', labelKey: 'next.shell.settings', width: 672, height: 300, render: () => <SegmentCase example={1} />},
  ...(['watching', 'busy', 'quiet', 'update', 'loading', 'error', 'discovery', 'overflow'] as const).flatMap(mode => [{id: `activity-${mode}`, labelKey: 'next.shell.activity' as const, width: 420, height: 900, render: () => <ActivityCase mode={mode} />}, {id: `activity-trigger-${mode}`, labelKey: 'next.shell.activity' as const, width: 1440, height: 816, render: () => <ActivityCase mode={mode} trigger />}]),
  {id: 'activity-open', labelKey: 'next.shell.activity', width: 1440, height: 816, render: () => <ActivityCase trigger initialOpen />},
  {id: 'activity-paused', labelKey: 'next.shell.activity', width: 420, height: 900, render: () => <ActivityCase paused />},
  {id: 'activity-username-unavailable', labelKey: 'next.shell.activity', width: 420, height: 900, render: () => <ActivityCase sparse="username" />},
  {id: 'activity-missing-details', labelKey: 'next.shell.activity', width: 420, height: 900, render: () => <ActivityCase sparse="details" />},
  {id: 'activity-long', labelKey: 'next.shell.activity', width: 420, height: 900, render: () => <ActivityCase long />},
  ...(['administrator', 'member'] as const).map(role => ({id: `activity-${role}`, labelKey: 'next.shell.activity' as const, width: 1440, height: 100, render: () => <ActivityCase role={role} trigger />})),
];

export default defineGalleryFamily({id: 'shell', labelKey: 'next.shell.galleryTitle', cases: [
  {id: 'owner-state', labelKey: 'next.shell.case.ownerState', width: 390, height: 300, render: OwnerState},
  {id: 'owner-details', labelKey: 'next.shell.case.ownerDetails', width: 620, height: 62, render: OwnerDetails},
  {id: 'owner-compact', labelKey: 'next.shell.case.ownerCompact', width: 354, height: 54, render: CompactOwner},
  {id: 'server-header-owner', labelKey: 'next.shell.case.serverHeader', width: 1440, height: 420, render: HeaderOwner},
  {id: 'server-header-shared-owner', labelKey: 'next.shell.case.serverShared', width: 1440, height: 420, render: HeaderShared},
  {id: 'server-header-administrator', labelKey: 'next.shell.case.serverAdministrator', width: 1440, height: 420, render: HeaderAdministrator},
  ...extraCases,
]});
