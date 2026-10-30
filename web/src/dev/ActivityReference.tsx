import {useState} from 'react';
import {createI18n} from '@i18n';
import {activityR7Reference, activityR9Reference, shellReference as f, shellFixtures} from '../../../packages/i18n/src/catalog/en-US/next/dev/shell-fixtures';
import {TopBarActivity, ProfileAvatarFace, type ActivityPanelView, type ActivityStreamView, type ServerRole} from '../kit/shell';
import {Icon} from '../kit/Icon';
import type {GalleryCase} from './gallery-registry';
import styles from '../kit/shell/activity-gallery.module.css';

const t = createI18n().t;
type Load = 'one' | 'three' | 'many' | 'quiet' | 'update' | 'working' | 'two' | 'both' | 'major' | 'major-glyph' | 'major-collision';
type LiteralStream = (typeof activityR7Reference.streams)[keyof typeof activityR7Reference.streams];

/** Synthetic component inputs from the drawing; these never masquerade as an API document. */
export function activityReferenceView(load: Load, onDashboard: ActivityPanelView['onDashboard'], openUpdate: () => void): ActivityPanelView {
  const source = activityR7Reference.streams;
  const major = load.startsWith('major');
  const rows: readonly LiteralStream[] = load === 'many' ? [source.alex, source.jordan, source.dana] : load === 'one' ? [source.sam] : load === 'three' || load === 'major' ? [source.priya, source.sam, source.nana] : load === 'two' || load === 'both' || load === 'major-glyph' ? [source.priya, source.sam] : [];
  const work = ['one', 'three', 'many', 'working', 'major'].includes(load);
  return {serverName: shellFixtures['next.dev.shell.serverName'], health: 'normal', phase: 'ready',
    majorIssue: major ? {...activityR9Reference, onOpen: () => onDashboard()} : undefined,
    streamCount: load === 'many' ? 24 : rows.length, streamOverflow: load === 'many' ? {count: 21, convertingCount: 4} : undefined,
    streams: rows.map<ActivityStreamView>(row => ({...row, identity: {id: row.id, kind: 'video', title: row.title, subtitle: row.subtitle},
      positionMs: row.progress, durationMs: 100, paused: false,
      conversion: 'conversion' in row ? {id: 'conversion', text: row.conversion, role: 'status'} : undefined,
      problem: 'problem' in row ? {id: 'problem', text: row.problem, role: 'status'} : undefined,
      onOpen: () => onDashboard('streams')})),
    scans: work ? [{...f.scan, icon: 'i-film', onOpen: () => onDashboard('activity')}] : [],
    tasks: work ? f.tasks.map(task => ({...task, onOpen: () => onDashboard('activity')})) : [], taskCount: work ? f.tasks.length : 0,
    update: {available: work && load !== 'working' || load === 'update' || load === 'both' || load === 'major-collision', latest: f.latest, current: f.currentVersion, onOpen: openUpdate},
    lastScan: f.lastScan, onDashboard, onRetry: () => {},
  };
}

function ActivityReference({load = 'three', initialOpen = false, role = 'owner', specimen = false}: {load?: Load; initialOpen?: boolean; role?: ServerRole; specimen?: boolean}) {
  const [open, setOpen] = useState(initialOpen), [destination, setDestination] = useState('');
  const onDashboard: ActivityPanelView['onDashboard'] = section => setDestination(section ? `${t('next.shell.dashboard')}: ${section}` : t('next.shell.dashboard'));
  const view = activityReferenceView(load, onDashboard, () => setDestination(t('next.shell.updateAvailable')));
  const [resolved, setResolved] = useState(false), [currentRole, setRole] = useState(role), [phase, setPhase] = useState(view.phase);
  const majorHarness = load.startsWith('major');
  const currentView: ActivityPanelView = majorHarness ? {...view, phase, majorIssue: resolved ? undefined : {...activityR9Reference, onOpen: () => setDestination(activityR9Reference.destination)}} : view;
  const top = <TopBarActivity viewerRole={currentRole} view={currentView} open={open} onOpenChange={setOpen} />;
  return <div className={styles.stage} data-reference-open={open || undefined} data-specimen={specimen || undefined}>
    {specimen ? <div className={styles.specimen}>{top}</div> : <div className={styles.tools}>
      <button type="button" aria-label={t('next.home.shell.search')}><Icon name="i-search" size={22} /></button>
      <button type="button" aria-label={t('next.home.shell.saved')}><Icon name="i-bookmark" size={22} /></button>
      <button type="button" aria-label={t('next.shell.notifications')}><Icon name="i-bell" size={22} />{load !== 'update' && <span className={styles.notificationDot} aria-hidden="true" />}</button>
      {top}<span className={styles.avatar}><ProfileAvatarFace initial={role === 'member' ? f.streams[0].initial : f.current.initial} art={role === 'member' ? 'mint' : 'blue'} /></span>
    </div>}
    {majorHarness && !specimen && <div className={styles.harness} data-major-harness="true"><button type="button" onClick={() => setResolved(true)}>{activityR9Reference.resolve}</button><button type="button" onClick={() => setRole('member')}>{t('next.shell.member')}</button><button type="button" onClick={() => setPhase('loading')}>{t('next.shell.loadingActivity')}</button></div>}
    <output className={styles.output} hidden={!destination}>{destination}</output>
  </div>;
}

export const activityR7Cases: readonly GalleryCase[] = [
  {id: 'activity-r9-major', labelKey: 'next.shell.activity', width: 1440, height: 890, render: () => <ActivityReference load="major" initialOpen />},
  {id: 'activity-r9-major-glyph', labelKey: 'next.shell.activity', width: 180, height: 120, render: () => <ActivityReference load="major-glyph" specimen />},
  {id: 'activity-r9-major-collision', labelKey: 'next.shell.activity', width: 1440, height: 890, render: () => <ActivityReference load="major-collision" />},
  {id: 'activity-r7-rest', labelKey: 'next.shell.activity', width: 1440, height: 816, render: () => <ActivityReference />},
  ...(['one', 'three', 'many', 'quiet'] as const).map(load => ({id: `activity-r7-${load}`, labelKey: 'next.shell.activity' as const, width: 1440, height: load === 'many' ? 900 : 816, render: () => <ActivityReference load={load} initialOpen />})),
  {id: 'activity-r7-update', labelKey: 'next.shell.activity', width: 1440, height: 816, render: () => <ActivityReference load="update" />},
  {id: 'activity-r7-member', labelKey: 'next.shell.activity', width: 1440, height: 816, render: () => <ActivityReference role="member" />},
  {id: 'activity-r7-administrator', labelKey: 'next.shell.activity', width: 1440, height: 816, render: () => <ActivityReference role="administrator" />},
  ...(['quiet', 'working', 'two', 'update', 'both'] as const).map(load => ({id: `activity-r7-glyph-${load}`, labelKey: 'next.shell.activity' as const, width: 180, height: 120, render: () => <ActivityReference load={load} specimen />})),
];
