import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {FactPart} from '@core/presentation/kit-model';
import type {SegmentKind, SegmentReviewEntry, SegmentReviewMarker, SegmentTitleReview, SegmentTitleSummary} from '@core/segment-review.ts';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {CreateMarkerSheet, SegmentWindows, StatTiles, type SegmentEdit, type SegmentMark} from '../../../kit/shell';
import {Dialog} from '../../../kit/overlays/Dialog';
import {Menu} from '../../../kit/overlays/Popover';
import {usePhone} from '../../../kit/overlays/shared';
import {ToastHost, type ToastData} from '../../../kit/overlays';
import type {SegmentIntrosSnapshot, SegmentIntrosSource} from './source';
import styles from './server-intros.module.css';

const t = defaultI18n.t;
const seconds = (us: string): number => {const value = Number(us); return Number.isFinite(value) && value > 0 ? value / 1e6 : 0;};
const isOpening = (kind: SegmentKind) => kind === 'intro' || kind === 'recap' || kind === 'commercial';
const windowKind = (kind: SegmentKind): SegmentMark['kind'] => kind === 'credits' || kind === 'outro' ? 'credits' : kind === 'recap' || kind === 'commercial' ? 'recap' : 'intro';
const waiting = (marker: SegmentReviewMarker) => !marker.approved && !marker.edited && marker.basis !== 'owner';
const trustText = (marker: SegmentReviewMarker) => t('web.segments.trust', {trust: marker.approved ? 'approved' : !marker.viewerVisible ? 'hidden' : marker.automaticSafe ? 'automatic' : 'suggested'});
const mark = (marker: SegmentReviewMarker): SegmentMark => {
  const facts: FactPart[] = [];
  if (marker.basis !== 'owner') facts.push({id: 'confidence', text: t('web.segments.confidence', {percent: Math.round(marker.confidence * 100)}), role: 'supplement'});
  facts.push({id: 'basis', text: t('web.segments.basis', {basis: marker.basis}), role: 'context'});
  return {id: marker.id, kind: windowKind(marker.kind), label: t('web.segments.kind', {kind: marker.kind}), start: seconds(marker.startUS), end: seconds(marker.endUS),
    suggested: waiting(marker), trust: trustText(marker), trustTone: marker.approved || !marker.viewerVisible ? 'neutral' : marker.automaticSafe ? 'automatic' : 'suggested', facts};
};
const titleLine = (title: SegmentTitleSummary) => [
  t('web.segments.titleFiles', {measured: title.files.complete, total: title.files.complete + title.files.pending + title.files.failed + title.files.unsupported}),
  title.markers.suggested ? t('web.segments.titleSuggested', {count: title.markers.suggested}) : '',
  title.markers.automatic ? t('web.segments.titleAutomatic', {count: title.markers.automatic}) : '',
  title.markers.approved ? t('web.segments.titleApproved', {count: title.markers.approved}) : '',
].filter(Boolean).join(' · ');

/** The owner's review under the drawn mode row and progress line: the status
 * the server reports, each video library, its shows and films, and one title's
 * markers with the drawn zoomed windows. Every number is the server's own. */
/** One opened marker editor. The token makes a reopened marker in the same
 * episode a different opening; Review and Episode carry this exact shape. */
type EditOpening = Readonly<{opening: number; entryId: string; marker: SegmentReviewMarker; start: number; end: number; kind: SegmentKind}>;
/** One Add a marker opening: the exact row and library captured when the menu
 * opened. A replacement row or context retires it. */
type CreateOpening = Readonly<{opening: number; entry: SegmentReviewEntry; libraryId: string}>;
type CreateOp = Readonly<{opening: CreateOpening; source: SegmentIntrosSource}>;
type DetectOpening = Readonly<{title: SegmentTitleReview; source: SegmentIntrosSource}>;
/** The exact review the Detect opener belongs to. A retained opener from another
 * source, title, library or season refuses before creating a successor. */
type ReviewAdmission = Readonly<{source: SegmentIntrosSource; title: SegmentTitleReview; libraryId: string; titleId: string; season: number | undefined}>;

export function ServerIntros({source, modeRows, onPreview}: {source: SegmentIntrosSource; modeRows: ReactNode; onPreview: (libraryId: string, itemId: string, start: number) => void}) {
  const snapshot = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const opening = useRef(0);
  const committed = useRef<EditOpening | undefined>(undefined);
  const [edit, setEdit] = useState<EditOpening>();
  const [creating, setCreating] = useState<CreateOpening>();
  const createCommitted = useRef<CreateOpening | undefined>(undefined);
  const createOp = useRef<CreateOp | undefined>(undefined);
  useLayoutEffect(() => () => {createOp.current = undefined;}, []);
  const createOpening = useRef(0);
  const [confirmingDetect, setConfirmingDetect] = useState<DetectOpening>();
  const [toast, setToast] = useState<ToastData | null>(null);
  const toastSequence = useRef(0);
  const admittedReview = useRef<ReviewAdmission | undefined>(undefined);
  const [review, setReview] = useState<ReviewAdmission | undefined>(undefined);
  useLayoutEffect(() => {
    const title = snapshot.title;
    if (!title || !snapshot.titleId || !snapshot.libraryId) {admittedReview.current = undefined;setReview(undefined);return;}
    const next: ReviewAdmission = {source, title, libraryId: snapshot.libraryId, titleId: snapshot.titleId, season: snapshot.season};
    admittedReview.current = next;setReview(next);
    return () => {if (admittedReview.current === next) admittedReview.current = undefined;};
  }, [source, snapshot.libraryId, snapshot.titleId, snapshot.season, snapshot.title]);
  const detectCommitted = useRef<DetectOpening | undefined>(undefined);
  useLayoutEffect(() => {
    detectCommitted.current = confirmingDetect;
    return () => {if (detectCommitted.current === confirmingDetect) detectCommitted.current = undefined;};
  }, [confirmingDetect]);
  useLayoutEffect(() => {
    createCommitted.current = creating;
    return () => {if (createCommitted.current === creating) createCommitted.current = undefined;};
  }, [creating]);
  useLayoutEffect(() => {
    committed.current = edit;
    return () => {if (committed.current === edit) committed.current = undefined;};
  }, [edit]);
  useEffect(() => {void source.load();}, [source]);
  // The drawn page starts on the first real video library; the owner can switch.
  useEffect(() => {
    const first = snapshot.status?.libraries[0];
    if (first && !snapshot.libraryId) void source.openLibrary(first.libraryId);
  }, [source, snapshot.status, snapshot.libraryId]);
  useLayoutEffect(() => {committed.current = undefined;setEdit(undefined);createCommitted.current = undefined;createOp.current = undefined;setCreating(undefined);detectCommitted.current = undefined;setConfirmingDetect(undefined);}, [source, snapshot.libraryId, snapshot.titleId, snapshot.season]);
  useLayoutEffect(() => {
    const open = createCommitted.current;
    if (!open) return;
    const title = snapshot.title;
    if (title && title.id === snapshot.titleId && title.seasons.some(season => season.entries.some(value => value === open.entry))) return;
    createCommitted.current = undefined;setCreating(undefined);
  }, [snapshot.title, snapshot.titleId, creating]);
  useLayoutEffect(() => {
    const open = detectCommitted.current;
    if (open && snapshot.title !== open.title) {detectCommitted.current = undefined;setConfirmingDetect(undefined);}
  }, [snapshot.title, confirmingDetect]);
  const status = snapshot.status;
  const stateText = !status ? '' : status.mode === 'off' ? t('web.segments.stateOff') : status.files.pending || status.showsWaiting || !status.catchUpComplete ? t('web.segments.stateWorking') : t('web.segments.stateDone');
  const busy = !!snapshot.busy;
  useEffect(() => {if (snapshot.note) setToast({id: `segments-note-${++toastSequence.current}`, message: snapshot.note, tone: 'success'});}, [snapshot.note]);
  const title = snapshot.title;
  const season = title?.kind === 'show' ? title.seasons.find(value => value.number === snapshot.season) ?? title.seasons[0] : undefined;
  const entries = title ? title.kind === 'show' ? season?.entries ?? [] : title.seasons.flatMap(value => value.entries) : [];
  return <>
    {modeRows}
    {snapshot.phase === 'error' && !status
      ? <ReviewNotice tone="bad" onRetry={() => {void source.load();}}>{t('web.segments.error')}</ReviewNotice>
      : status ? <StatTiles variant="segments" items={[
        {id: 'measured', label: t('web.segments.measured'), value: defaultI18n.number(status.files.complete)},
        {id: 'waiting', label: t('web.segments.waiting'), value: defaultI18n.number(status.files.pending)},
        {id: 'unreadable', label: t('web.segments.unreadable'), value: defaultI18n.number(status.files.failed + status.files.unsupported)},
        {id: 'status', label: t('web.segments.status'), value: stateText},
      ]}/> : <p className={styles.loading} role="status">{t('web.segments.loading')}</p>}

    <section className={styles.review} id="segments.review" aria-labelledby="segments-review-title">
      <header className={styles.reviewHead}>
        <h3 id="segments-review-title">{t('web.segments.review')}</h3>
        <div className={styles.legend} aria-hidden="true">
          <span data-kind="intro">{t('web.segments.kind', {kind: 'intro'})}</span>
          <span data-kind="recap">{t('web.segments.kind', {kind: 'recap'})}</span>
          <span data-kind="credits">{t('web.segments.kind', {kind: 'credits'})}</span>
          <span data-kind="waiting">{t('web.segments.waitingReview')}</span>
        </div>
      </header>
      {!status?.libraries.length ? <p className={styles.empty}>{t('web.segments.noLibraries')}</p> : <div className={styles.columns}>
        <div className={styles.shows} id="segments.libraries">
          <div className={styles.libraryTabs} role="group" aria-label={t('web.segments.libraries')}>
            {status.libraries.map(library => <button key={library.libraryId} type="button" aria-pressed={snapshot.libraryId === library.libraryId}
              className={styles.libraryTab} onClick={() => {void source.openLibrary(library.libraryId);}}>
              <b>{library.name}</b>
              <small>{t(library.itemKind === 'movie' ? 'web.segments.libraryModeFilms' : 'web.segments.libraryMode', {mode: library.mode})}{library.choice === 'server' ? ` · ${t('web.segments.followsServer')}` : ''}</small>
            </button>)}
          </div>
          {snapshot.titlesLoading && !snapshot.titles.length ? <p className={styles.loading} role="status">{t('web.segments.loading')}</p> : null}
          {snapshot.titlesError ? <ReviewNotice tone="bad">{t('web.segments.error')}</ReviewNotice> : null}
          {!snapshot.titlesLoading && status && snapshot.libraryId && !snapshot.titles.length ? <p className={styles.empty}>{t('web.segments.noTitles')}</p> : null}
          {snapshot.titles.map(value => <button key={value.id} type="button" className={styles.show} aria-current={snapshot.titleId === value.id || undefined} onClick={() => {void source.openTitle(value.id);}}>
            <b>{value.title || t('web.segments.untitled')}</b>
            <small>{titleLine(value)}</small>
            {value.markers.suggested ? <span className={styles.todo}>{t('web.segments.toReview', {count: value.markers.suggested})}</span> : null}
          </button>)}
          {snapshot.titlesCursor ? <Button variant="ghost" size="small" onClick={() => {void source.moreTitles();}} disabled={snapshot.titlesLoading}>{t('web.segments.showMore')}</Button> : null}
        </div>

        <div className={styles.pane}>
          {!title ? snapshot.titleLoading ? <p className={styles.loading} role="status">{t('web.segments.loading')}</p> : snapshot.titleError ? <ReviewNotice tone="bad" onRetry={() => {if (snapshot.titleId) void source.openTitle(snapshot.titleId);}}>{t('web.segments.error')}</ReviewNotice> : <p className={styles.empty}>{t('web.segments.chooseTitle')}</p> : <Review title={title} season={snapshot.season} entries={entries} snapshot={snapshot} edit={edit}
            onSeason={value => source.selectSeason(value)} onApproveAll={() => {void source.approve();}} onDetect={() => {
              const admission = review;
              if (!admission || admittedReview.current !== admission || detectCommitted.current) return;
              const title = snapshot.title;
              if (!title || title !== admission.title || snapshot.titleId !== admission.titleId || snapshot.season !== admission.season) return;
              setConfirmingDetect({title, source: admission.source});
            }}
            onRetryTitle={() => {if (snapshot.titleId) void source.openTitle(snapshot.titleId);}}
            onEdit={(entry, marker) => {const next: EditOpening = {opening: ++opening.current, entryId: entry.itemId, marker, start: seconds(marker.startUS), end: seconds(marker.endUS), kind: marker.kind};committed.current = next;setEdit(next);}}
            onChange={(entry, value) => {const open = committed.current;if (!open || open.opening !== value.opening || open.marker.id !== value.id || open.entryId !== entry.itemId) return;setEdit(current => current && current.opening === value.opening ? {...current, start: value.start, end: value.end, kind: value.kind} : current);}}
            onSave={(entry, draft) => {const open = committed.current;if (!open || open.opening !== draft.opening || open.marker.id !== draft.id || open.entryId !== entry.itemId) return;committed.current = undefined;setEdit(undefined);void source.saveMarker(entry, open.marker, draft.kind, String(Math.round(draft.start * 1e6)), String(Math.round(draft.end * 1e6)));}}
            onCancel={token => {const open = committed.current;if (!open || open.opening !== token) return;committed.current = undefined;setEdit(undefined);}}
            onApprove={(entry, marker) => {void source.approveMarker(entry, marker);}}
            onDismiss={(entry, marker) => {void source.dismissMarker(entry, marker);}}
            onAdd={entry => {const currentTitle = snapshot.title;if (!currentTitle || currentTitle.id !== snapshot.titleId) return;const open: CreateOpening = {opening: ++createOpening.current, entry, libraryId: currentTitle.libraryId};createCommitted.current = open;setCreating(open);}}
            busy={busy} onPreview={onPreview} />}
        </div>
      </div>}
    </section>
    <Dialog open={!!confirmingDetect} onOpenChange={next => {if (!next) {const open = detectCommitted.current;if (open && open === confirmingDetect) {detectCommitted.current = undefined;setConfirmingDetect(undefined);}}}} size="sm" role="alertdialog" title={t('web.segments.detectAgain')} description={t('web.segments.detectAgainHelp')}
      footer={<><Button variant="glass" size="compact" disabled={busy} onClick={() => {const open = detectCommitted.current;if (!open || open !== confirmingDetect) return;detectCommitted.current = undefined;setConfirmingDetect(undefined);}}>{t('next.shell.cancel')}</Button>
        <Button variant="primary" size="compact" disabled={busy} onClick={() => {const open = detectCommitted.current;if (!open || open !== confirmingDetect) return;detectCommitted.current = undefined;setConfirmingDetect(undefined);void open.source.detectAgain(open.title);}}>{t('web.segments.detectAgain')}</Button></>}>{null}</Dialog>
    <ToastHost toast={toast} onDismiss={() => setToast(null)} />
    {creating ? <CreateMarkerSheet key={creating.opening} duration={seconds(creating.entry.durationUS)} busy={busy}
      onCancel={() => {const open = createCommitted.current;if (!open || open.opening !== creating.opening) return;createCommitted.current = undefined;setCreating(undefined);}}
      onSave={draft => {
        const open = createCommitted.current;
        if (!open || open.opening !== creating.opening || createOp.current) return;
        const currentTitle = snapshot.title;
        if (!currentTitle || currentTitle.id !== snapshot.titleId || !currentTitle.seasons.some(season => season.entries.some(value => value === open.entry))) return;
        const op: CreateOp = {opening: open, source};
        createOp.current = op;
        void source.createMarker(open.entry, draft.kind, draft.startUS, draft.endUS).then(saved => {
          if (createOp.current !== op) return;
          createOp.current = undefined;
          if (!saved || createCommitted.current !== open) return;
          createCommitted.current = undefined;setCreating(undefined);
        }).catch(() => {if (createOp.current === op) createOp.current = undefined;});
      }}
      onPreviewAt={start => {
        const open = createCommitted.current;
        if (!open || open.opening !== creating.opening) return;
        const currentTitle = snapshot.title;
        if (!currentTitle || currentTitle.id !== snapshot.titleId || !currentTitle.seasons.some(season => season.entries.some(value => value === open.entry))) return;
        const duration = seconds(open.entry.durationUS);
        if (!Number.isFinite(start) || duration <= 0 || start < 0 || start > duration) return;
        onPreview(open.libraryId, open.entry.itemId, start);
      }} /> : null}
  </>;
}

function Review({title, season, entries, snapshot, edit, onSeason, onApproveAll, onDetect, onRetryTitle, onEdit, onChange, onSave, onCancel, onApprove, onDismiss, onAdd, busy, onPreview}: {
  title: SegmentTitleReview; season?: number; entries: readonly SegmentReviewEntry[]; snapshot: SegmentIntrosSnapshot;
    onSeason: (value: number) => void; onApproveAll: () => void; onDetect: () => void; onRetryTitle: () => void;
  onEdit: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => void;
  onChange: (entry: SegmentReviewEntry, value: SegmentEdit) => void;
  onSave: (entry: SegmentReviewEntry, draft: SegmentEdit) => void; onCancel: (opening: number) => void;
  onApprove: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => void; onDismiss: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => void; onAdd: (entry: SegmentReviewEntry) => void; busy: boolean; edit?: EditOpening;
  onPreview: (libraryId: string, itemId: string, start: number) => void;
}) {
  return <>
    <header className={styles.reviewTitle}>
      <div><b>{title.title}</b>{title.kind === 'show' && <label className={styles.season}>{t('web.segments.season', {number: season ?? 1})}
        <select aria-label={t('web.segments.season', {number: season ?? 1})} value={season ?? title.seasons[0]?.number} onChange={event => onSeason(Number(event.currentTarget.value))}>
          {title.seasons.map(value => <option key={value.number} value={value.number}>{value.title || t('web.segments.season', {number: value.number})}</option>)}
        </select>
        <Icon name="i-down" size={14} />
      </label>}</div>
      <div className={styles.actions}>
        <Button variant="glass" size="small" icon="i-check" onClick={onApproveAll} disabled={busy}>{t('web.segments.approveSeason')}</Button>
        <Button variant="ghost" size="small" icon="i-refresh" onClick={onDetect} disabled={busy}>{t('web.segments.detectAgain')}</Button>
      </div>
    </header>
    {snapshot.actionError ? <ReviewNotice tone="bad">{t('web.segments.actionError')}</ReviewNotice> : null}
    {snapshot.titleLoading ? <p className={styles.loading} role="status">{t('web.segments.loading')}</p> : null}
    {snapshot.titleError ? <ReviewNotice tone="bad" onRetry={onRetryTitle}>{t('web.segments.error')}</ReviewNotice> : null}
    {!snapshot.titleLoading && !entries.length ? <p className={styles.empty}>{t('web.segments.entryState', {state: 'complete'})}</p> : null}
    {entries.map(entry => <Episode key={entry.itemId} entry={entry} title={title} edit={edit?.entryId === entry.itemId ? edit : undefined} busy={busy}
      onEdit={onEdit} onChange={onChange} onSave={onSave} onCancel={onCancel} onApprove={onApprove} onDismiss={onDismiss} onAdd={onAdd} onPreview={onPreview} />)}
  </>;
}

/** The episode's own menu captures the exact row and Add callback it opened
 * with; a replacement retires it in layout, and Add claims synchronously. */
function EpisodeMenu({entry, title, onAdd, t = defaultI18n.t}: Readonly<{
  entry: SegmentReviewEntry; title: string; onAdd: (entry: SegmentReviewEntry) => void; t?: typeof defaultI18n.t;
}>) {
  type Admission = Readonly<{entry: SegmentReviewEntry; onAdd: (entry: SegmentReviewEntry) => void}>;
  const admitted = useRef<Admission | undefined>(undefined);
  const [admission, setAdmission] = useState<Admission | undefined>(undefined);
  const live = useRef<{admission: Admission} | undefined>(undefined);
  const [opening, setOpening] = useState<{admission: Admission} | undefined>(undefined);
  useLayoutEffect(() => {
    const current = admitted.current;
    if (current && current.entry === entry && current.onAdd === onAdd) return;
    const next: Admission = {entry, onAdd};
    admitted.current = next;setAdmission(next);
    if (live.current && live.current.admission !== next) {live.current = undefined;setOpening(undefined);}
    return () => {
      if (admitted.current === next) admitted.current = undefined;
      if (live.current?.admission === next) {live.current = undefined;setOpening(undefined);}
    };
  }, [entry, onAdd]);
  const retire = (expected: {admission: Admission}) => {if (live.current !== expected) return;live.current = undefined;setOpening(undefined);};
  const changeOpen = (next: boolean) => {
    if (!next) {if (opening) retire(opening);return;}
    if (opening || live.current) return;
    if (!admission || admitted.current !== admission) return;
    const lease = {admission};live.current = lease;setOpening(lease);
  };
  const run = () => {
    const current = live.current;
    if (!current || opening !== current) return;
    live.current = undefined;setOpening(undefined);
    current.admission.onAdd(current.admission.entry);
  };
  return <Menu open={opening !== undefined} onOpenChange={changeOpen} title={title} trigger={<Button variant="icon" icon="i-more" iconOnly aria-label={t('next.shell.markerMenu')} />}
    items={[{id: 'add', label: t('web.segments.addTitle'), onSelect: run}]} />;
}

function Episode({entry, title, edit, busy, onEdit, onChange, onSave, onCancel, onApprove, onDismiss, onAdd, onPreview}: {
  entry: SegmentReviewEntry; title: SegmentTitleReview; edit?: EditOpening; busy: boolean;
  onEdit: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => void;
  onChange: (entry: SegmentReviewEntry, value: SegmentEdit) => void;
  onSave: (entry: SegmentReviewEntry, draft: SegmentEdit) => void; onCancel: (opening: number) => void;
  onApprove: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => void; onDismiss: (entry: SegmentReviewEntry, marker: SegmentReviewMarker) => void; onAdd: (entry: SegmentReviewEntry) => void;
  onPreview: (libraryId: string, itemId: string, start: number) => void;
}) {
  const duration = seconds(entry.durationUS), phone = usePhone();
  const opening = entry.markers.filter(marker => isOpening(marker.kind)).map(mark);
  const ending = entry.markers.filter(marker => !isOpening(marker.kind)).map(mark);
  const todo = entry.markers.filter(waiting).length;
  return <article className={styles.episode}>
    <header className={styles.episodeHead}>
      {title.kind === 'show' && <span className={styles.number}>{entry.number}</span>}
      <b>{entry.title || t('web.segments.untitled')}</b>
      {duration > 0 && <span className={styles.duration}>{t('web.segments.durationMinutes', {count: Math.round(duration / 60)})}</span>}
      <span className={styles.state} data-todo={todo > 0 || undefined}>{todo > 0 ? t('web.segments.titleSuggested', {count: todo}) : t('web.segments.entryState', {state: 'complete'})}</span>
      {phone ? <EpisodeMenu entry={entry} title={entry.title || t('web.segments.untitled')} onAdd={onAdd} t={t} /> : null}
    </header>
    {duration > 0 && (opening.length || ending.length)
      ? <SegmentWindows duration={duration} opening={opening} ending={ending} context={entry.title && duration > 0 ? t('web.segments.inEpisode', {title: entry.title, count: Math.round(duration / 60)}) : undefined} edit={edit ? {id: edit.marker.id, opening: edit.opening, start: edit.start, end: edit.end, kind: edit.kind} : undefined}
          onPreview={(_mark, start) => onPreview(title.libraryId, entry.itemId, start)}
          onEdit={value => {const marker = entry.markers.find(candidate => candidate.id === value.id); if (marker) onEdit(entry, marker);}}
          onChange={value => onChange(entry, value)} onSave={draft => onSave(entry, draft)} onCancel={onCancel}
          onApprove={value => {const marker = entry.markers.find(candidate => candidate.id === value.id); if (marker && !busy) onApprove(entry, marker);}}
          onDismiss={value => {const marker = entry.markers.find(candidate => candidate.id === value.id); if (marker && !busy) onDismiss(entry, marker);}} />
      : <p className={styles.empty}>{t('web.segments.entryState', {state: entry.state})}</p>}
  </article>;
}

function ReviewNotice({children, tone, onRetry}: {children: ReactNode; tone: 'bad'; onRetry?: () => void}) {
  return <div className={styles.notice} role="alert" data-tone={tone}><Icon name="i-info" size={16}/><span>{children}</span>{onRetry && <button type="button" onClick={onRetry}>{t('action.tryAgain')}</button>}</div>;
}
