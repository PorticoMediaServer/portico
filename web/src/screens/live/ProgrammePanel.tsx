import {forwardIdentity} from '../detail/context';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {channelTint, programmeFactParts, type ChannelMarkModel} from '@portico/client-core/kit-model';
import {legacyProgramme, type GuideChannelRow, type GuideProgram} from '@core/guide/index.ts';
import {Button, Segmented, Select} from '../../kit/controls';
import type {SelectOption} from '../../kit/controls';
import {ChannelMark} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {Banner, BottomSheet, Dialog, Menu, SidePanel, type MenuEntry} from '../../kit/overlays';
import {useShellWidth} from '../../shell/viewport';
import {useWindowHeight} from '../../kit/theme';
import type {ScreenContext} from '../detail/context';
import type {SelectionAdmission} from './row-witness';
import {createProgrammeBindings, defaultSeriesChoice, type PreviewAiring, type ProgrammeSnapshot, type SeriesChoice} from './programme-bindings';
import {readReminders, subscribeReminders, toggleReminder} from './reminders';
import styles from './live.module.css';

const t = defaultI18n.t;
const supportsNewEpisodes = (programme?: GuideProgram) => !!programme && ['new', 'repeat'].includes(legacyProgramme(programme)?.newEvidence ?? '');
const KEEPS = ['all', '1', '3', '5', '10', '20'] as const;
const PADDINGS = [0, 1, 2, 5, 10, 15] as const;
const keepLabel = (value: string) => value === 'all' ? t('recordings.keepAll') : value === '1' ? t('next.live.keepLatest') : t('next.live.keepLatestCount', {count: Number(value)});
const paddingLabel = (minutes: number) => minutes ? t('next.live.minutes', {count: minutes}) : t('recordings.none');
const keepOptions: readonly SelectOption[] = KEEPS.map(value => ({value, label: keepLabel(value)}));
const paddingOptions: readonly SelectOption[] = PADDINGS.map(value => ({value: String(value), label: paddingLabel(value)}));

const channelMark = (channel: GuideChannelRow): ChannelMarkModel => ({
  sourceId: channel.sourceId ?? '', id: channel.id,
  ...(channel.mark ?? {text: Array.from(channel.name.trim())[0]?.toLocaleUpperCase() ?? '', seed: channel.id}),
});

/** One upcoming airing's reminder; a local browser fact, never a server promise. */
function reminderIds(): string {
  return JSON.stringify(readReminders().map(reminder => reminder.programId));
}

export type ProgrammePanelViewProps = Readonly<{
  channel: GuideChannelRow;
  programme?: GuideProgram;
  now: number;
  timezone: string;
  snapshot: ProgrammeSnapshot;
  choice: SeriesChoice;
  seriesOpen: boolean;
  reminders: readonly string[];
  onClose: () => void;
  onWatch?: () => void;
  onRecord?: () => void;
  onCancelRecording?: () => void;
  onRecordSeries?: () => void;
  onFavorite?: () => void;
  onSeriesOpenChange: (open: boolean) => void;
  onChoice: (patch: Partial<SeriesChoice>) => void;
  onReminder?: (airing: PreviewAiring) => void;
  onProgrammeReminder?: () => void;
  onViewRecordings?: () => void;
}>;

/** The drawn programme panel (desktop-live.html#live-sheet). Presentation only: every
 * role, value and action is a real fact the bound wrapper supplies. */
export function ProgrammePanelView({channel, programme, now, timezone, snapshot, choice, seriesOpen,
  reminders, onClose, onWatch, onRecord, onCancelRecording, onRecordSeries, onFavorite, onSeriesOpenChange, onChoice, onReminder, onProgrammeReminder, onViewRecordings}: ProgrammePanelViewProps) {
  const [cancelOpen, setCancelOpen] = useState(false);
  const [recordingOpen, setRecordingOpen] = useState(false);
  const mark = channelMark(channel), tint = channelTint(mark.seed);
  const airing = !!programme && programme.start <= now && programme.end > now;
  const ended = !!programme && programme.end <= now;
  const future = !!programme && programme.start > now;
  const facts = programme ? programmeFactParts({startMs: programme.start, endMs: programme.end,
    episode: programme.episode ? {mode: 'seasonal', season: programme.episode.season, episode: programme.episode.number} : undefined,
    subtitle: programme.subtitle, categories: programme.categories, year: programme.year, rating: programme.rating, flags: programme.flags,
    recording: snapshot.recording !== undefined}, {t, locale: defaultI18n.region.formatLocale, timeZone: timezone}) : [];
  const range = facts.find(part => part.id === 'time-range')?.text;
  const categories = programme?.categories?.join(', ');
  const episodeInfo = programme?.episode;
  const episodeNumber = episodeInfo?.number ?? (episodeInfo as {number?: number; episode?: number} | undefined)?.episode;
  const episode = episodeInfo?.display ?? (episodeInfo?.season !== undefined && episodeNumber !== undefined
    ? t('downloads.title.episode', {season: episodeInfo.season, episode: episodeNumber}) : undefined);
  const canRecord = !!programme && channel.recordAvailable && !ended;
  const started = programme ? Math.max(0, Math.round((now - programme.start) / 60_000)) : 0;
  const left = programme ? Math.max(0, Math.round((programme.end - now) / 60_000)) : 0;
  const progress = programme && programme.end > programme.start ? Math.min(1, Math.max(0, (now - programme.start) / (programme.end - programme.start))) : 0;
  const watchLabel = airing ? t('guide.watchLive') : t('guide.watchChannel');
  const titleMatch = programme?.seriesId === undefined || programme?.seriesId === '';
  const recordingFact = snapshot.recording !== undefined || (programme?.recording !== undefined && snapshot.recordingRemoved !== true);
  const notice = snapshot.notice === 'recorded' ? t('guide.scheduledBody')
    : snapshot.notice === 'series' ? t('guide.seriesSet')
      : snapshot.notice === 'cancelled' ? t('next.live.recordingCancelled') : undefined;
  const busy = snapshot.busy !== undefined;
  const recordingMenu: readonly MenuEntry[] = [
    {id: 'cancel', label: t('web.dvr.cancelRecording'), icon: 'i-close', destructive: true, onSelect: () => setCancelOpen(true)},
    ...(onViewRecordings ? [{id: 'recordings', label: t('guide.viewRecordings'), icon: 'i-rec' as const, onSelect: onViewRecordings}] : [])];
  return <div className={styles.programmeIn} style={{'--tint': tint} as CSSProperties} data-programme-panel>
    <div className={styles.programmeGlow} aria-hidden="true"/>
    <span className={styles.programmeGlyph} aria-hidden="true">{mark.text.slice(0, 1)}</span>
    <button type="button" className={styles.programmeClose} aria-label={t('next.overlays.close')} onClick={onClose}><Icon name="i-close" size={22}/></button>
    <div className={styles.programmeChannel}>
      <ChannelMark mark={mark} size={34}/>
      <span className={styles.programmeChannelText}>
        <b>{channel.name}{channel.favorite && <Icon className={styles.programmeFavorite} name="i-star-f" size={13}/>}</b>
        <small>{channel.number}{channel.group && <><i className={styles.gap}/>{channel.group}</>}</small>
      </span>
      {airing && <span className={styles.programmeLive}>{t('guide.onNow')}</span>}
    </div>
    <h2 className={styles.programmeTitle}>{programme?.title ?? channel.name}</h2>
    {programme && (episode || programme.subtitle) && <p className={styles.programmeEpisode}>
      {episode}{episode && programme.subtitle && <i className={styles.gap}/>}{programme.subtitle}</p>}
    {programme && <div className={styles.programmeMeta}>
      {range && <strong>{range}</strong>}
      {categories && <span>{categories}</span>}
      {programme.rating && <span className={styles.programmeTag}>{programme.rating}</span>}
      {programme.year && <span>{programme.year}</span>}
      {programme.flags?.live && <span className={`${styles.programmeFlag} ${styles.programmeFlagLive}`}>{t('guide.live')}</span>}
      {programme.flags?.new && <span className={`${styles.programmeFlag} ${styles.programmeFlagNew}`}>{t('guide.new')}</span>}
      {programme.flags?.premiere && <span className={`${styles.programmeFlag} ${styles.programmeFlagPremiere}`}>{t('guide.premiere')}</span>}
    </div>}
    {airing && <div className={styles.programmeProgress}>
      <div className={styles.programmeTrack} aria-hidden="true"><i style={{width: `${(progress * 100).toFixed(2)}%`}}/></div>
      <span>{t('guide.startedLeft', {started, left})}</span>
    </div>}
    <p className={styles.programmeDescription}>{programme?.description || (programme ? t('guide.noDescription') : t('guide.noInformation'))}</p>
    {snapshot.error && <Banner className={styles.programmeBanner}>{snapshot.error}</Banner>}
    {notice && <Banner className={styles.programmeBanner}>{notice}</Banner>}
    <div className={styles.programmeActions}>
      {onWatch && !future && <Button className={styles.programmeAction} variant="primary" icon="i-play" onClick={onWatch} disabled={!channel.tuneAvailable}>{watchLabel}</Button>}
      {canRecord && (recordingFact
        ? <Menu open={recordingOpen} onOpenChange={open => setRecordingOpen(open)} trigger={<Button className={styles.programmeAction} variant="glass" aria-label={t('next.live.recordingOptions')} aria-haspopup="menu" disabled={busy || !snapshot.recording}><span className={styles.recordingDot} aria-hidden="true"/>{t('next.kit.recording')}</Button>}
          title={t('next.live.recordingOptions')} items={recordingMenu}/>
        : <Button className={styles.programmeAction} variant={future ? "primary" : "glass"} icon="i-rec" onClick={onRecord} disabled={busy} loading={snapshot.busy === 'record'}>{t('guide.record')}</Button>)}
      {canRecord && <Button className={`${styles.programmeAction} ${seriesOpen ? styles.programmeActionOpen : ''}`} variant="glass"
        icon="i-series" aria-expanded={seriesOpen} disabled={busy} onClick={() => onSeriesOpenChange(!seriesOpen)}>{t('guide.recordSeries')}</Button>}
      {future && onProgrammeReminder && <Button className={styles.programmeAction} variant="glass" icon={reminders.includes(programme.id) ? 'i-bell-f' : 'i-bell'}
        aria-pressed={reminders.includes(programme.id)} onClick={onProgrammeReminder}>{t(reminders.includes(programme.id) ? 'guide.stopRemind' : 'guide.remind')}</Button>}
      {onFavorite && <Button className={`${styles.programmeAction} ${styles.programmeFavoriteAction}`} variant="glass" iconOnly icon={snapshot.favorite ? 'i-star-f' : 'i-star'}
        aria-label={snapshot.favorite ? t('channels.removeFavorite') : t('channels.addFavorite')} aria-pressed={snapshot.favorite} disabled={busy} onClick={onFavorite}/>}
    </div>
    {future && onProgrammeReminder && <p className={styles.airingsNote}>{t('guide.reminderSet')}</p>}
    {seriesOpen && canRecord && <div className={styles.seriesBox} role="group" aria-label={t('guide.recordSeriesTitle')}>
      <div className={styles.seriesHead}><b>{t('guide.recordSeriesTitle')}</b>
        <span>{snapshot.preview ? t('next.live.matchCount', {count: snapshot.preview.matches}) : snapshot.previewLoading ? t('recordings.loading') : ''}</span></div>
      <Segmented className={styles.seriesSegment} aria-label={t('guide.recordSeriesTitle')} value={choice.episodes} disabled={busy}
        onValueChange={value => {if (value === 'new' || value === 'all') onChoice({episodes: value});}}
        items={[{value: 'new', label: t('guide.newOnly'), disabled: !supportsNewEpisodes(programme)}, {value: 'all', label: t('guide.allEpisodes')}]}/>
      <Segmented className={styles.seriesSegment} aria-label={t('guide.channelColumn')} value={choice.anyChannel ? 'any' : 'this'} disabled={busy}
        onValueChange={value => {if (value === 'this' || value === 'any') onChoice({anyChannel: value === 'any'});}}
        items={[{value: 'this', label: t('guide.thisChannel')}, {value: 'any', label: t('guide.anyChannel'), disabled: titleMatch}]}/>
      <div className={styles.seriesRow}><span>{t('recordings.keepLabel')}</span>
        <Select className={styles.seriesSelect} aria-label={t('recordings.keepLabel')} value={choice.keep} options={keepOptions} disabled={busy} onValueChange={keep => onChoice({keep})}/></div>
      <div className={styles.seriesRow}><span>{t('recordings.startEarly')}</span>
        <Select className={styles.seriesSelect} aria-label={t('recordings.startEarly')} value={String(choice.startEarly)} options={paddingOptions} disabled={busy} onValueChange={value => onChoice({startEarly: Number(value)})}/></div>
      <div className={styles.seriesRow}><span>{t('recordings.endLate')}</span>
        <Select className={styles.seriesSelect} aria-label={t('recordings.endLate')} value={String(choice.endLate)} options={paddingOptions} disabled={busy} onValueChange={value => onChoice({endLate: Number(value)})}/></div>
      <div className={styles.seriesActions}>
        <Button className={styles.seriesAction} variant="primary" size="small" loading={snapshot.busy === 'series'} disabled={busy} onClick={onRecordSeries}>{t('guide.recordSeriesTitle')}</Button>
        <Button className={styles.seriesAction} variant="ghost" size="small" disabled={busy} onClick={() => onSeriesOpenChange(false)}>{t('action.cancel')}</Button>
      </div>
    </div>}
    {snapshot.preview && snapshot.preview.airings.length > 0 && <section className={styles.airings} aria-label={t('next.live.moreAirings')}>
      <h3 className={styles.airingsHead}>{t('next.live.moreAirings')}<span>{t('next.live.airingsOn', {title: programme?.title ?? channel.name, channel: channel.name, day: snapshot.preview.airings[0]?.day ?? t('guide.today')})}</span></h3>
      {snapshot.preview.airings.map(item => {
        const on = reminders.includes(item.id);
        return <div className={styles.airingRow} key={item.id}>
          <span className={styles.airingAt}>{item.time}</span>
          <div><b>{item.episode}{item.episode && <i className={styles.gap}/>}{item.title}</b><small>{item.range}{item.day !== t('guide.today') && <><i className={styles.gap}/>{item.day}</>}</small></div>
          <Button className={styles.airingRemind} variant={on ? 'glass' : 'ghost'} size="small" iconOnly icon={on ? 'i-bell-f' : 'i-bell'}
            aria-label={on ? t('guide.stopRemind') : t('guide.remind')} aria-pressed={on} onClick={() => onReminder?.(item)}/>
        </div>;
      })}
      <p className={styles.airingsNote}>{t('guide.reminderSet')}</p>
    </section>}
    <Dialog open={cancelOpen} onOpenChange={setCancelOpen} title={t('recordings.cancelTitle', {title: programme?.title ?? channel.name})}
      description={t('recordings.cancelNotRecorded')} width={440}
      footer={<><Button variant="glass" onClick={() => setCancelOpen(false)}>{t('action.cancel')}</Button>
        <Button variant="ghost" loading={snapshot.busy === 'cancel'} onClick={() => {setCancelOpen(false);onCancelRecording?.();}}>{t('recordings.cancelAction')}</Button></>}>{null}</Dialog>
  </div>;
}

/** The mounted programme panel. It owns this selection's captured DVR scope;
 * closing it disposes every read and command that belongs to that selection.
 * A layout-owned lease creates those bindings fresh after Strict effect replay,
 * and the panel is drawn only for the lease that is still committed. */
type ProgrammePanelProps = Readonly<{
  context: ScreenContext; admission: SelectionAdmission; channel: GuideChannelRow; programme?: GuideProgram; now: number; timezone: string;
  open: boolean; onClose: () => void; onWatch?: () => void; onChanged?: () => void; onViewRecordings?: () => void;
}>;
type ProgrammeLease = {id: string; context: ScreenContext; admission: SelectionAdmission; channel: GuideChannelRow; programme?: GuideProgram;
  timezone: string; onChanged?: () => void; live: boolean; binding: ReturnType<typeof createProgrammeBindings>};
export function ProgrammePanel(props: ProgrammePanelProps) {
  const {context, admission, channel, programme, timezone, onChanged} = props;
  const [published, publish] = useState<ProgrammeLease>();
  useLayoutEffect(() => {
    // Every command belongs to this exact selected row as well as this page.
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: () => context.isCurrent() && admission.row === channel && admission.current()});
    const binding = createProgrammeBindings(screen, channel, programme, {timezone,
      locale: defaultI18n.region.formatLocale, onChanged});
    const lease: ProgrammeLease = {id: crypto.randomUUID(), context, admission, channel, programme, timezone, onChanged, live: true, binding};
    publish(lease);
    void binding.load().catch(() => {});
    return () => {lease.live = false;binding.dispose();};
  }, [context, admission, channel, programme, timezone, onChanged]);
  const current = published?.live && published.context === context && published.admission === admission && published.channel === channel && published.programme === programme
    && published.timezone === timezone && published.onChanged === onChanged && admission.row === channel && context.isCurrent() ? published : undefined;
  return current ? <CommittedProgrammePanel key={current.id} {...props} lease={current}/> : null;
}
function CommittedProgrammePanel({context, admission, channel, programme, now, timezone, open, onClose, onWatch, onViewRecordings, lease}:
  ProgrammePanelProps & {lease: ProgrammeLease}) {
  const binding = lease.binding, width = useShellWidth(), height = useWindowHeight();
  const portrait = width < 600 && height >= width, landscape = height < 600 && width > height;
  const snapshot = useSyncExternalStore(binding.subscribe, binding.getSnapshot, binding.getSnapshot);
  const reminders = useSyncExternalStore(subscribeReminders, reminderIds, reminderIds);
  const [seriesOpen, setSeriesOpen] = useState(false);
  const [choice, setChoice] = useState<SeriesChoice>(() => ({...defaultSeriesChoice, episodes: supportsNewEpisodes(programme) ? 'new' : 'all'}));
  const previewTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const change = (patch: Partial<SeriesChoice>) => {
    const next = {...choice, ...patch};
    setChoice(next);
    clearTimeout(previewTimer.current);
    previewTimer.current = setTimeout(() => {void binding.preview(next).catch(() => {});}, 250);
  };
  useEffect(() => () => clearTimeout(previewTimer.current), []);
  const openSeries = (value: boolean) => {
    setSeriesOpen(value);
    if (value) void binding.preview(choice).catch(() => {});
  };
  // Reminders are direct local writes; a retired opening or row admission
  // must not write them from a retained panel.
  const canWrite = () => lease.live && context.isCurrent() && admission.row === channel && admission.current();
  const reminder = (airing: PreviewAiring) => {
    if (!canWrite()) return;
    toggleReminder({programId: airing.id, channelId: airing.channelId, title: airing.title, channelName: channel.name, start: airing.start});
  };
  const view = <ProgrammePanelView channel={channel} {...(programme ? {programme} : {})} now={now} timezone={timezone} snapshot={snapshot}
    choice={choice} seriesOpen={seriesOpen} reminders={JSON.parse(reminders) as string[]}
    onClose={onClose} {...(onWatch ? {onWatch} : {})} onRecord={() => {void binding.record();}}
    onCancelRecording={() => {void binding.cancel();}} onRecordSeries={() => {void binding.recordSeries(choice).then(ok => {if (ok) setSeriesOpen(false);});}}
    onFavorite={() => {void binding.toggleFavorite();}} onSeriesOpenChange={openSeries} onChoice={change} onReminder={reminder}
    {...(programme ? {onProgrammeReminder: () => {if (!canWrite()) return; toggleReminder({programId: programme.id, channelId: channel.id, title: programme.title, channelName: channel.name, start: programme.start});}} : {})}
    {...(onViewRecordings ? {onViewRecordings} : {})}/>;
  return portrait ? <BottomSheet open={open} onOpenChange={value => {if (!value) onClose();}} fitContent showCloseButton={false}
    className={styles.programmeSheet} headerClassName={styles.programmeSheetHeader} bodyClassName={styles.programmeSheetBody}
    accessibilityLabel={programme?.title ?? channel.name} title={programme?.title ?? channel.name}>{view}</BottomSheet>
    : <SidePanel open={open} onOpenChange={value => {if (!value) onClose();}} width={landscape ? 376 : Math.min(600, width)} topInset={0}
      className={`${styles.programmePanel} ${landscape ? styles.programmeLandscape : ''}`} accessibilityLabel={programme?.title ?? channel.name} title={programme?.title ?? channel.name}>{view}</SidePanel>;
}

export type {ProgrammeSnapshot, SeriesChoice};
