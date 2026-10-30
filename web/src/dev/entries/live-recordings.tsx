import {createRoot} from 'react-dom/client';
import {defaultI18n} from '@i18n';
import type {GuideProgramme} from '@core/channel-guide.ts';
import type {Recording, RecordingDetail, RecordingStorage} from '@core/dvr.ts';
import {Chip} from '../../kit/controls';
import {Icon, IconSprite} from '../../kit/Icon';
import {ProfileAvatarFace, TopBarActivity} from '../../kit/shell';
import {RecordingsLayout, RecordingStorage as StorageMeter} from '../../kit/dvr/RecordingsLayout';
import {LiveRecordingTabs} from '../../screens/live/SeriesRules';
import {RecordedView, UpcomingView} from '../../screens/live/RecordingsScreen';
import type {RecordingsController, RecordingsSnapshot} from '../../screens/live/recordings-controller';
import type {ScreenContext} from '../../screens/detail/context';
import shell from '../../shell/shell.module.css';
import live from '../../screens/live/live.module.css';
import wordmark from '../../shell/wordmark.svg';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// The drawn demo recordings and guide facts live only in this DEV specimen.
if (!import.meta.env.DEV) throw new Error('Recordings specimens require the development server.');
const t = defaultI18n.t;
const NOW = Date.UTC(2026, 9, 21, 19, 5);
const at = (hour: number, minutes: number) => Date.UTC(2026, 9, 21, hour + 3, minutes);
const iso = (ms: number) => new Date(ms).toISOString();
const programme = (title: string, from: number, to: number, extra: Partial<GuideProgramme> = {}): GuideProgramme => ({id: `p-${title}`, channelId: 'c',
  title, start: iso(from), end: iso(to), lineage: 'provider-id', seriesId: '', episodeId: '', newEvidence: 'unknown', description: '', ...extra});
const channel = (name: string, number: string, mark: string) => ({id: `c-${mark}`, name, number, mark: {text: mark, seed: `synthetic:${mark}`}});
const recording = (id: string, state: Recording['state'], title: string, from: number, to: number, channelFact: ReturnType<typeof channel>, extra: Partial<Recording> = {}, programmeExtra: Partial<GuideProgramme> = {}): Recording => ({
  id, revision: 1, occurrence: {sourceId: 'synthetic', channelId: channelFact.id, generation: 'synthetic', programmeId: `p-${title}`},
  programme: programme(title, from, to, programmeExtra), ruleId: '', options: {beforeSeconds: 0, afterSeconds: 0, priority: 0, retentionDays: 0, episodeLimit: 0},
  start: iso(from), end: iso(to), state, reason: '', keep: false, itemId: '', libraryId: '', coverageStart: '', coverageEnd: '', bytes: 0,
  conflicts: [], channel: channelFact, watched: null, ...extra});
const pebble = channel('Pebble Kids', '401', 'PK'), atlas = channel('Atlas Documentaries', '501', 'AD'), summit = channel('Summit Sports Extra', '202', 'SSE'); // lint-strings-allow fixture data
const conflict = recording('r-conflict', 'conflicted', 'Summit Football: Harbor City vs. Ridgeview', at(20, 0), at(21, 0), summit, // lint-strings-allow fixture data
  {conflicts: [{start: iso(at(20, 0)), end: iso(at(20, 15)), demand: 3, capacity: 2}], ruleId: 'rule-2'}, {subtitle: t('next.live.example.football'), flags: {live: true}, episode: {season: 5, number: 9}});
const active = recording('r-active', 'recording', 'Story Time Island', at(16, 0), at(16, 30), pebble, {watched: false},
  {episode: {season: 3, number: 1}, subtitle: 'Homecoming', flags: {new: true}, description: 'Story Time Island on Pebble Kids.'}); // lint-strings-allow fixture data
const later = recording('r-later', 'scheduled', 'The Clockmakers', at(16, 15), at(17, 0), atlas, {ruleId: 'rule-1'}, {episode: {season: 5, number: 9}, subtitle: t('next.live.example.crossed')});
const later2 = recording('r-later2', 'waiting-source', 'Wild Plains', at(18, 45), at(19, 45), atlas);
const detail: RecordingDetail = {recording: conflict, capacity: {known: true, effective: 2}, nextOverlap: '',
  overlaps: [{id: 'o1', title: 'The Long Road North', start: iso(at(17, 53)), end: iso(at(20, 20)), priority: 0, state: 'recording'}, // lint-strings-allow fixture data
    {id: 'o2', title: 'The Clockmakers', start: iso(at(19, 30)), end: iso(at(20, 15)), priority: 0, state: 'scheduled'}]}; // lint-strings-allow fixture data
const storage: RecordingStorage = {policy: {revision: 1, retentionDays: 30, episodeLimit: 10, floorBytes: 0, capBytes: 0},
  measurement: {freeBytes: 412e9, usedBytes: 64e9, reservedBytes: 0, writeHealthy: true, measuredAt: iso(NOW)},
  pendingDeleteBytes: 0, forecastBytes: 0, forecastHours: 230, forecastDescription: '', captureAvailable: true, warning: ''};
const groupRecording = (title: string, channelFact: ReturnType<typeof channel>, recorded: number, extra: Partial<Recording> = {}) =>
  recording(`g-${title}`, 'completed', title, recorded, recorded + 30 * 60_000, channelFact, {watched: false, ...extra});
const groups = {revision: 1, nextCursor: '', groups: [
  {key: 'series:clockmakers', title: 'The Clockmakers', recordings: 6, unwatched: 2, kept: 1, exceptions: 0, latest: iso(at(16, 15)), recording: groupRecording('The Clockmakers', atlas, at(16, 15), {seriesId: 'clockmakers'})}, // lint-strings-allow fixture data
  {key: 'series:story', title: 'Story Time Island', recordings: 3, unwatched: 1, kept: 0, exceptions: 0, latest: iso(at(16, 0)), recording: groupRecording('Story Time Island', pebble, at(16, 0), {seriesId: 'story'})}, // lint-strings-allow fixture data
  {key: 'one:longroad', title: 'The Long Road North', recordings: 1, unwatched: 1, kept: 0, exceptions: 0, latest: iso(at(17, 53)), recording: groupRecording('The Long Road North', atlas, at(17, 53))}]}; // lint-strings-allow fixture data
const snapshot: RecordingsSnapshot = {query: {view: 'upcoming'}, phase: 'ready',
  page: {recordings: [conflict, active, later, later2], rules: [], nextCursor: '', revision: 1, captureAvailable: true, captureUnavailableReason: '',
    deletionAvailable: true, usage: {bytes: 0, pendingDeleteBytes: 0, recordings: 4}},
  detail: null, storage, summary: {upcoming: 8, recorded: 20, rules: 4, conflicts: 1, groups: 6, unwatched: 8}, groups, groupsSort: 'recent',
  paging: {busy: false, error: '', previous: false, next: false}, accessDenied: false, error: '', mutating: false, commandError: ''};
const context = {isCurrent: () => false, scope: {serverId: 'synthetic'}, effects: {play: async () => {}, reportError: () => {}},
  api: {request: async () => { throw new Error('No reads in the specimen.'); }}} as unknown as ScreenContext;
const controller = {cancel: async () => ({committed: false, refreshed: false, busy: true}), raisePriority: async () => ({committed: false, refreshed: false, busy: true}),
  readGroups: async () => groups} as unknown as RecordingsController;
const details = {[conflict.id]: detail} as const;

function GuideNavigation() {
  return <nav className={shell.topnav} aria-label={t('next.home.shell.main')}>
    <a className={shell.brand} href="#"><img src={wordmark} alt={t('next.platform.appName')}/></a>
    <ul className={shell.destinations}>{(['next.home.shell.home', 'search.chip.movies', 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.1.label', 'web.libraries.kindAnime', 'pref.group.music.title',
      'search.chip.audiobooks', 'next.home.shell.live'] as const).map(key => <li key={key}><a href="#" aria-current={key === 'next.home.shell.live' ? 'page' : undefined}>{t(key)}</a></li>)}</ul>
    <div className={shell.tools}>
      <button className={shell.icon} aria-label={t('next.home.shell.search')}><Icon name="i-search" size={22}/></button>
      <button className={shell.icon} aria-label={t('next.home.shell.saved')}><Icon name="i-bookmark" size={22}/></button>
      <button className={shell.icon} aria-label={t('next.home.shell.unread', {count: 3})}><Icon name="i-bell" size={22}/><span className={shell.dot}/></button>
      <TopBarActivity viewerRole="owner" open={false} onOpenChange={() => {}} view={{serverName: t('next.live.example.source'), streamCount: 2, streams: [], scans: [], tasks: [], phase: 'ready', onRetry() {}, onDashboard() {}}}/>
      <span className={shell.avatar}><ProfileAvatarFace initial="D" size={34}/></span>
    </div>
  </nav>;
}

function Specimen() {
  const view = new URLSearchParams(location.search).get('view') === 'recorded' ? 'recorded' as const : 'upcoming' as const;
  const views = [
    {id: 'upcoming' as const, label: t('recordings.upcoming'), count: snapshot.summary?.upcoming},
    {id: 'recorded' as const, label: t('recordings.recorded'), count: snapshot.summary?.recorded},
    {id: 'rules' as const, label: t('recordings.rules'), count: snapshot.summary?.rules},
    {id: 'history' as const, label: t('saved.tab.history')},
    {id: 'storage' as const, label: t('recordings.storage')},
  ];
  return <RecordingsLayout title={t('next.home.shell.live')} sourceLabel={t('channels.source')} allChannels={t('channels.all')}
    sourceName={t('next.live.example.source')} channels={t('next.kit.channels', {count: 14})} through={t('next.live.guideThrough', {day: t('next.live.example.saturday')})}>
    <LiveRecordingTabs view="recordings" conflicts={1} onChange={() => {}}/>
    <div className={live.recordingsBar}>
      <div className={live.recordingsChips} role="group" aria-label={t('recordings.title')}>
        {views.map(item => <Chip className={live.recordingsChip} key={item.id} selected={item.id === view} count={item.count} onClick={() => {}}>{item.label}</Chip>)}
      </div>
      <button type="button" className={live.storageButton} aria-label={t('recordings.storage')}>
        <StorageMeter used={t('next.live.storageUsed', {amount: 64})} free={t('next.live.storageFree', {amount: 412})} remaining={t('recordings.hoursLeft', {count: 230})}
          label={t('recordings.storage')} fraction={64 / 476}/>
      </button>
    </div>
    {view === 'upcoming' ? <UpcomingView context={context} controller={controller} snapshot={snapshot} now={NOW} onResolve={() => {}} conflictDetails={details}/>
      : <RecordedView context={context} controller={controller} current={context.isCurrent} snapshot={snapshot}/>}
  </RecordingsLayout>;
}

createRoot(document.getElementById('root')!).render(<><IconSprite /><main data-recordings-specimen><div className={shell.shell} data-desktop="true"><GuideNavigation /><Specimen /></div></main></>);
