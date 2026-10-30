import {useId} from 'react';
import {createI18n} from '@i18n';
import {timelineFacts, type FactPart, type PlaybackIdentityModel} from '@core/presentation/kit-model';
import {Icon, type IconName} from '../Icon';
import {Popover} from '../overlays/Popover';
import type {OverlayChange} from '../overlays/types';
import {ServerMark, type ServerRole, type ShellTranslate} from './ServerIdentity';
import {ProfileAvatarFace, type ProfileTone} from './AccountSurfaces';
import {counterProgress, differingProfileName} from './model';
import {ShellScrim} from './ShellScrim';
import styles from './activity.module.css';

const tDefault = createI18n().t;
export type ActivityStreamView = Readonly<{
  id: string; identity?: PlaybackIdentityModel; title?: string; name: string; device: string; initial: string; art?: ProfileTone;
  username?: string; startedAt?: string; problem?: FactPart;
  positionMs?: number; durationMs?: number; paused: boolean; conversion?: FactPart; rate?: FactPart; onOpen: () => void;
}>;
export type ActivityWorkView = Readonly<{
  id: string; title: string; detail?: string; current?: string; icon: IconName; processed?: number; total?: number;
  discovered?: number; progressText?: string; onOpen: () => void;
}>;
/** Supplied only for a qualified playback-stopping or data-risk issue, never a warning count. */
export type ActivityMajorIssueView = Readonly<{title: string; description: string; since?: string; onOpen: () => void}>;
export type ActivityPanelView = Readonly<{
  serverName: string; health?: 'normal' | 'attention'; streamCount?: number; taskCount?: number; streamOverflow?: Readonly<{count: number; convertingCount: number}>;
  streams: readonly ActivityStreamView[]; scans: readonly ActivityWorkView[]; tasks: readonly ActivityWorkView[];
  majorIssue?: ActivityMajorIssueView;
  update?: Readonly<{available: boolean; latest?: string; current?: string; onOpen: () => void}>;
  lastScan?: string; phase: 'ready' | 'loading' | 'error'; onRetry: () => void; onDashboard: (section?: 'streams' | 'activity') => void;
}>;

/** No store is created here. The owner shell supplies its existing freshness subscription's real summary. */
export function TopBarActivity({viewerRole, view, open, onOpenChange, t = tDefault}: {viewerRole: ServerRole; view: ActivityPanelView; open: boolean; onOpenChange: OverlayChange; t?: ShellTranslate}) {
  if (viewerRole !== 'owner') return null;
  return <OwnerActivity view={view} open={open} onOpenChange={onOpenChange} t={t} />;
}
function OwnerActivity({view, open, onOpenChange, t}: {view: ActivityPanelView; open: boolean; onOpenChange: OverlayChange; t: ShellTranslate}) {
  const tip = useId(), known = view.phase === 'ready';
  const streamCount = known ? view.streamCount : undefined, update = known && view.update?.available;
  const majorIssue = known ? view.majorIssue : undefined;
  const busy = known && ((streamCount ?? 0) > 0 || view.scans.length > 0 || (view.taskCount ?? 0) > 0);
  const activityLabel = streamCount === undefined ? t('next.shell.activity') : t(update ? 'next.shell.activityLabelUpdate' : 'next.shell.activityLabel', {count: streamCount});
  const label = majorIssue ? t('next.shell.activityLabelMajor', {activity: activityLabel, title: majorIssue.title}) : activityLabel;
  const tooltip = majorIssue ? update ? t('next.shell.activityIssueUpdateHint', {title: majorIssue.title}) : majorIssue.title : update ? t('next.shell.updateAvailable') : undefined;
  const trigger = <button type="button" className={styles.trigger} data-busy={busy || undefined} data-open={open || undefined} data-streams={(streamCount ?? 0) > 0 || undefined} aria-label={label} aria-haspopup="dialog" aria-expanded={open} aria-describedby={tooltip && !open ? tip : undefined}>
    <Icon name="i-act" size={22} />{(streamCount ?? 0) > 0 && <span className={styles.streamCount}>{streamCount}</span>}{/* Both facts survive; the major issue owns their overlapping visual slot. */}{majorIssue ? <span className={styles.majorDot} aria-hidden="true" /> : update && <span className={styles.updateDot} aria-hidden="true" />}
  </button>;
  return <span className={styles.triggerWrap}><ShellScrim open={open} context="activity" /><Popover open={open} onOpenChange={onOpenChange} trigger={trigger} title={t('next.shell.activity')} width={420} sideOffset={14} alignOffset={-72} className={styles.popover} modal>
    <ActivityPanel viewerRole="owner" view={view} t={t} />
  </Popover>{tooltip && !open && <span id={tip} role="tooltip" className={styles.tooltip}>{tooltip}</span>}</span>;
}

export function ActivityPanel({viewerRole, view, t = tDefault}: {viewerRole: ServerRole; view: ActivityPanelView; t?: ShellTranslate}) {
  if (viewerRole !== 'owner') return null;
  const streams = view.streams, tasks = view.tasks.slice(0, 3);
  if (streams.length > 3) throw new RangeError('Activity requires the server-selected maximum of three sessions.');
  const known = view.phase === 'ready';
  const majorIssue = known ? view.majorIssue : undefined;
  const quiet = known && view.streamCount === 0 && view.scans.length === 0 && view.taskCount === 0;
  return <div className={styles.panel}>
    <header className={styles.header}><ServerMark size={38} /><div><b>{view.serverName}</b>{known && (majorIssue || view.health !== undefined) && <span className={styles.health} data-health={majorIssue ? 'major' : view.health}>{t(majorIssue ? 'next.shell.health.major' : view.health === 'normal' ? 'next.shell.health.normal' : 'next.shell.health.attention')}</span>}</div></header>
    <button type="button" className={styles.dashboard} onClick={() => view.onDashboard()}><Icon name="i-pulse" size={20} />{t('next.shell.dashboard')}<span>{known && view.streamCount !== undefined ? t('next.shell.streams', {count: view.streamCount}) : null}</span><Icon name="i-right" size={18} /></button>
    <div className={styles.divider} />
    {view.phase === 'loading' ? <div className={styles.loading} role="status" aria-label={t('next.shell.loadingActivity')}><i /><i /><i /></div> : view.phase === 'error' ? <div className={styles.error}><p>{t('next.shell.activityError')}</p><button type="button" onClick={view.onRetry}>{t('next.shell.tryAgain')}</button></div> : <>
      {majorIssue && <button type="button" className={styles.work} data-major-issue="true" onClick={majorIssue.onOpen} aria-label={t('next.shell.activityOpenIssue', {title: majorIssue.title})}><span className={styles.majorLead}><Icon name="i-warn" size={18} /></span><span className={styles.workText}><span className={styles.line1}><b>{majorIssue.title}</b></span><span className={styles.line2}><span>{majorIssue.description}</span>{majorIssue.since && <span>{majorIssue.since}</span>}</span></span><span className={styles.issueOpen}>{t('next.shell.open')}</span></button>}
      {quiet ? <div className={styles.quiet}><span className={styles.quietLead}><Icon name="i-act" size={18} /></span><div><b>{t('next.shell.activityQuiet')}</b>{view.lastScan && <p>{t('next.shell.lastScan', {when: view.lastScan})}</p>}</div></div> : <>
        {(view.streamCount ?? 0) > 0 && <section className={styles.group}><h4>{t('next.shell.nowPlaying')}</h4>{streams.map(stream => <ActivityStream key={stream.id} stream={stream} t={t} />)}{view.streamOverflow !== undefined && view.streamOverflow.count > 0 && <button type="button" className={styles.work} data-streams-more="true" aria-label={[t('next.shell.moreStreaming', {count: view.streamOverflow.count}), ...(view.streamOverflow.convertingCount > 0 ? [t('next.shell.moreConverting', {count: view.streamOverflow.convertingCount})] : []), t('next.shell.dashboard')].join(', ')} onClick={() => view.onDashboard('streams')}><span className={styles.moreLead}><Icon name="i-people" size={16} /></span><span className={styles.workText}><span className={styles.line1}><b>{t('next.shell.moreStreaming', {count: ` ${view.streamOverflow.count}`})}</b>{view.streamOverflow.convertingCount > 0 && <span className={styles.moreConverting}><Icon name="i-swap" size={14} />{t('next.shell.moreConverting', {count: view.streamOverflow.convertingCount})}</span>}</span></span><Icon name="i-right" size={18} className={styles.chevron} /></button>}</section>}
        {view.scans.length > 0 && <section className={styles.group}><h4>{t('next.shell.libraryScans')}</h4>{view.scans.map(scan => <ActivityWork key={scan.id} work={scan} scan t={t} />)}</section>}
        {(view.taskCount ?? 0) > 0 && <section className={styles.group}><h4>{t('next.shell.backgroundTasks')}</h4>{tasks.map(task => <ActivityWork key={task.id} work={task} t={t} />)}{view.taskCount !== undefined && view.taskCount > tasks.length && <More count={view.taskCount - tasks.length} onClick={() => view.onDashboard('activity')} t={t} />}</section>}
      </>}
      {view.update?.available && <><div className={styles.divider} /><button type="button" className={styles.work} data-update="true" onClick={view.update.onOpen}><span className={styles.updateLead}><Icon name="i-arrow-up" size={18} /></span><span className={styles.workText}><span className={styles.line1}><b>{t('next.shell.updateAvailable')}</b></span><span className={styles.line2}>{view.update.latest && <span>{t('next.shell.version', {version: view.update.latest})}</span>}{view.update.current && <span>{t('next.shell.youHaveVersion', {version: view.update.current})}</span>}</span></span><Icon name="i-right" size={18} className={styles.chevron} /></button></>}
    </>}
  </div>;
}
function More({count, onClick, t}: {count: number; onClick: () => void; t: ShellTranslate}) { return <button type="button" className={styles.more} onClick={onClick}>{t('next.shell.moreDashboard', {count})}</button>; }
function ActivityStream({stream, t}: {stream: ActivityStreamView; t: ShellTranslate}) {
  const fraction = timelineFacts(stream.durationMs, stream.positionMs).progress;
  const profile = differingProfileName(stream.name, stream.username);
  return <button type="button" className={styles.work} data-stream="true" data-session-id={stream.id} onClick={stream.onOpen}><ProfileAvatarFace initial={stream.initial} art={stream.art} size={32} appearance="session" /><span className={styles.workText}>
    <span className={styles.line1}><b>{stream.identity?.title || stream.title || t('next.shell.playbackDetailsUnavailable')}</b>{stream.identity?.subtitle.map(part => <span key={part.id} data-code={part.id === 'episode-label' || undefined}>{part.text}</span>)}</span>
    <span className={styles.line2}><em>{stream.username || t('next.shell.usernameUnavailable')}</em>{profile && <span className={styles.profile}>{profile}</span>}{stream.device && <span className={styles.device}>{stream.device}</span>}{stream.problem ? <span className={styles.streamNote} data-problem="true">{stream.problem.text}</span> : stream.conversion ? <span className={styles.streamNote}><Icon name="i-swap" size={14} />{stream.conversion.text}</span> : null}{stream.rate && <span className={styles.streamNote}>{stream.rate.text}</span>}</span>
    {fraction !== undefined && <span className={styles.progress} data-paused={stream.paused || undefined} aria-hidden="true"><span style={{transform: `scaleX(${fraction})`}} /></span>}
  </span></button>;
}
function ActivityWork({work, scan = false, t}: {work: ActivityWorkView; scan?: boolean; t: ShellTranslate}) {
  const fraction = counterProgress(work.processed, work.total);
  const detail = scan ? fraction !== undefined ? t('next.shell.filesOf', {processed: work.processed!, total: work.total!}) : work.discovered !== undefined ? t('next.shell.filesFound', {count: work.discovered}) : work.detail : work.detail;
  const bottom = scan ? work.current : work.progressText;
  return <button type="button" className={styles.work} onClick={work.onOpen}><ProgressRing icon={work.icon} fraction={fraction} /><span className={styles.workText}><span className={styles.line1}><b>{work.title}</b>{detail && <span>{detail}</span>}</span>{bottom && <span className={styles.line2}><span className={styles.file}>{bottom}</span></span>}</span>{fraction !== undefined && <span className={styles.percentage}>{Math.round(fraction * 100)}%</span>}</button>;
}
export function ProgressRing({fraction, icon}: {fraction?: number; icon: IconName}) {
  const known = fraction !== undefined && Number.isFinite(fraction) && fraction >= 0 && fraction <= 1;
  return <span className={styles.ring} aria-hidden="true"><svg width={32} height={32} viewBox="0 0 32 32"><circle className={styles.ringTrack} cx={16} cy={16} r={14} />{known && <circle className={styles.ringValue} cx={16} cy={16} r={14} pathLength={100} strokeDasharray={`${fraction * 100} 100`} />}</svg><Icon name={icon} size={15} /></span>;
}
