import {useCallback, useLayoutEffect, useEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {RadioGroup} from 'radix-ui';
import {defaultI18n, type MessageId} from '@i18n';
import {
  RemoteSourceService, cloudTokenDraft, driveOAuthDraft, driveServiceAccountDraft, s3SourceDraft, sourceReadPreset,
  type CloudTokenFields, type DAVConnectionResult, type DriveKeyFields, type RcloneConfigView, type RcloneProgram, type RcloneTestResult, type RemoteFolder, type RemoteSource, type SavedSourceTest, type S3SourceFields, type RemoteSourceStatus, type RemoteContactErrorCode, type RemoteScanMode, type RemoteScanPhase, type RemoteSourceHealth, type RemoteScanStateCode, type SourceReadPreset, type SourceReadSettings, type SourceReadValues,
} from '@core/remote-sources.ts';
import {LibraryManagementService, type ManagedLibrary} from '@core/library-management.ts';
import {readLibraryInventoryConfig, saveLibraryFolder} from '@core/server-admin/libraries.ts';
import {davConfiguration, stableStorageOperationId, type WebDavDraft} from '@core/server-admin/panel-logic.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {Button, RichSelect, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {usePhone} from '../../../kit/overlays/shared';
import {SettingsGroup, SettingsPanel} from '../../../kit/settings';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import {Icon, type IconName} from '../../../kit/Icon';
import {MetadataFacts} from '../../../kit/MetadataFacts';
import styles from './remote-sources.module.css';
import {DriveKeyPane, DriveModePane, DriveOAuthPane} from './DrivePanes';
import {RemoteFolderPicker} from './RemoteFolderPicker';
import {ProgramPicker} from './ProgramPicker';
import {SourceHeader} from './SourceHeader';
import {SourceWizard} from './SourceWizard';
import {SourceDone} from './SourceDone';
import {SourceReadingDialog, SourceReadingFields} from './SourceReadingDialog';
import {ReadingProfileOpening} from './reading-profile-opening';
import {sourceChoiceComparison} from '@core/remote-source-choice.ts';
import {configuredReadingSource, acceptedReadingSource} from './reading-source-row';
import {useOwnedResource} from './resources';
import {router} from '../../../router';

const t = defaultI18n.t;
const noReadSubscribe = () => () => {};
const noReadState = () => undefined;

/** Read values, including the retired whole-file compatibility wire; the independent analysis consent is its own draft. */
function readValuesWithoutConsent(values: SourceReadValues): SourceReadValues {
  const {allowAdvancedMediaAnalysis, ...read} = values;
  return allowAdvancedMediaAnalysis === undefined ? values : read;
}
const emptyDraft: WebDavDraft = {name: '', root: '', username: '', password: '', insecureLocal: false};
type SourceKind = 'webdav' | 'rclone' | 'config' | 's3' | 'drive' | 'cloud';
type RcloneDraft = {name: string; remote: string; config: string};
type Tested = Readonly<{revision: number; result: RcloneTestResult}>;
const emptyRclone: RcloneDraft = {name: '', remote: '', config: ''};
const emptyS3: S3SourceFields = {endpoint: '', region: '', accessKey: '', secretKey: '', bucket: '', folder: ''};
const emptyDrive: DriveKeyFields = {keyJson: '', location: 'folder', locationId: ''};
const emptyCloud: CloudTokenFields = {provider: 'onedrive', token: '', folder: ''};
/** Step 1 draws three choices: WebDAV, a new rclone remote, or one already in
 * the account's own configuration (remote-sources.html#1, #8a, #14a). */
const whatOptions: readonly {family: SourceFamily; label: MessageId; hint: MessageId; tag?: MessageId}[] = [
  {family: 'webdav', label: 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.0.label', hint: 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.0.hint', tag: 'next.remoteSources.kindCommon'},
  {family: 'rclone', label: 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.1.label', hint: 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.1.hint', tag: 'next.remoteSources.kindIncluded'},
  {family: 'config', label: 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.2.label', hint: 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.2.hint', tag: 'next.remoteSources.kindIncluded'},
];
/** Step 2's kinds, grouped by how each is filled in, and only the kinds
 * Portico can build a form for, plus pasting (remote-sources.html#3d). */
const rcloneKindGroups: readonly {title: MessageId; kinds: readonly {value: SourceKind; label: MessageId; icon: IconName}[]}[] = [
  {title: 'next.remoteSources.groupDetails', kinds: [{value: 's3', label: 'next.remoteSources.connectS3', icon: 'i-storage'}]},
  {title: 'next.remoteSources.groupCloud', kinds: [{value: 'drive', label: 'next.remoteSources.connectDrive', icon: 'i-google'}, {value: 'cloud', label: 'next.remoteSources.connectCloud', icon: 'i-storage'}]},
  {title: 'next.remoteSources.groupAnything', kinds: [{value: 'rclone', label: 'next.remoteSources.connectRclone', icon: 'i-storage'}]},
];
const presetLabels: Readonly<Record<SourceReadPreset, MessageId>> = {
  list: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.0.label',
  once: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.1.label',
  everything: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.2.label',
  ask: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.3.label',
};
const presetLabel = (name: SourceReadPreset): MessageId => presetLabels[name];

/** The drawn words for a source's own active scan (remote status scan, run facts). */
const scanStateWord = (state: RemoteScanStateCode): MessageId => state === 'queued' ? 'next.remoteSources.scanQueued'
  : state === 'paused' ? 'next.remoteSources.scanPaused' : 'next.remoteSources.scanRunning';
const scanModeWord = (mode: RemoteScanMode | undefined): MessageId | undefined => mode === 'first-import' ? 'next.remoteSources.scanModeFirst'
  : mode === 'incremental' ? 'next.remoteSources.scanModeIncremental' : mode === 'integrity' ? 'next.remoteSources.scanModeIntegrity'
    : mode === 'full' ? 'next.remoteSources.scanModeFull' : undefined;
/** The drawn words for the all-agree folder health; absent stays absent. */
/** The closed words for a real provider failure; never prose from the server. */
const contactWord = (code: RemoteContactErrorCode): MessageId => code === 'offline' ? 'next.remoteSources.contactOffline'
  : code === 'credentials_required' ? 'next.remoteSources.contactCredentials' : code === 'rate_limited' ? 'next.remoteSources.contactRateLimited'
    : 'next.remoteSources.contactQuota';
const healthWord = (health: RemoteSourceHealth): MessageId => health === 'ready' ? 'next.remoteSources.healthReady'
  : health === 'healthy' ? 'next.remoteSources.healthHealthy' : health === 'degraded' ? 'next.remoteSources.healthDegraded'
  : health === 'inventory_running' ? 'next.remoteSources.healthInventoryRunning' : health === 'cancelled' ? 'next.remoteSources.healthCancelled'
    : health === 'offline' ? 'next.remoteSources.healthOffline' : health === 'root_changed' ? 'next.remoteSources.healthRootChanged'
      : 'next.remoteSources.healthRemoving';
const scanPhaseWord = (phase: RemoteScanPhase | undefined): MessageId | undefined => phase === 'inventory' ? 'next.remoteSources.scanPhaseInventory'
  : phase === 'legacy_inventory' ? 'next.remoteSources.scanPhaseLegacyInventory' : phase === 'sidecars' ? 'next.remoteSources.scanPhaseSidecars'
  : phase === 'basic_pending' ? 'next.remoteSources.scanPhaseBasicPending' : phase === 'basic_running' ? 'next.remoteSources.scanPhaseBasicRunning'
    : phase === 'analysis_enqueue' ? 'next.remoteSources.scanPhaseAnalysis' : phase === 'reconciling' ? 'next.remoteSources.scanPhaseReconciling'
      : phase === 'verifying' ? 'next.remoteSources.scanPhaseVerifying' : phase === 'extras' ? 'next.remoteSources.scanPhaseExtras' : undefined;

function stateId(source: RemoteSource): MessageId {
  switch (source.errorCode) {
    case 'offline': return 'next.remoteSources.state.unreachable';
    case 'credentials_required': return 'next.remoteSources.state.credentials';
    case 'rate_limited': case 'quota_exceeded': return 'next.remoteSources.state.waiting';
    case 'range_unsupported': return 'next.remoteSources.state.limited';
    case 'invalid_configuration': case 'budget_exceeded': return 'next.remoteSources.state.attention';
    case 'cancelled': return 'next.remoteSources.state.stopped';
    case 'source_changed': case 'cursor_expired': return 'next.remoteSources.state.checking';
  }
  if (source.state === 'ready' || source.state === 'healthy') return 'next.remoteSources.state.ready';
  if (source.state === 'checking' || source.state === 'pending') return 'next.remoteSources.state.checking';
  if (source.state === 'unreachable' || source.state === 'degraded') return 'next.remoteSources.state.unreachable';
  if (source.state === 'credentials_required' || source.state === 'unauthorized') return 'next.remoteSources.state.credentials';
  return 'next.remoteSources.state.problem';
}
function stateTone(source: RemoteSource): 'healthy' | 'warning' | 'danger' | 'neutral' {
  if (source.errorCode === 'offline' || source.errorCode === 'credentials_required' || source.errorCode === 'binary_changed') return 'danger';
  if (source.errorCode === 'rate_limited' || source.errorCode === 'quota_exceeded' || source.errorCode === 'range_unsupported') return 'warning';
  if (source.errorCode) return 'danger';
  return source.state === 'ready' || source.state === 'healthy' ? 'healthy' : 'neutral';
}
function problemId(source: RemoteSource): MessageId | undefined {
  const id = source.errorCode && `next.remoteSources.problem.${source.errorCode}`;
  return id && defaultI18n.has(id) ? id as MessageId : undefined;
}
function SourceStatus({source}: {source: RemoteSource}) {
  return <span className={styles.badge} data-tone={stateTone(source)}>{t(stateId(source))}</span>;
}
export function remoteErrorText(error: unknown): string {
  const code = error && typeof error === 'object' && typeof (error as {code?: unknown}).code === 'string' ? (error as {code: string}).code : '';
  switch (code) {
    case 'remote_credentials_required': case 'source_credentials_required': return t('next.remoteSources.problem.credentials_required');
    case 'remote_invalid_configuration': case 'invalid_request': return t('next.remoteSources.error.invalid');
    case 'remote_range_unsupported': return t('next.remoteSources.problem.range_unsupported');
    case 'remote_rate_limited': return t('next.remoteSources.problem.rate_limited');
    case 'remote_administration_unavailable': case 'administration_unavailable': return t('next.remoteSources.problem.administration_unavailable');
    case 'revision_mismatch': case 'storage_command_conflict': return t('next.remoteSources.error.changed');
    case 'idempotency_key_reused': return t('next.remoteSources.error.reused');
    case 'request_in_progress': return t('next.remoteSources.error.busy');
    case 'unauthorized': return t('next.remoteSources.error.signIn');
    case 'forbidden': case 'not_permitted': return t('next.remoteSources.error.forbidden');
    case 'refresh_failed': return t('next.remoteSources.error.refreshFailed');
    case 'source_folders_unsupported': return t('next.remoteSources.foldersUnsupported');
    default: return t('next.remoteSources.error.generic');
  }
}
function authFailure(error: unknown): boolean {
  const code = error && typeof error === 'object' ? (error as {code?: string}).code : '';
  return code === 'unauthorized' || code === 'forbidden' || code === 'not_permitted' || code === 'remote_credentials_required' || code === 'source_credentials_required';
}
function refreshFailure(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as {code?: string}).code === 'refresh_failed';
}
/** The server's own check time, in the owner's local words. */
function checkedWhen(value: string): string {
  const when = new Date(value);
  if (Number.isNaN(when.getTime())) return value;
  try { return new Intl.DateTimeFormat(undefined, {dateStyle: 'medium', timeStyle: 'short'}).format(when); }
  catch { return when.toISOString(); }
}
/** A folder attachment failure names the one thing the owner can act on: the
 * folders are saved on the source either way, so this never reads as data loss. */
function attachFailure(error: unknown): string {
  const code = error && typeof error === 'object' && typeof (error as {code?: unknown}).code === 'string' ? (error as {code: string}).code : '';
  if (code === 'source_already_configured') return t('next.remoteSources.folderAlreadyAdded');
  if (code === 'library_configuration_conflict' || code === 'revision_mismatch') return t('next.remoteSources.error.changed');
  if (code === 'unauthorized' || code === 'forbidden' || code === 'not_permitted') return t('next.remoteSources.error.forbidden');
  return t('next.remoteSources.attachFailed');
}

/** The wizard step's Program row: what the server's own rclone is, drawn from
 * the same published status the source page reads, with the frame's Check again
 * repeating that read without leaving the step (remote-sources 2/2c). */
function ProgramRow({program, disabled, onCheck}: {program?: RcloneProgram; disabled: boolean; onCheck?: () => void}) {
  return <div className={styles.programRow}>
    <span>{t('next.remoteSources.program')}</span>
    {program && <span role="status">{program.available ? (program.version ? t('next.remoteSources.bundledVersion', {version: program.version}) : t('next.remoteSources.bundledProgram')) : t('next.remoteSources.bundledUnavailable')}</span>}
    {onCheck && <Button size="xs" variant="glass" disabled={disabled} onClick={onCheck}>{t('next.remoteSources.checkAgain')}</Button>}
  </div>;
}

export type RemoteSourcesPageProps = Readonly<{
  api: LibraryContentApi; serverId: string; viewerId: string; isCurrent: () => boolean; reportError: (error: unknown) => void;
  openSection?: (section: string) => void;
}>;

/** Server › Remote sources. The drawn page over the source list, the WebDAV add
 * flow and the source's reading profile; only routes the server actually has. */
export function RemoteSourcesPage(props: RemoteSourcesPageProps) {
  // The page's reader belongs to this exact API/scope: it is created in a
  // layout pass and retired by the same pass, so a replay or a scope change
  // gets a fresh reader rather than a disposed one.
  const owned = useOwnedResource(
    () => new RemoteSourceService(props.api, {serverId: props.serverId, viewerId: props.viewerId}, stableStorageOperationId().next, props.isCurrent),
    value => value.dispose(),
    [props.api, props.serverId, props.viewerId, props.isCurrent],
    value => {void value.load();});
  if (!owned) return <div className={styles.page}><p className={styles.muted} role="status">{t('next.remoteSources.loading')}</p></div>;
  // The body belongs to that exact reader: a successor scope starts clean.
  return <RemoteSourcesLoaded key={owned.id} {...props} service={owned.value}/>;
}

function RemoteSourcesLoaded({api, serverId, viewerId, isCurrent, reportError, openSection, service}: RemoteSourcesPageProps & {service: RemoteSourceService}) {
  const [adding, setAdding] = useState(false);
  const phone = usePhone(), addButton = useRef<HTMLButtonElement>(null);
  const [managing, setManaging] = useState<RemoteSource>();
  type ReadingAdmission = {live: boolean};
  type ReadingSelection = {source: RemoteSource; id: number; admission: ReadingAdmission; live: boolean; current: () => boolean; close: () => void};
  const [reading, setReading] = useState<ReadingSelection>();
  const readingRef = useRef<ReadingSelection | undefined>(undefined), readingId = useRef(0);
  const renderedLocation = router.state.location;
  const readingAdmission = useOwnedResource<ReadingAdmission>(() => ({live: true}), original => {
    original.live = false;
    const selected = readingRef.current;
    if (selected?.admission === original) {selected.live = false;readingRef.current = undefined;}
  }, [service, isCurrent]);
  const openReading = (source: RemoteSource) => {
    const admission = readingAdmission?.value;
    if (!admission?.live || !isCurrent() || router.state.location !== renderedLocation || !service.getSnapshot().sources.includes(source)) return;
    if (readingRef.current) readingRef.current.live = false;
    // Every actual selection gets a new child even when close/reopen of the
    // same source batches into one React commit. Old callbacks retain only A.
    const selection: ReadingSelection = {source, id: ++readingId.current, admission, live: true,
      current: () => admission.live && selection.live && readingRef.current === selection && isCurrent() && router.state.location === renderedLocation,
      close: () => {
        if (!selection.live || readingRef.current !== selection) return;
        selection.live = false;readingRef.current = undefined;
        setReading(current => current === selection ? undefined : current);
      }};
    readingRef.current = selection;setReading(selection);
  };
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  const sources = snapshot.sources.filter(source => source.kind === 'webdav' || source.kind === 'rclone');
  const [notice, setNotice] = useState('');
  const refresh = () => {if (!isCurrent()) return;setNotice('');void service.load();};
  const recover = () => {if (!isCurrent()) return;setNotice('');void service.recover().catch(error => {if (isCurrent()) setNotice(remoteErrorText(error));});};
  const needing = sources.filter(source => stateTone(source) === 'danger');
  const attention = needing[0];
  const attentionMore = needing.length > 1 ? t('next.remoteSources.problemMore', {count: needing.length - 1}) : '';
  return <div className={styles.page}>
    <div className={styles.sourceList} hidden={adding && !phone}>
    {attention && <div className={styles.pageNotice} role="status">
      <Icon name="i-warn" size={20}/>
      <span><b>{t('next.remoteSources.problemHeading')}</b> {attention.name}: {problemId(attention) ? t(problemId(attention)!) : t('next.remoteSources.problem.offline')}{attentionMore}
        <Button size="xs" variant="text" onClick={() => setManaging(attention)}>{t('next.remoteSources.openSource', {name: attention.name})}</Button></span>
    </div>}
    <SettingsGroup title={t('next.settingsSpec.server.remote-sources.groups.0.title')}
      actions={<Button ref={addButton} size="compact" variant="primary" icon="i-plus" onClick={() => setAdding(true)}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.addLabel')}</Button>}>
      {snapshot.error && <p className={styles.error} role="alert">{snapshot.error}{snapshot.ambiguous && <Button size="xs" variant="text" onClick={recover}>{t('next.remoteSources.checkResult')}</Button>}</p>}
      {notice && <p className={styles.error} role="alert">{notice}</p>}
      {!!sources.length && <p className={styles.sectionLabel}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.label')}</p>}
      {snapshot.loading && !sources.length && <p className={styles.muted} role="status">{t('next.remoteSources.loading')}</p>}
      {!snapshot.loading && !sources.length && !snapshot.error && <div className={styles.empty}><b>{t('next.remoteSources.empty')}</b><p>{t('next.remoteSources.emptyHelp')}</p></div>}
      {sources.map(source => <SettingsRow key={source.id} label={<><Icon name="i-globe" size={18} /> {source.name}</>}
        help={<MetadataFacts parts={[t(source.kind === 'rclone' ? 'web.storage.rcloneRemote' : 'next.remoteSources.kindWebdav'), source.origin, problemId(source) ? t(problemId(source)!) : undefined]}/>}>
        <span className={styles.rowActions}><SourceStatus source={source}/><Button size="small" variant="glass" onClick={() => setManaging(source)}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.itemActions.0.label')}</Button>{source.kind === 'rclone' && <Button size="small" variant="glass" onClick={() => openReading(source)}>{t('next.remoteSources.reading')}</Button>}</span>
      </SettingsRow>)}
    </SettingsGroup>
    </div>
    <AddSourceDialog service={service} api={api} serverId={serverId} viewerId={viewerId} open={adding} returnFocusRef={addButton} isCurrent={isCurrent} reportError={reportError} onClose={() => setAdding(false)} onCreated={source => {setAdding(false);setManaging(source);}}/>
    {managing && (managing.kind === 'rclone'
      ? <ManageRcloneDialog key={managing.id} service={service} source={managing} openSection={openSection} isCurrent={isCurrent} reportError={reportError} onClose={() => setManaging(undefined)} onRemoved={() => setManaging(undefined)}/>
      : <ManageSourceDialog key={managing.id} service={service} source={managing} api={api} serverId={serverId} viewerId={viewerId} openSection={openSection} isCurrent={isCurrent} reportError={reportError} onClose={() => setManaging(undefined)} onRemoved={() => setManaging(undefined)}/>)}
    {reading && reading.admission === readingAdmission?.value && reading.current() && <SourceReadingDialog key={reading.id} service={service} source={reading.source}
      isCurrent={reading.current} reportError={reportError} onClose={reading.close} errorText={remoteErrorText} isAuthFailure={authFailure}/>}
    {!adding && snapshot.loading && sources.length > 0 && <Button size="xs" variant="text" onClick={refresh}>{t('next.remoteSources.refresh')}</Button>}
  </div>;
}

type WizardStep = 'what' | 'remote' | 'reading' | 'folders' | 'done';
type SourceFamily = 'webdav' | 'rclone' | 'config';
type ReadingOpening = Readonly<{guard: () => boolean; sourceId: string; sourceGeneration: number; profileGeneration: string; profileRevision: number}>;
/** The saved test's admission: the exact dialog owner, service, source row and
 *  ready status object it was opened against. It is published through state and
 *  retired as a whole when any of those is replaced. */
type SavedTestLease = Readonly<{owner: object; service: RemoteSourceService; source: RemoteSource; status: RemoteSourceStatus; generation: string}>;
type AttachMode = 'new' | 'existing';
type LibraryKind = ManagedLibrary['kind'];

/** The seven drawn kinds, named and hinted from the catalogue (owner-libraries.html#8). */
const libraryKinds: readonly {id: LibraryKind; icon: IconName; label: MessageId; hint: MessageId}[] = [
  {id: 'movie', icon: 'i-film', label: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.0.label', hint: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.0.hint'},
  {id: 'tv', icon: 'i-tvlib', label: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.1.label', hint: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.1.hint'},
  {id: 'anime', icon: 'i-tvlib', label: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.2.label', hint: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.2.hint'},
  {id: 'music', icon: 'i-music', label: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.3.label', hint: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.3.hint'},
  {id: 'audiobook', icon: 'i-headph', label: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.4.label', hint: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.4.hint'},
  {id: 'book', icon: 'i-sbook', label: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.5.label', hint: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.5.hint'},
  {id: 'comic', icon: 'i-page', label: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.6.label', hint: 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.6.hint'},
];
const SIX_HOURS = 21_600;
/** Google's own console, opened by the owner; Portico never navigates there itself. */
const DRIVE_CONSOLE = 'https://console.cloud.google.com';

type AddSourceProps = Readonly<{
  service: RemoteSourceService; api: LibraryContentApi; serverId: string; viewerId: string; open: boolean;
  returnFocusRef?: RefObject<HTMLButtonElement | null>;
  isCurrent: () => boolean; reportError: (error: unknown) => void; onClose: () => void; onCreated: (source: RemoteSource) => void;
}>;

/** The wizard's library reader belongs to this opening: created in a layout
 * pass from the page's reader and scope, retired with it, and the body keyed to
 * that exact resource so a replay or replacement starts clean. */
export function AddSourceDialog(props: AddSourceProps) {
  const owned = useOwnedResource(
    () => new LibraryManagementService({api: props.api, scope: {serverId: props.serverId, viewerId: props.viewerId}}),
    value => value.dispose(),
    [props.api, props.serverId, props.viewerId, props.service]);
  if (!owned) return null;
  return <AddSourceLoaded key={owned.id} {...props} libraries={owned.value}/>;
}

function AddSourceLoaded({service, api, serverId, viewerId, open, returnFocusRef, isCurrent, reportError, onClose, onCreated, libraries}: AddSourceProps & Readonly<{libraries: LibraryManagementService}>) {
  const phone = usePhone();
  const [step, setStep] = useState<WizardStep>('what');
  const [family, setFamily] = useState<SourceFamily>('webdav');
  const [sourceKind, setSourceKind] = useState<SourceKind>('webdav');
  const [draft, setDraft] = useState<WebDavDraft>(emptyDraft);
  const [rclone, setRclone] = useState<RcloneDraft>(emptyRclone);
  const [program, setProgram] = useState<RcloneProgram>();
  const [providers, setProviders] = useState<readonly string[]>([]);
  const [configView, setConfigView] = useState<RcloneConfigView>();
  const [configRemote, setConfigRemote] = useState('');
  const [configName, setConfigName] = useState('');
  const [s3, setS3] = useState<S3SourceFields>(emptyS3);
  const [s3Name, setS3Name] = useState('');
  const [drive, setDrive] = useState<DriveKeyFields>(emptyDrive);
  const [driveName, setDriveName] = useState('');
  const [driveMode, setDriveMode] = useState<'service'|'oauth'>('service');
  const [oauthClientId, setOauthClientId] = useState('');
  const [oauthClientSecret, setOauthClientSecret] = useState('');
  const [oauthToken, setOauthToken] = useState('');
  const [oauthState, setOauthState] = useState('');
  const [oauthPhase, setOauthPhase] = useState<'idle'|'starting'|'waiting'|'ready'|'expired'|'denied'|'error'>('idle');
  const [oauthAddress, setOauthAddress] = useState('');
  const [oauthError, setOauthError] = useState('');
  const oauthLease = useRef<{state:string}|null>(null);
  const oauthGeneration = useRef(0);
  const [cloud, setCloud] = useState<CloudTokenFields>(emptyCloud);
  const [cloudName, setCloudName] = useState('');
  // A test belongs to the exact rendered tuple; the revision changes with it.
  const [s3Revision, setS3Revision] = useState(0);
  const [driveRevision, setDriveRevision] = useState(0);
  const [s3Tested, setS3Tested] = useState<Tested>();
  const [driveTested, setDriveTested] = useState<Tested>();
  const [cloudRevision, setCloudRevision] = useState(0);
  const [cloudTested, setCloudTested] = useState<Tested>();
  // The refs are the invalidation truth and move before the state is published,
  // so a retained handler cannot use the tuple a finished render still shows.
  const s3RevisionRef = useRef(0), driveRevisionRef = useRef(0), cloudRevisionRef = useRef(0);
  const invalidateS3 = () => {const next = ++s3RevisionRef.current;setS3Revision(next);return next;};
  const invalidateDrive = () => {const next = ++driveRevisionRef.current;setDriveRevision(next);return next;};
  const invalidateCloud = () => {const next = ++cloudRevisionRef.current;setCloudRevision(next);return next;};
  const keyFile = useRef<HTMLInputElement>(null);
  const driveResult = driveServiceAccountDraft(drive);
  // A signed-in OAuth client is tested with its own token, not the key draft:
  // the drawn flow dims Test until the sign-in is done, then lets it run.
  const driveTestReady = driveMode === 'oauth' ? oauthPhase === 'ready' && !!oauthToken && !!drive.locationId.trim() : driveResult.ok;
  const cloudResult = cloudTokenDraft(cloud);
  const [result, setResult] = useState<DAVConnectionResult>();
  const [rcloneResult, setRcloneResult] = useState<RcloneTestResult>();
  const [error, setError] = useState('');
  const [created, setCreated] = useState<RemoteSource>();
  const nextSourceId = useRef('');
  const [profile, setProfile] = useState<SourceReadSettings>();
  const [values, setValues] = useState<SourceReadValues>(sourceReadPreset('once'));
  const [, showConsent] = useState(0);
  // The committed step and form token of this wizard opening.
  const wizardLive = useRef<{step: WizardStep; token: number}>({step: 'what', token: -1});
  const [busy, setBusy] = useState(false);
  // The folders step: chosen folders, the library they join, and what the done step reports.
  const librarySnapshot = useSyncExternalStore(libraries.subscribe, libraries.getSnapshot, libraries.getSnapshot);
  const [chosen, setChosen] = useState<readonly RemoteFolder[]>([]);
  const [picking, setPicking] = useState(false);
  const [attachMode, setAttachMode] = useState<AttachMode>('new');
  const [libraryName, setLibraryName] = useState('');
  const [kind, setKind] = useState<LibraryKind>('movie');
  const [existingId, setExistingId] = useState('');
  const [finishBusy, setFinishBusy] = useState(false);
  const [finishError, setFinishError] = useState('');
  const [summary, setSummary] = useState<Readonly<{library?: string; folders: number}>>({folders: 0});
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [leaving, setLeaving] = useState<'' | 'saved' | 'unsaved'>('');
  const [leaveChoice, setLeaveChoice] = useState<'keep' | 'remove'>('keep');
  const [leaveRemove, setLeaveRemove] = useState(false);
  const [leaveError, setLeaveError] = useState('');
  const testAttempt = useRef(0), active = useRef(false), pending = useRef(false), lifetime = useRef(0);
  const [formToken, setFormToken] = useState(0);
  // A saved handler belongs to its rendered form, even if invoked after reopen.
  const attemptGuard = () => () => active.current && lifetime.current === formToken && isCurrent();
  // This render's own guard. A retained handler carries the guard of the
  // render that drew it, never one acquired later.
  const renderGuard = attemptGuard();


  const enterReading = (sourceId: string, generation: number, guard: () => boolean) => {
    if (!guard()) return;
    const row = configuredReadingSource(service.getSnapshot().sources, {sourceId, generation});
    if (!row) {setError(t('next.remoteSources.error.changed'));return;}
    nextSourceId.current = sourceId;setCreated(row);setStep('reading');setProfile(undefined);
  };
  /** A refused owner retires this form before Settings hears about it. */
  const refuse = useCallback((error: unknown) => {active.current=false;pending.current=false;lifetime.current++;reportError(error);}, [reportError]);
  const initializeWizard = () => {
    wizardReader?.retire();
    setStep('what'); setFamily('webdav'); setSourceKind('webdav'); setDraft(emptyDraft); setRclone(emptyRclone); setProgram(undefined); setProviders([]); setConfigView(undefined); setConfigRemote(''); setConfigName(''); setS3(emptyS3); setDrive(emptyDrive); setS3Name(''); setDriveName(''); setS3Tested(undefined); setDriveTested(undefined); setCloud(emptyCloud); setCloudName(''); setCloudTested(undefined); setDriveMode('service'); setOauthClientId(''); setOauthClientSecret(''); setOauthToken(''); setOauthState(''); setOauthPhase('idle'); setOauthAddress(''); setOauthError(''); oauthLease.current = null; oauthGeneration.current++; invalidateS3(); invalidateDrive(); invalidateCloud();
    setResult(undefined); setRcloneResult(undefined); setError(''); setCreated(undefined);
    setProfile(undefined); setValues(sourceReadPreset('once')); setBusy(false);
    setChosen([]); setPicking(false); setAttachMode('new'); setLibraryName(''); setKind('movie'); setExistingId('');
    setFinishBusy(false); setFinishError(''); setSummary({folders: 0}); setLeaving(''); setLeaveRemove(false); setLeaveError(''); setLeaveChoice('keep');
    setAdvancedOpen(false);
    testAttempt.current++; lifetime.current++; setFormToken(lifetime.current); pending.current = false;
  };
  const updateDraft = (patch: Partial<WebDavDraft>) => {testAttempt.current++;setResult(undefined);setError('');setDraft(current => ({...current, ...patch}));};
  const updateRclone = (patch: Partial<RcloneDraft>) => {testAttempt.current++;setRcloneResult(undefined);setError('');setRclone(current => ({...current, ...patch}));};
  // The opening pass resets with this render's own wizard reader; a later reader
  // identity must not replay the reset.
  const initializeWizardNow = useRef(initializeWizard);
  useLayoutEffect(() => {initializeWizardNow.current = initializeWizard;});
  useLayoutEffect(() => {active.current=open;if(open)initializeWizardNow.current();else{retireOAuthCurrentRef.current();lifetime.current++;pending.current=false;setDraft(emptyDraft);setRclone(emptyRclone);setResult(undefined);setRcloneResult(undefined);setError('');}}, [open]);
  useEffect(() => () => {active.current=false;lifetime.current++;retireOAuthCurrentRef.current();}, []);
  const test = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard()) return;
    pending.current = true;
    const attempt = ++testAttempt.current, captured = {...draft};
    setError('');setResult(undefined);setBusy(true);
    try {const next = await service.testConnection({name: captured.name.trim(), root: captured.root.trim(), username: captured.username, password: captured.password, insecureLocal: captured.insecureLocal}, guard);
      if (guard() && attempt === testAttempt.current) setResult(next);}
    catch (value) {if (guard() && attempt === testAttempt.current) setError(remoteErrorText(value));}
    finally {if (guard() && attempt === testAttempt.current) {pending.current=false;setBusy(false);}}
  };
  const loadProgram = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard()) return;
    pending.current = true;
    try {
      const [status, names] = await Promise.all([service.rcloneProgram(), service.rcloneProviders()]);
      if (guard()) {setProgram(status);setProviders(names);}
    } catch (value) {
      // The generic note stays for an ordinary failure; a refusal retires the form.
      if (guard() && authFailure(value)) refuse(value);
    }
    finally {if (guard()) pending.current = false;}
  };
  const loadConfig = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard()) return;
    pending.current = true;
    try {
      const [status, view] = await Promise.all([service.rcloneProgram(), service.rcloneConfig()]);
      if (!guard()) return;
      setProgram(status);setConfigView(view);
      setConfigRemote(current => current || (view.remotes.find(remote => remote.supported)?.name ?? ''));
    } catch (value) {
      if (!guard()) return;
      if (authFailure(value)) {refuse(value);return;}
      setError(remoteErrorText(value));
    }
    finally {if (guard()) pending.current = false;}
  };
  const chooseSourceKind = (next: SourceKind) => {
    if (next === sourceKind) return;
    testAttempt.current++;retireOAuth(oauthRenderTicket);setResult(undefined);setRcloneResult(undefined);setS3Tested(undefined);setDriveTested(undefined);setCloudTested(undefined);invalidateS3();invalidateDrive();invalidateCloud();setError('');setSourceKind(next);
    if (next === 'rclone' && !program) void loadProgram();
    if (next === 'config' && !configView) void loadConfig();
  };
  /** A step move belongs to the render that drew the control, in the opening it
   * was drawn for: a retained Continue cannot move a successor wizard. */
  const wizardAction = (guard: () => boolean, expected: WizardStep, token: number) =>
    guard() && wizardLive.current.step === expected && wizardLive.current.token === token;
  const continueFromWhat = (guard: () => boolean, token: number) => {
    if (!wizardAction(guard, 'what', token)) return;
    setStep('remote');
  };
  const goBack = (from: WizardStep, to: WizardStep, guard: () => boolean, token: number) => {
    if (!wizardAction(guard, from, token)) return;
    wizardReader?.retire();setStep(to);
  };
  const chooseFamily = (next: SourceFamily, guard: () => boolean, token: number) => {
    if (!wizardAction(guard, 'what', token) || next === family) return;
    setFamily(next);
    chooseSourceKind(next === 'webdav' ? 'webdav' : next === 'config' ? 'config' : 's3');
  };
  const chooseRemoteKind = (next: SourceKind, guard: () => boolean, token: number) => {
    if (!wizardAction(guard, 'remote', token)) return;
    chooseSourceKind(next);
  };
  const driveReason = (reason: 'json'|'oauth'|'type'|'email'|'key'|'location'|'client'|'token'): MessageId => reason === 'client' || reason === 'token' ? 'next.remoteSources.driveOAuthInvalid'
    : reason === 'json' ? 'next.remoteSources.driveKeyNotJson'
    : reason === 'oauth' ? 'next.remoteSources.driveKeyOAuth'
      : reason === 'type' ? 'next.remoteSources.driveKeyWrongType'
        : reason === 'email' ? 'next.remoteSources.driveKeyEmail'
          : reason === 'key' ? 'next.remoteSources.driveKeyUnreadable'
            : 'next.remoteSources.driveLocationInvalid';
  // One rendered sign-in ticket owns Start/Paste/Status/Cancel and every phase.
  const oauthTicket = useRef<object | null>(null);
  const oauthPollPromise = useRef<Promise<void> | null>(null);
  const oauthApproved = useRef<{clientId: string; clientSecret: string} | null>(null);
  const oauthClientRef = useRef({id: '', secret: ''});
  const oauthRenderTicket = oauthTicket.current;
  const oauthRenderLease = oauthLease.current;
  const oauthRenderClient = {id: oauthClientId, secret: oauthClientSecret};
  oauthClientRef.current = oauthRenderClient;
  const retireOAuth = (ticket: object | null): boolean => {
    // Only the exact opening may retire itself; it cannot retire a successor.
    if (ticket !== oauthTicket.current) return false;
    oauthGeneration.current++;
    oauthTicket.current = null;
    const lease = oauthLease.current;
    oauthLease.current = null;
    oauthApproved.current = null;
    if (ticket && lease) void service.driveOAuthCancel(lease.state);
    setOauthState('');setOauthToken('');setOauthClientSecret('');setOauthPhase('idle');setOauthAddress('');setOauthError('');
    return true;
  };
  /** Every OAuth transport carries this opening's exact ticket, lease and draft revision. */
  const oauthGuardFor = (ticket: object, revision: number, lease?: {state: string}) => () => {
    const guard = attemptGuard();
    return guard() && oauthTicket.current === ticket && driveRevisionRef.current === revision && (!lease || oauthLease.current === lease);
  };
  const retireOAuthCurrentRef = useRef<() => void>(() => {});
  retireOAuthCurrentRef.current = () => retireOAuth(oauthTicket.current);
  const updateOAuthClient = (patch: {clientId?: string; clientSecret?: string}) => {
    // A new client is a new authorization: cancel the old lease at once.
    retireOAuth(oauthRenderTicket);
    invalidateDrive();setDriveTested(undefined);setRcloneResult(undefined);setError('');
    if (patch.clientId !== undefined) setOauthClientId(patch.clientId);
    if (patch.clientSecret !== undefined) setOauthClientSecret(patch.clientSecret);
  };
  const updatePasteAddress = (value: string) => {setOauthAddress(value);};
  const pollOAuth = async (state: string, ticket: object, guard: () => boolean, revision: number, generation: number) => {
    for (let attempt = 0; attempt < 150; attempt++) {
      await new Promise(resolve => setTimeout(resolve, 2000));
      if (!guard() || oauthTicket.current !== ticket || oauthGeneration.current !== generation || driveRevisionRef.current !== revision) return;
      try {
        const status = await service.driveOAuthStatus(state, oauthGuardFor(ticket, revision, oauthLease.current ?? undefined));
        if (!guard() || oauthTicket.current !== ticket || oauthGeneration.current !== generation || driveRevisionRef.current !== revision) return;
        if (status.phase === 'pending') continue;
        if (status.phase === 'ready') {setOauthToken(status.token);setDriveTested(undefined);setOauthPhase('ready');return;}
        setOauthPhase(status.phase === 'denied' ? 'denied' : 'expired');return;
      } catch { if (guard() && oauthTicket.current === ticket) setOauthPhase('error'); return; }
    }
    if (oauthTicket.current === ticket) setOauthPhase('expired');
  };
  const startOAuth = async () => {
    const guard = attemptGuard();
    const frozen = oauthRenderClient, revision = driveRevision;
    if (pending.current || !guard()) return;
    if (oauthTicket.current !== oauthRenderTicket) return;
    if (oauthClientRef.current.id !== frozen.id || oauthClientRef.current.secret !== frozen.secret || revision !== driveRevisionRef.current) return;
    if (!frozen.id.trim() || !frozen.secret) {setOauthError(t('next.remoteSources.driveOAuthInvalid'));return;}
    if (!retireOAuth(oauthRenderTicket)) return;
    const ticket = {};oauthTicket.current = ticket;
    pending.current = true;setOauthError('');setOauthPhase('starting');
    try {
      const startedGuard = oauthGuardFor(ticket, revision);
      const started = await service.startDriveOAuth(frozen.id.trim(), frozen.secret, startedGuard);
      if (!startedGuard()) {void service.driveOAuthCancel(started.state);return;}
      // The opening's client tuple stays private for its Ready/Test/Save; the visible secret field stays empty.
      oauthLease.current = {state: started.state};oauthApproved.current = {clientId: frozen.id, clientSecret: frozen.secret};
      setOauthState(started.state);setOauthPhase('waiting');
      window.open(started.authorizationUrl, '_blank', 'noopener,noreferrer');
      // One status owner: await the retiring flight's settlement, then start this ticket's.
      const prior = oauthPollPromise.current;
      const run = (async () => {
        if (prior) await prior;
        if (!guard() || oauthTicket.current !== ticket) return;
        await pollOAuth(started.state, ticket, startedGuard, revision, ++oauthGeneration.current);
      })();
      oauthPollPromise.current = run;
      void run.finally(() => {if (oauthPollPromise.current === run) oauthPollPromise.current = null;});
    } catch (value) {
      if (!guard() || oauthTicket.current !== ticket) return;
      if (authFailure(value)) {refuse(value);return;}
      setOauthPhase('error');setOauthError(t('next.remoteSources.driveOAuthInvalid'));
    } finally {if (guard()) pending.current = false;}
  };
  const pasteOAuth = async () => {
    const guard = attemptGuard();
    const ticket = oauthRenderTicket, lease = oauthRenderLease;
    if (pending.current || !guard() || !ticket || !lease || !oauthAddress.trim()) return;
    if (oauthTicket.current !== ticket || oauthLease.current !== lease) return;
    const pasteGuard = oauthGuardFor(ticket, driveRevision, lease);
    pending.current = true;setOauthError('');
    try {
      await service.driveOAuthPaste(lease.state, oauthAddress, pasteGuard);
      if (!guard() || oauthTicket.current !== ticket || oauthLease.current !== lease) return;
      // The pasted address is spent; the running poll is the only status owner.
      setOauthAddress('');
    } catch (value) {
      if (!guard() || oauthTicket.current !== ticket) return;
      if (authFailure(value)) {refuse(value);return;}
      setOauthError(t('next.remoteSources.driveOAuthInvalid'));
    } finally {if (guard()) pending.current = false;}
  };
  const updateDrive = (patch: Partial<DriveKeyFields>) => {testAttempt.current++;setRcloneResult(undefined);setDriveTested(undefined);invalidateDrive();setError('');setDrive(current => ({...current, ...patch}));};
  const readKeyFile = async (file: File | undefined) => {
    const guard = attemptGuard();
    const opening = driveRevisionRef.current;
    if (!file || !guard()) return;
    if (file.size > 64 * 1024) {setError(t('next.remoteSources.driveKeyTooLarge'));return;}
    let body = '';
    try {body = await file.text();}
    catch {if (guard() && opening === driveRevisionRef.current) setError(t('next.remoteSources.driveKeyUnreadable'));return;}
    // The key applies only to the form that opened the file and did not change since.
    if (!guard() || opening !== driveRevisionRef.current) return;
    updateDrive({keyJson: body});
  };
  const copyEmail = (email: string) => {const clipboard = navigator.clipboard;if (!clipboard) return;void clipboard.writeText(email).catch(() => undefined);};
  const testDrive = async () => {
    const guard = attemptGuard();
    const revision = driveRevision, fields = {...drive};
    if (pending.current || !guard() || revision !== driveRevisionRef.current) return;
    const result = driveMode === 'oauth'
      ? driveOAuthDraft({clientId: oauthApproved.current?.clientId ?? '', clientSecret: oauthApproved.current?.clientSecret ?? '', token: oauthToken, location: fields.location, locationId: fields.locationId})
      : driveServiceAccountDraft(fields);
    if (!result.ok) {setError(t(driveMode === 'oauth' ? 'next.remoteSources.driveOAuthInvalid' : driveReason(result.reason)));return;}
    pending.current = true;
    const attempt = ++testAttempt.current, built = result.draft;
    setError('');setRcloneResult(undefined);setBusy(true);
    try {const next = await service.testRclone({remote: built.remote, config: built.config}, guard);
      if (guard() && attempt === testAttempt.current && revision === driveRevisionRef.current) {setRcloneResult(next);setDriveTested({revision, result: next});}}
    catch (value) {
      if (!guard() || attempt !== testAttempt.current) return;
      if (authFailure(value)) {refuse(value);return;}
      setError(remoteErrorText(value));
    }
    finally {if (guard() && attempt === testAttempt.current) {pending.current=false;setBusy(false);}}
  };
  const createDrive = async () => {
    const guard = attemptGuard();
    const revision = driveRevision, fields = {...drive}, name = driveName.trim(), tested = driveTested;
    if (pending.current || !guard() || !tested || tested.revision !== revision || revision !== driveRevisionRef.current || !name) return;
    const result = driveMode === 'oauth'
      ? driveOAuthDraft({clientId: oauthApproved.current?.clientId ?? '', clientSecret: oauthApproved.current?.clientSecret ?? '', token: oauthToken, location: fields.location, locationId: fields.locationId})
      : driveServiceAccountDraft(fields);
    if (!result.ok) {setError(t(driveMode === 'oauth' ? 'next.remoteSources.driveOAuthInvalid' : driveReason(result.reason)));return;}
    pending.current = true;setError('');setBusy(true);
    const built = result.draft;
    // The submitted tuple is captured; the key leaves the form and this witness is spent.
    setDrive(current => ({...current, keyJson: ''}));setDriveTested(undefined);invalidateDrive();
    if (driveMode === 'oauth') {setOauthClientSecret('');setOauthToken('');retireOAuth(oauthRenderTicket);}
    try {
      const receipt = await service.configureRclone(null, {name, remote: built.remote, config: built.config}, undefined, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (!receipt) {
        if (authFailure({code: state.errorCode})) {refuse({code: state.errorCode});return;}
        setError(state.error || t('next.remoteSources.unconfirmed'));return;
      }
      enterReading(receipt.sourceId, receipt.generation, guard);
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const cloudReason = (reason: 'json'|'token'|'client'|'google'|'folder'): MessageId => reason === 'json' ? 'next.remoteSources.cloudTokenNotJson'
    : reason === 'token' ? 'next.remoteSources.cloudTokenMissing'
      : reason === 'client' ? 'next.remoteSources.cloudTokenClient'
        : reason === 'google' ? 'next.remoteSources.cloudTokenGoogle'
          : 'next.remoteSources.cloudFolderInvalid';
  const updateCloud = (patch: Partial<CloudTokenFields>) => {testAttempt.current++;setRcloneResult(undefined);setCloudTested(undefined);invalidateCloud();setError('');setCloud(current => ({...current, ...patch}));};
  const testCloud = async () => {
    const guard = attemptGuard();
    const revision = cloudRevision, fields = {...cloud};
    if (pending.current || !guard() || revision !== cloudRevisionRef.current) return;
    const result = cloudTokenDraft(fields);
    if (!result.ok) {setError(t(cloudReason(result.reason)));return;}
    pending.current = true;
    const attempt = ++testAttempt.current, built = result.draft;
    setError('');setRcloneResult(undefined);setBusy(true);
    try {const next = await service.testRclone({remote: built.remote, config: built.config}, guard);
      if (guard() && attempt === testAttempt.current && revision === cloudRevisionRef.current) {setRcloneResult(next);setCloudTested({revision, result: next});}}
    catch (value) {
      if (!guard() || attempt !== testAttempt.current) return;
      if (authFailure(value)) {refuse(value);return;}
      setError(remoteErrorText(value));
    }
    finally {if (guard() && attempt === testAttempt.current) {pending.current=false;setBusy(false);}}
  };
  const createCloud = async () => {
    const guard = attemptGuard();
    const revision = cloudRevision, fields = {...cloud}, name = cloudName.trim(), tested = cloudTested;
    if (pending.current || !guard() || !tested || tested.revision !== revision || revision !== cloudRevisionRef.current || !name) return;
    const result = cloudTokenDraft(fields);
    if (!result.ok) {setError(t(cloudReason(result.reason)));return;}
    pending.current = true;setError('');setBusy(true);
    const built = result.draft;
    // The submitted tuple is captured; the token leaves the form and this witness is spent.
    setCloud(current => ({...current, token: ''}));setCloudTested(undefined);invalidateCloud();
    try {
      const receipt = await service.configureRclone(null, {name, remote: built.remote, config: built.config}, undefined, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (!receipt) {
        if (authFailure({code: state.errorCode})) {refuse({code: state.errorCode});return;}
        setError(state.error || t('next.remoteSources.unconfirmed'));return;
      }
      enterReading(receipt.sourceId, receipt.generation, guard);
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const updateS3 = (patch: Partial<S3SourceFields>) => {testAttempt.current++;setRcloneResult(undefined);setS3Tested(undefined);invalidateS3();setError('');setS3(current => ({...current, ...patch}));};
  const testS3 = async () => {
    const guard = attemptGuard();
    const revision = s3Revision, fields = {...s3};
    if (pending.current || !guard() || revision !== s3RevisionRef.current) return;
    const built = s3SourceDraft(fields);
    if (!built) {setError(t('next.remoteSources.s3Invalid'));return;}
    pending.current = true;
    const attempt = ++testAttempt.current;
    setError('');setRcloneResult(undefined);setBusy(true);
    try {const next = await service.testRclone({remote: built.remote, config: built.config}, guard);
      if (guard() && attempt === testAttempt.current && revision === s3RevisionRef.current) {setRcloneResult(next);setS3Tested({revision, result: next});}}
    catch (value) {
      if (!guard() || attempt !== testAttempt.current) return;
      if (authFailure(value)) {refuse(value);return;}
      setError(remoteErrorText(value));
    }
    finally {if (guard() && attempt === testAttempt.current) {pending.current=false;setBusy(false);}}
  };
  const testRclone = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard()) return;
    pending.current = true;
    const attempt = ++testAttempt.current, captured = {...rclone};
    setError('');setRcloneResult(undefined);setBusy(true);
    try {const next = await service.testRclone({remote: captured.remote.trim(), config: captured.config}, guard);
      if (guard() && attempt === testAttempt.current) setRcloneResult(next);}
    catch (value) {
      if (!guard() || attempt !== testAttempt.current) return;
      if (authFailure(value)) {refuse(value);return;}
      setError(remoteErrorText(value));
    }
    finally {if (guard() && attempt === testAttempt.current) {pending.current=false;setBusy(false);}}
  };
  const createRclone = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard() || !rcloneResult) return;
    pending.current = true;
    setError('');setBusy(true);
    const outgoing = {name: rclone.name.trim(), remote: rclone.remote.trim(), config: rclone.config};
    // The configuration leaves the form as the mutation starts; only the request holds it now.
    setRclone(current => ({...current, config: ''}));
    try {
      const receipt = await service.configureRclone(null, outgoing, undefined, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (!receipt) {
        if (authFailure({code: state.errorCode})) {refuse({code: state.errorCode});return;}
        setError(state.error || t('next.remoteSources.unconfirmed'));return;
      }
      enterReading(receipt.sourceId, receipt.generation, guard);
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const create = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard() || !result) return;
    pending.current = true;
    setError('');setBusy(true);
    try {
      await service.configure(null, davConfiguration(null, draft), undefined, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (state.error || !state.receipt) {if (guard()) setError(state.error || t('next.remoteSources.unconfirmed'));return;}
      const source = state.receipt.source;enterReading(source.id, source.generation, guard);setDraft(current => ({...current, password: ''}));
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const createS3 = async () => {
    const guard = attemptGuard();
    const revision = s3Revision, fields = {...s3}, name = s3Name.trim(), tested = s3Tested;
    if (pending.current || !guard() || !tested || tested.revision !== revision || revision !== s3RevisionRef.current || !name) return;
    const built = s3SourceDraft(fields);
    if (!built) {setError(t('next.remoteSources.s3Invalid'));return;}
    pending.current = true;setError('');setBusy(true);
    // The submitted tuple is captured; the secret leaves the form and this witness is spent.
    setS3(current => ({...current, secretKey: ''}));setS3Tested(undefined);invalidateS3();
    try {
      const receipt = await service.configureRclone(null, {name, remote: built.remote, config: built.config}, undefined, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (!receipt) {
        if (authFailure({code: state.errorCode})) {refuse({code: state.errorCode});return;}
        setError(state.error || t('next.remoteSources.unconfirmed'));return;
      }
      enterReading(receipt.sourceId, receipt.generation, guard);
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const createFromConfig = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard() || !configRemote) return;
    const chosen = configView?.remotes.find(remote => remote.name === configRemote && remote.supported);
    if (!chosen) {setError(t('next.remoteSources.configUnsupportedKind'));return;}
    pending.current = true;setError('');setBusy(true);
    try {
      const receipt = await service.configureRcloneFromConfig({name: configName.trim() || chosen.name, remote: chosen.name}, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (!receipt) {
        if (authFailure({code: state.errorCode})) {refuse({code: state.errorCode});return;}
        setError(state.error || t('next.remoteSources.unconfirmed'));return;
      }
      enterReading(receipt.sourceId, receipt.generation, guard);
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const saveReading = () => {
    if (pending.current || !wizardAction(renderGuard, 'reading', formToken) || !wizardReader || !wizardReadState) return;
    if (wizardReadState.kind === 'unknown') {void wizardReader.recover(wizardReadState);return;}
    if (wizardReadState.kind !== 'ready' && wizardReadState.kind !== 'failed' || !wizardReadState.draft) return;
    const draft = wizardReadState.draft;
    void (draft.advancedDirty ? wizardReader.saveAdvanced(draft, wizardReadState) : wizardReader.save(draft, wizardReadState));
  };
  /** Attaches the chosen folders to a new or existing library; the source is
   * already saved, so a failure here leaves the source in place. */
  const finish = async () => {
    if (finishBusy || pending.current) return;
    setFinishBusy(true);setFinishError('');
    try {
      if (!chosen.length) {setSummary({folders: 0});setStep('done');return;}
      if (attachMode === 'new') {
        const name = libraryName.trim() || created?.name || '';
        if (!name) {setFinishError(t('next.remoteSources.attachFailed'));return;}
        await libraries.createLibrary({name, kind, folders: chosen.map(folder => folder.path)});
        if (libraries.getSnapshot().mutation.phase !== 'complete') {setFinishError(t('next.remoteSources.attachFailed'));return;}
        setSummary({library: name, folders: chosen.length});
      } else {
        const target = librarySnapshot.directory.data?.items.find(library => library.id === existingId);
        if (!target) {setFinishError(t('next.remoteSources.existingLibraryMissing'));return;}
        for (const folder of chosen) {
          const config = await readLibraryInventoryConfig(api, {serverId, viewerId}, target.id);
          await saveLibraryFolder(api, target.id, undefined, {name: folder.name, path: folder.path, classification: 'local', followSymlinks: false, intervalSeconds: SIX_HOURS, expectedRevision: config.revision, acceptReplacement: false});
        }
        setSummary({library: target.name, folders: chosen.length});
      }
      setStep('done');
    } catch (value) {
      if (isCurrent()) setFinishError(attachFailure(value));
    } finally {if (isCurrent()) setFinishBusy(false);}
  };
  /** Leaving before the end is a real choice once the source is saved, and the
   * dialog says exactly what is kept and what is removed. */
  const close = (force = false) => {
    if (!renderGuard() || wizardLive.current.token !== formToken) return;
    if (finishBusy) return;
    if (!force && step !== 'done') {
      const saved = created ?? service.getSnapshot().sources.find(source => source.id === nextSourceId.current);
      if (saved) {setLeaving('saved');setLeaveRemove(false);setLeaveError('');setLeaveChoice('keep');return;}
      if (step === 'what') force = true;
      else if (step !== 'remote') {setLeaving('unsaved');setLeaveError('');return;}
      else if (!draft.name.trim() && !draft.root.trim() && !draft.username && !draft.password) force = true;
      else {setLeaving('unsaved');setLeaveError('');return;}
    }
    if (busy || pending.current) return;
    wizardReader?.retire();
    retireOAuthCurrentRef.current();
    active.current=false;pending.current=false;lifetime.current++;setDraft(emptyDraft);setResult(undefined);setError('');setChosen([]);setLeaving('');onClose();
  };
  const removeAndLeave = async () => {
    const guard = attemptGuard();
    if (!guard() || !leaving) return;
    const saved = created ?? service.getSnapshot().sources.find(source => source.id === nextSourceId.current);
    if (!saved || leaveRemove) return;
    setLeaveRemove(true);setLeaveError('');
    try {await service.remove(saved, guard);if (!guard()) return;const state = service.getSnapshot();if (state.error) {setLeaveError(state.error);return;}close(true);}
    catch (value) {if (guard()) setLeaveError(remoteErrorText(value));}
    finally {if (guard()) setLeaveRemove(false);}
  };
  useEffect(() => {if (attachMode === 'existing' && !existingId && librarySnapshot.directory.data?.items.length) setExistingId(librarySnapshot.directory.data.items[0].id);}, [attachMode, existingId, librarySnapshot.directory.data]);
  const sourceNow = created ?? (step === 'folders' || step === 'done' ? service.getSnapshot().sources.find(source => source.id === nextSourceId.current) : undefined);
  const recordedReading = profile ? sourceChoiceComparison(profile) : undefined;
  const presetName = recordedReading?.changedFields.length === 0 ? recordedReading.name : undefined;
  // The drawn wizard has five steps: what, the remote, how Portico treats it,
  // folders, done (remote-sources.html#1, #8 to #12).
  const stepNumber = step === 'what' ? 1 : step === 'remote' ? 2 : step === 'reading' ? 3 : step === 'folders' ? 4 : 5;
  const familyLabel = family === 'webdav' ? 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.0.label'
    : family === 'rclone' ? 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.1.label'
      : 'next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.2.label';
  const stepTitle = step === 'what' ? t('next.remoteSources.whatToConnect') : step === 'remote' ? t(familyLabel)
    : step === 'reading' ? t('next.remoteSources.howTreated') : step === 'folders' ? t('next.remoteSources.foldersSection') : '';
  const foldersFooter = <>
    <Button size="compact" variant="glass" disabled={finishBusy} onClick={() => {if (!wizardAction(renderGuard, 'folders', formToken)) return;setFinishError('');setStep('reading');}}>{t('next.remoteSources.back')}</Button>
    <Button size="compact" variant="primary" loading={finishBusy} disabled={finishBusy || (!!chosen.length && attachMode === 'new' && !libraryName.trim())} onClick={() => void finish()}>{t('next.remoteSources.finish')}</Button>
  </>;
  const doneFooter = <>
    <Button size="compact" variant="glass" onClick={() => close(true)}>{t('next.remoteSources.done')}</Button>
    <Button size="compact" variant="glass" onClick={() => {if (wizardAction(renderGuard, 'done', formToken)) initializeWizard();}}>{t('next.remoteSources.addAnother')}</Button>
    {sourceNow && <Button size="compact" variant="primary" onClick={() => {if (!wizardAction(renderGuard, 'done', formToken)) return;active.current=false;onCreated(sourceNow);}}>{t('next.remoteSources.openSource', {name: sourceNow.name})}</Button>}
  </>;
  const canContinue = sourceKind === 'webdav' ? !!result
    : sourceKind === 'rclone' ? !!rcloneResult && !!rclone.name.trim()
      : sourceKind === 's3' ? !!s3Tested && s3Tested.revision === s3Revision && !!s3Name.trim()
        : sourceKind === 'drive' ? !!driveTested && driveTested.revision === driveRevision && !!driveName.trim() && (driveMode === 'oauth' ? oauthPhase === 'ready' && !!oauthToken : driveResult.ok)
          : sourceKind === 'cloud' ? !!cloudTested && cloudTested.revision === cloudRevision && !!cloudName.trim() && cloudResult.ok
          : !!configRemote && !!configView?.remotes.some(remote => remote.name === configRemote && remote.supported);
  useLayoutEffect(() => {wizardLive.current = {step, token: formToken};});
  const [readingEpoch, setReadingEpoch] = useState(0);
  const readingOwner = useOwnedResource(() => {
    if (!open || step !== 'reading' || !created) return undefined;
    const location = router.state.location;
    const reader = new ReadingProfileOpening(service, created, () => renderGuard() && router.state.location === location &&
      wizardLive.current.step === 'reading' && wizardLive.current.token === formToken &&
      service.getSnapshot().sources.some(row => row.id === created.id && row.generation === created.generation),
      () => !pending.current && !service.getSnapshot().busy && !service.getSnapshot().ambiguous);
    const off = router.subscribe('onBeforeNavigate', () => reader.retire());
    return {reader, retire: () => {reader.retire();off();}};
  }, entry => entry?.retire(), [service, created, isCurrent, formToken, open, step === 'reading', readingEpoch], entry => {void entry?.reader.load();});
  const wizardReader = readingOwner?.value?.reader;
  const wizardReadState = useSyncExternalStore(wizardReader?.subscribe ?? noReadSubscribe, wizardReader?.getSnapshot ?? noReadState, noReadState);
  const readingCurrent = wizardReader?.current() === true;
  const wizardCanSave = readingCurrent && !!wizardReadState && !service.getSnapshot().busy &&
    (wizardReadState.kind === 'unknown' || !service.getSnapshot().ambiguous &&
      (wizardReadState.kind === 'ready' || wizardReadState.kind === 'failed') && !!wizardReadState.draft);
  const readingHandled = useRef<object | undefined>(undefined);
  useLayoutEffect(() => {
    if (!wizardReader?.current() || !wizardReadState || wizardReadState.kind !== 'failed' || !authFailure(wizardReadState.error)) return;
    wizardReader.retire();refuse(wizardReadState.error);
  }, [wizardReader, wizardReadState, refuse]);
  // The accepted-read completion keeps the form token, guard and services of the
  // commit that admitted it; a later render's closures must not move this step.
  const readingPass = useRef({renderGuard, formToken, libraries, service});
  useLayoutEffect(() => {readingPass.current = {renderGuard, formToken, libraries, service};});
  useEffect(() => {
    const {renderGuard, formToken, libraries, service} = readingPass.current;
    if (!wizardReader?.current() || wizardReadState?.kind !== 'saved' || readingHandled.current === wizardReadState) return;
    readingHandled.current = wizardReadState;
    if (!wizardReadState.profile) {setReadingEpoch(value => value + 1);return;}
    const accepted = wizardReadState.profile, location = router.state.location;
    const current = () => renderGuard() && router.state.location === location && wizardLive.current.step === 'reading' && wizardLive.current.token === formToken;
    void (async () => {
      // A confirmed choice is already terminal. Only this exact accepted raw
      // generation may supply the next folder step; a refresh failure is no PUT.
      await service.load();
      if (!current()) return;
      const row = acceptedReadingSource(service.getSnapshot().sources, accepted);
      if (!row) {setError(t('next.remoteSources.error.refreshFailed'));return;}
      wizardReader.retire();setProfile(accepted);setValues(readValuesWithoutConsent(accepted.values));setCreated(row);
      setStep('folders');setLibraryName(value => value || row.name);void libraries.loadLibraries();
    })().catch(error => {if (current()) setError(remoteErrorText(error));});
  }, [wizardReader, wizardReadState]);
  const footer = step === 'what'
    ? <><Button size="compact" variant="glass" onClick={() => close()}>{t('next.remoteSources.cancel')}</Button><Button size="compact" variant="primary" onClick={() => continueFromWhat(renderGuard, formToken)}>{t('next.remoteSources.continue')}</Button></>
    : step === 'remote'
      ? <><Button size="compact" variant="glass" disabled={busy} onClick={() => goBack('remote', 'what', renderGuard, formToken)}>{t('next.remoteSources.back')}</Button><Button size="compact" variant="primary" disabled={busy || !canContinue} onClick={() => void (sourceKind === 'webdav' ? create() : sourceKind === 'rclone' ? createRclone() : sourceKind === 's3' ? createS3() : sourceKind === 'drive' ? createDrive() : sourceKind === 'cloud' ? createCloud() : createFromConfig())}>{t('next.remoteSources.saveAndContinue')}</Button></>
      : step === 'reading'
        ? <><Button size="compact" variant="glass" disabled={busy} onClick={() => goBack('reading', 'remote', renderGuard, formToken)}>{t('next.remoteSources.back')}</Button><Button size="compact" variant="primary" disabled={busy || !wizardCanSave} onClick={saveReading}>{t(wizardReadState?.kind === 'unknown' ? 'next.remoteSources.checkResult' : 'next.remoteSources.save')}</Button></>
        : step === 'folders' ? foldersFooter : doneFooter;
  return <SourceWizard open={open} returnFocusRef={returnFocusRef} onClose={() => close()} title={t('next.settingsSpec.server.remote-sources.panels.newSource.newTitle')} currentTitle={stepTitle}
    step={stepNumber} steps={[
      {title: t('next.remoteSources.whatToConnect'), detail: t(familyLabel)},
      {title: t('next.remoteSources.remote'), detail: sourceNow?.name ?? created?.name},
      {title: t('next.remoteSources.howTreated'), detail: recordedReading ? t(presetName ? presetLabel(presetName) : 'next.remoteSources.custom') : undefined},
      {title: t('next.remoteSources.foldersSection'), detail: summary.library},
      {title: t('next.remoteSources.done')},
    ]} footer={footer}>
    {error && <p className={styles.error} role="alert">{error}</p>}
    {finishError && <p className={styles.error} role="alert">{finishError}</p>}
    {step === 'what' && <RadioGroup.Root className={styles.whatCards} aria-label={t('next.remoteSources.whatToConnect')} value={family} disabled={busy}
      onValueChange={value => {const option = whatOptions.find(option => option.family === value);if (!busy && option) chooseFamily(option.family, renderGuard, formToken);}}>
      {whatOptions.map(option => <RadioGroup.Item key={option.family} value={option.family} className={styles.whatCard}
        data-picked={family === option.family || undefined} disabled={busy}>
        <span className={styles.choiceRadio} aria-hidden="true"/>
        <span className={styles.whatBody}><b>{t(option.label)}{option.tag && <span className={styles.choiceTag} data-tone={option.tag === 'next.remoteSources.kindCommon' ? 'good' : undefined}>{t(option.tag)}</span>}</b>
          <span className={styles.choiceHint}>{t(option.hint)}</span></span>
      </RadioGroup.Item>)}
    </RadioGroup.Root>}
    {step === 'remote' && <>
      {family === 'rclone' && <RadioGroup.Root className={styles.remoteKinds} aria-label={t('next.remoteSources.remoteKind')} value={sourceKind} disabled={busy}
        onValueChange={value => {const kind = rcloneKindGroups.flatMap(group => group.kinds).find(kind => kind.value === value);if (!busy && kind) chooseRemoteKind(kind.value, renderGuard, formToken);}}>
        {rcloneKindGroups.map(group => <div key={group.title} className={styles.remoteGroup}>
          <p className={styles.advTitle}>{t(group.title)}</p>
          <div className={styles.remoteKindList}>
            {group.kinds.map(kind => <RadioGroup.Item key={kind.value} value={kind.value} className={styles.remoteKind}
              data-picked={sourceKind === kind.value || undefined} disabled={busy}>
              <span className={styles.choiceRadio} aria-hidden="true"/><Icon name={kind.icon} size={20}/>{t(kind.label)}
            </RadioGroup.Item>)}
          </div>
        </div>)}
      </RadioGroup.Root>}
      {sourceKind === 'webdav' ? <>
        <div className={styles.kind}><Icon name="i-globe" size={22} /><span><b>{t('next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.0.label')}</b><small>{t('next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.0.rows.0.control.options.0.hint')}</small></span></div>
        <TextField label={t('next.remoteSources.name')} value={draft.name} maxLength={100} disabled={busy} onChange={event => updateDraft({name: event.target.value})}/>
        <TextField label={t('next.remoteSources.address')} value={draft.root} placeholder={t('next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.0.control.placeholder')} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateDraft({root: event.target.value})}/>
        <TextField label={t('next.remoteSources.username')} value={draft.username} placeholder={t('next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.1.control.placeholder')} disabled={busy} autoCapitalize="off" spellCheck={false} onChange={event => updateDraft({username: event.target.value})}/>
        <TextField label={t('next.remoteSources.password')} type="password" value={draft.password} placeholder={t('next.settingsSpec.server.remote-sources.panels.newSource.tabs.0.groups.1.rows.2.control.placeholder')} disabled={busy} onChange={event => updateDraft({password: event.target.value})}/>
        <label className={styles.switchRow}><span>{t('next.remoteSources.insecureLocal')}</span><Switch aria-label={t('next.remoteSources.insecureLocal')} checked={draft.insecureLocal} disabled={busy} onCheckedChange={value => updateDraft({insecureLocal: value})}/></label>
        <div className={styles.testRow}><Button size="compact" variant="glass" disabled={busy || !draft.name.trim() || !draft.root.trim()} onClick={() => void test()}>{busy ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>
          {result && <span className={styles.testCard} data-tone={result.rangeSupport === 'unsupported' ? 'warning' : 'good'}><Icon name="i-check" size={18}/>{t(result.rangeSupport === 'unsupported' ? 'next.remoteSources.testedLimited' : 'next.remoteSources.tested', {origin: result.origin})}</span>}
        </div>
      </> : sourceKind === 'cloud' ? <>
        <div className={styles.kind}><Icon name="i-storage" size={22} /><span><b>{t('next.remoteSources.connectCloud')}</b><small>{t('next.remoteSources.connectCloudHint')}</small></span></div>
        <TextField label={t('next.remoteSources.name')} value={cloudName} maxLength={200} disabled={busy} onChange={event => setCloudName(event.target.value)}/>
        <div className={styles.lab2}>{t('next.remoteSources.cloudProvider')}</div>
        <div className={styles.segment} role="group">
          <Button size="small" variant={cloud.provider === 'onedrive' ? 'primary' : 'glass'} aria-pressed={cloud.provider === 'onedrive'} disabled={busy} onClick={() => updateCloud({provider: 'onedrive'})}>{t('next.remoteSources.cloudOneDrive')}</Button>
          <Button size="small" variant={cloud.provider === 'dropbox' ? 'primary' : 'glass'} aria-pressed={cloud.provider === 'dropbox'} disabled={busy} onClick={() => updateCloud({provider: 'dropbox'})}>{t('next.remoteSources.cloudDropbox')}</Button>
        </div>
        <label className={styles.configField}><span className={styles.configLabel}>{t('next.remoteSources.cloudToken')}</span>
          <textarea className={styles.config} aria-label={t('next.remoteSources.cloudToken')} value={cloud.token} placeholder={t('next.remoteSources.cloudTokenPlaceholder')} maxLength={8192}
            disabled={busy} spellCheck={false} rows={5} onChange={event => updateCloud({token: event.target.value})}/>
          <small className={styles.note}>{t('next.remoteSources.cloudTokenHelp')}</small>
        </label>
        {!cloud.token.trim() ? <p className={styles.note}>{t('next.remoteSources.cloudTokenEmpty')}</p>
          : !cloudResult.ok ? <p className={styles.error} role="alert">{t(cloudReason(cloudResult.reason))}</p> : null}
        <TextField label={t('next.remoteSources.cloudFolder')} description={t('next.remoteSources.cloudFolderHelp')} value={cloud.folder} maxLength={1024} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateCloud({folder: event.target.value})}/>
        <p className={styles.note}>{t('next.remoteSources.cloudChecks')}</p>
        <div className={styles.testRow}><Button size="compact" variant="glass" disabled={busy || !cloudResult.ok} onClick={() => void testCloud()}>{busy ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>
          {rcloneResult && <span className={styles.testCard} data-tone="good"><Icon name="i-check" size={18}/>{rcloneResult.version ? t('next.remoteSources.testedRclone', {version: rcloneResult.version}) : t('next.remoteSources.testedRcloneUnknown')}</span>}
        </div>
      </> : sourceKind === 'drive' ? <>
        <div className={styles.kind}><Icon name="i-google" size={22} /><span><b>{t('next.remoteSources.connectDrive')}</b><small>{t('next.remoteSources.connectDriveHint')}</small></span></div>
        <TextField label={t('next.remoteSources.name')} value={driveName} maxLength={200} disabled={busy} onChange={event => setDriveName(event.target.value)}/>
        <DriveModePane mode={driveMode} disabled={busy} onChange={next => {retireOAuth(oauthRenderTicket);invalidateDrive();setDriveTested(undefined);setDriveMode(next);}}/>
        <a className={styles.consoleLink} href={DRIVE_CONSOLE} target="_blank" rel="noopener noreferrer">{t('next.remoteSources.driveConsole')}</a>
        {driveMode === 'oauth' && <DriveOAuthPane phase={oauthPhase} clientId={oauthClientId} clientSecret={oauthClientSecret} address={oauthAddress}
          error={oauthError} disabled={busy} onClientId={value => updateOAuthClient({clientId: value})} onClientSecret={value => updateOAuthClient({clientSecret: value})}
          onSignIn={() => void startOAuth()} onAddress={value => updatePasteAddress(value)} onPaste={() => void pasteOAuth()}/>}
        {driveMode === 'service' && <>
        <DriveKeyPane value={drive.keyJson} disabled={busy}
          error={driveResult.ok ? undefined : t(driveReason(driveResult.reason))}
          email={driveResult.ok ? driveResult.draft.email : undefined}
          oauth={!driveResult.ok && driveResult.reason === 'oauth'}
          onChange={keyJson => updateDrive({keyJson})} onUpload={() => keyFile.current?.click()}
          onClear={() => updateDrive({keyJson: ''})} onCopy={() => {if (driveResult.ok) copyEmail(driveResult.draft.email);}}
          onUseOAuth={() => {retireOAuth(oauthRenderTicket);invalidateDrive();setDriveTested(undefined);setDriveMode('oauth');}}/>
        <input ref={keyFile} type="file" accept="application/json,.json" hidden aria-label={t('next.remoteSources.driveUpload')}
          onChange={event => {const file = event.target.files?.[0];void readKeyFile(file);event.target.value = '';}}/>
        </>}

        <div className={styles.lab2}>{t('next.remoteSources.driveLocation')}</div>
        <div className={styles.segment} role="group">
          <Button size="small" variant={drive.location === 'folder' ? 'primary' : 'glass'} aria-pressed={drive.location === 'folder'} disabled={busy} onClick={() => updateDrive({location: 'folder'})}>{t('next.remoteSources.driveFolder')}</Button>
          <Button size="small" variant={drive.location === 'drive' ? 'primary' : 'glass'} aria-pressed={drive.location === 'drive'} disabled={busy} onClick={() => updateDrive({location: 'drive'})}>{t('next.remoteSources.driveDrive')}</Button>
        </div>
        <TextField label={t(drive.location === 'drive' ? 'next.remoteSources.driveDriveId' : 'next.remoteSources.driveFolderId')}
          description={t(drive.location === 'drive' ? 'next.remoteSources.driveDriveHelp' : 'next.remoteSources.driveFolderHelp')}
          value={drive.locationId} maxLength={200} disabled={busy} autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateDrive({locationId: event.target.value})}/>
        <p className={styles.note}>{t('next.remoteSources.driveCannotSee')}</p>
        <p className={styles.note}>{t('next.remoteSources.driveChecks')}</p>
        <div className={styles.testRow}><Button size="compact" variant="glass" disabled={busy || !driveTestReady} onClick={() => void testDrive()}>{busy ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>
          {rcloneResult && <span className={styles.testCard} data-tone="good"><Icon name="i-check" size={18}/>{rcloneResult.version ? t('next.remoteSources.testedRclone', {version: rcloneResult.version}) : t('next.remoteSources.testedRcloneUnknown')}</span>}
        </div>
      </> : sourceKind === 's3' ? <>
        <div className={styles.kind}><Icon name="i-storage" size={22} /><span><b>{t('next.remoteSources.connectS3')}</b><small>{t('next.remoteSources.connectS3Hint')}</small></span></div>
        <TextField label={t('next.remoteSources.name')} value={s3Name} maxLength={200} disabled={busy} onChange={event => setS3Name(event.target.value)}/>
        <TextField label={t('next.remoteSources.s3Endpoint')} description={t('next.remoteSources.s3EndpointHelp')} value={s3.endpoint} placeholder={t('next.remoteSources.s3EndpointPlaceholder')} maxLength={2048} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateS3({endpoint: event.target.value})}/>
        <TextField label={t('next.remoteSources.s3Region')} description={t('next.remoteSources.s3RegionHelp')} value={s3.region} maxLength={32} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateS3({region: event.target.value})}/>
        <TextField label={t('next.remoteSources.s3AccessKey')} value={s3.accessKey} maxLength={256} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateS3({accessKey: event.target.value})}/>
        <TextField label={t('next.remoteSources.s3SecretKey')} type="password" value={s3.secretKey} maxLength={1024} disabled={busy} onChange={event => updateS3({secretKey: event.target.value})}/>
        <TextField label={t('next.remoteSources.s3Bucket')} description={t('next.remoteSources.s3BucketHelp')} value={s3.bucket} maxLength={63} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateS3({bucket: event.target.value})}/>
        <TextField label={t('next.remoteSources.s3Folder')} description={t('next.remoteSources.s3FolderHelp')} value={s3.folder} maxLength={1024} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateS3({folder: event.target.value})}/>
        <p className={styles.note}>{t('next.remoteSources.s3Checks')}</p>
        <div className={styles.testRow}><Button size="compact" variant="glass" disabled={busy || !s3.endpoint.trim() || !s3.accessKey.trim() || !s3.secretKey || !s3.bucket.trim()} onClick={() => void testS3()}>{busy ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>
          {rcloneResult && <span className={styles.testCard} data-tone="good"><Icon name="i-check" size={18}/>{rcloneResult.version ? t('next.remoteSources.testedRclone', {version: rcloneResult.version}) : t('next.remoteSources.testedRcloneUnknown')}</span>}
        </div>
      </> : sourceKind === 'config' ? <>
        <div className={styles.kind}><Icon name="i-storage" size={22} /><span><b>{t('next.remoteSources.connectFromConfig')}</b><small>{t('next.remoteSources.connectFromConfigHint')}</small></span></div>
        <ProgramRow program={program} disabled={busy} onCheck={() => void loadProgram()}/>
        {!configView ? <p className={styles.note} role="status">{t('next.remoteSources.loading')}</p>
          : !configView.remotes.length ? <p className={styles.note} role="status">{t(configView.path ? 'next.remoteSources.configEmpty' : 'next.remoteSources.configAbsent')}</p>
            : <>
              {configView.path && <p className={styles.note}>{t('next.remoteSources.configPath', {path: configView.path})}</p>}
              <RichSelect aria-label={t('next.remoteSources.configRemotes')} value={configRemote} disabled={busy}
                options={configView.remotes.map(remote => ({value: remote.name, label: t('next.remoteSources.configRemoteLabel', {name: remote.name, kind: remote.kind || t('next.remoteSources.configUnknownKind')}),
                  description: remote.supported ? undefined : t(remote.reason === 'encrypted' ? 'next.remoteSources.configEncrypted' : remote.reason === 'wrapper' ? 'next.remoteSources.configWrapper' : remote.reason === 'duplicate' ? 'next.remoteSources.configDuplicate' : remote.reason === 'ambiguous' ? 'next.remoteSources.configAmbiguous' : 'next.remoteSources.configUnsupportedKind'),
                  disabled: !remote.supported}))}
                onValueChange={value => {setConfigRemote(value);setConfigName(current => current || value);}}/>
              <TextField label={t('next.remoteSources.name')} value={configName} maxLength={200} disabled={busy}
                placeholder={configRemote} onChange={event => setConfigName(event.target.value)}/>
              {configView.truncated && <p className={styles.note} role="status">{t('next.remoteSources.configTruncated')}</p>}
              <p className={styles.note}>{t('next.remoteSources.configChecksOnSave')}</p>
            </>}
      </> : <>
        <div className={styles.kind}><Icon name="i-storage" size={22} /><span><b>{t('next.remoteSources.connectRclone')}</b><small>{t('next.remoteSources.connectRcloneHint')}</small></span></div>
        <TextField label={t('next.remoteSources.name')} value={rclone.name} maxLength={100} disabled={busy} onChange={event => updateRclone({name: event.target.value})}/>
        <TextField label={t('next.remoteSources.remoteName')} description={t('next.remoteSources.remoteNameHelp')} value={rclone.remote} placeholder={t('next.remoteSources.remoteNamePlaceholder')} maxLength={200} disabled={busy}
          autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateRclone({remote: event.target.value})}/>
        <label className={styles.configField}><span className={styles.configLabel}>{t('next.remoteSources.configuration')}</span>
          <textarea className={styles.config} aria-label={t('next.remoteSources.configuration')} value={rclone.config} placeholder={t('next.remoteSources.configurationPlaceholder')} maxLength={65536}
            disabled={busy} spellCheck={false} rows={8} onChange={event => updateRclone({config: event.target.value})}/>
          <small className={styles.note}>{t('next.remoteSources.configurationHelp')}</small>
        </label>
        <ProgramRow program={program} disabled={busy} onCheck={() => void loadProgram()}/>
        {providers.length > 0 && <p className={styles.note}>{t('next.remoteSources.providers', {providers: providers.join(', ')})}</p>}
        <p className={styles.note}>{t('next.remoteSources.rcloneKindsNote')}</p>
        <div className={styles.testRow}><Button size="compact" variant="glass" disabled={busy || !rclone.remote.trim() || !rclone.config.trim()} onClick={() => void testRclone()}>{busy ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>
          {rcloneResult && <span className={styles.testCard} data-tone="good"><Icon name="i-check" size={18}/>{rcloneResult.version ? t('next.remoteSources.testedRclone', {version: rcloneResult.version}) : t('next.remoteSources.testedRcloneUnknown')}</span>}
        </div>
      </>}
    </>}
    {step === 'reading' && <>
      {(!wizardReadState || wizardReadState.kind === 'loading') && <p role="status">{t('next.remoteSources.loading')}</p>}
      {wizardReadState?.kind === 'failed' && <p className={styles.error} role="alert">{remoteErrorText(wizardReadState.error)}</p>}
      {wizardReadState?.kind === 'unknown' && <p className={styles.note} role="status">{t('next.remoteSources.unconfirmed')}</p>}
      {(wizardReadState?.kind === 'accepted-unavailable' || wizardReadState?.kind === 'saved-unavailable') && <p className={styles.note} role="status">{t('next.remoteSources.error.refreshFailed')}</p>}
      {wizardReadState?.kind === 'saved' && <p role="status">{t('next.remoteSources.saved')}</p>}
      {wizardReader && !readingCurrent && wizardReadState?.kind !== 'saved' && <p className={styles.note}>{t('next.remoteSources.error.changed')}</p>}
      {wizardReader && readingCurrent && wizardReadState && <SourceReadingFields reader={wizardReader} state={wizardReadState} phone={phone}
        disabled={busy || !['ready', 'failed'].includes(wizardReadState.kind)} advancedOpen={advancedOpen}
        onAdvancedChange={() => {if (wizardAction(renderGuard, 'reading', formToken) && wizardReader.getSnapshot() === wizardReadState) setAdvancedOpen(!advancedOpen);}}/>}
    </>}
    {step === 'folders' && <>
      <p className={styles.note}>{t('next.remoteSources.foldersOnlyHint')}</p>
      <div className={styles.lab2}>{t('next.remoteSources.foldersHeader', {name: sourceNow?.name ?? ''})}</div>
      <ul className={styles.pickedList}>
        {chosen.map(folder => <li key={folder.relative} className={styles.pickedRow}>
          <Icon name="i-folder" size={20}/><span className={styles.pickedPath}>{folder.relative ? `/${folder.relative}` : '/'}</span>
          <Button size="xs" variant="text" onClick={() => setChosen(chosen.filter(value => value.relative !== folder.relative))} aria-label={t('next.remoteSources.removeFolder', {name: folder.name})}><Icon name="i-x" size={16}/></Button>
        </li>)}
        <li className={styles.pickedRow} data-empty={!chosen.length || undefined}>
          {!chosen.length && <span className={styles.pickedPath}>{t('next.remoteSources.noFoldersChosen')}</span>}
          <Button size="small" variant="glass" icon="i-folder" onClick={() => setPicking(true)}>{t('next.remoteSources.chooseFolders')}</Button>
        </li>
      </ul>
      <div className={styles.lab2}>{t('next.remoteSources.addThemTo')}</div>
      <div className={styles.segment} role="group">
        <Button size="small" variant={attachMode === 'new' ? 'primary' : 'glass'} aria-pressed={attachMode === 'new'} onClick={() => setAttachMode('new')}>{t('next.remoteSources.newLibrary')}</Button>
        <Button size="small" variant={attachMode === 'existing' ? 'primary' : 'glass'} aria-pressed={attachMode === 'existing'} onClick={() => setAttachMode('existing')}>{t('next.remoteSources.existingLibrary')}</Button>
      </div>
      {attachMode === 'new' ? <>
        <TextField label={t('next.remoteSources.libraryName')} value={libraryName} maxLength={100} disabled={finishBusy} onChange={event => setLibraryName(event.currentTarget.value)}/>
        <RadioGroup.Root className={styles.kindTiles} aria-label={t('next.remoteSources.libraryKind')} value={kind} disabled={finishBusy}
          onValueChange={value => {const next = libraryKinds.find(kind => kind.id === value);if (!finishBusy && next && wizardAction(renderGuard, 'folders', formToken)) setKind(next.id);}}>
          {libraryKinds.map(value => <RadioGroup.Item key={value.id} value={value.id} className={styles.kindTile}
            data-picked={kind === value.id || undefined}><Icon name={value.icon} size={22}/>{t(value.label)}</RadioGroup.Item>)}
        </RadioGroup.Root>
        <p className={styles.note}>{t(libraryKinds.find(value => value.id === kind)!.hint)}</p>
      </> : (librarySnapshot.directory.data?.items.length
        ? <RichSelect aria-label={t('next.remoteSources.existingLibrary')} value={existingId} disabled={finishBusy}
          options={librarySnapshot.directory.data.items.map(library => ({value: library.id, label: library.name, description: t(libraryKinds.find(value => value.id === library.kind)?.label ?? libraryKinds[0].label)}))}
          onValueChange={setExistingId}/>
        : <p className={styles.note} role="status">{librarySnapshot.directory.phase === 'loading' ? t('next.remoteSources.loading') : t('next.remoteSources.existingLibraryMissing')}</p>)}
      <div className={styles.switchRow}><span>{t('next.remoteSources.scanWhenFinished')}<small className={styles.note}>{t('next.remoteSources.scanWhenFinishedHelp')}</small></span>
        <span className={styles.staticValue}>{t('next.remoteSources.on')}</span></div>
    </>}
    {step === 'done' && <SourceDone
      title={t('next.remoteSources.doneTitle', {name: sourceNow?.name ?? ''})}
      lede={summary.library ? t('next.remoteSources.doneLede', {name: summary.library}) : t('next.remoteSources.summaryNoLibraryHelp')}
      rows={[
        {label: t('next.remoteSources.summaryConnected'), detail: t('next.remoteSources.summaryConnectedHelp')},
        {label: t('next.remoteSources.summaryLimits', {connections: values.connections, requests: values.requestsPerSecond}),
          detail: `${values.bytesPerSecond ? t('next.remoteSources.downloadSpeed') + ': ' + (values.bytesPerSecond / 1_000_000) + ' ' + t('next.remoteSources.megabytesPerSecond') + '. ' : t('next.remoteSources.noLimit') + '. '}${t('next.remoteSources.summaryLimitsHelp', {name: sourceNow?.name ?? ''})}`},
        ...(recordedReading ? [{label: t('next.remoteSources.summaryTreatment', {preset: presetName ? t(presetLabel(presetName)) : t('next.remoteSources.custom')}),
          detail: t('next.remoteSources.summaryTreatmentHelp', {name: sourceNow?.name ?? ''})}] : []),
        summary.library
          ? {label: t('next.remoteSources.summaryLibrary', {library: summary.library}),
            detail: t(summary.folders === 1 ? 'next.remoteSources.summaryLibraryOne' : 'next.remoteSources.summaryLibraryMany', {count: summary.folders, source: sourceNow?.name ?? ''})}
          : {label: t('next.remoteSources.summaryNoLibrary'), detail: t('next.remoteSources.summaryNoLibraryHelp')},
        ...(summary.library ? [{label: t('next.remoteSources.summaryScan'), detail: t('next.remoteSources.summaryScanHelp')}] : []),
      ]}/>}
    {picking && sourceNow && <RemoteFolderPicker service={service} sourceId={sourceNow.id} sourceName={sourceNow.name} open={picking}
      onOpenChange={setPicking} onChoose={folders => setChosen(current => [...current, ...folders.filter(folder => !current.some(value => value.relative === folder.relative))])}/>}
    <Dialog open={leaving !== ''} role="alertdialog" width={520} title={t('next.remoteSources.leaveTitle')}
      onOpenChange={value => {if (!value && !leaveRemove) setLeaving('');}}
      description={leaving === 'saved' ? t('next.remoteSources.leaveSavedBody', {name: sourceNow?.name ?? ''}) : t('next.remoteSources.leaveUnsavedBody')}
      footer={<>
        <Button size="compact" variant="glass" disabled={leaveRemove} onClick={() => setLeaving('')}>{t('next.remoteSources.stayHere')}</Button>
        <Button size="compact" variant="primary" loading={leaveRemove} disabled={leaveRemove}
          onClick={() => {if (leaving === 'saved' && leaveChoice === 'remove') void removeAndLeave(); else close(true);}}>{t('next.remoteSources.leave')}</Button>
      </>}>
      {leaving === 'saved' && <RadioGroup.Root className={styles.leaveRows} aria-label={t('next.remoteSources.leaveTitle')} value={leaveChoice} disabled={leaveRemove}
        onValueChange={value => {if (!leaveRemove && renderGuard() && (value === 'keep' || value === 'remove')) setLeaveChoice(value);}}>
        <RadioGroup.Item value="keep" className={styles.leaveRow}
          data-picked={leaveChoice === 'keep' || undefined}>
          <span className={styles.choiceRadio} aria-hidden="true"/><span><b>{t('next.remoteSources.keepIt')}</b>
            <small>{t('next.remoteSources.keepItHelp', {name: sourceNow?.name ?? ''})}</small></span>
        </RadioGroup.Item>
        <RadioGroup.Item value="remove" className={styles.leaveRow}
          data-picked={leaveChoice === 'remove' || undefined}>
          <span className={styles.choiceRadio} aria-hidden="true"/><span><b>{t('next.remoteSources.removeIt')}</b>
            <small>{t('next.remoteSources.removeItHelp', {name: sourceNow?.name ?? ''})}</small></span>
        </RadioGroup.Item>
      </RadioGroup.Root>}
      {leaveError && <p className={styles.error} role="alert">{leaveError}</p>}
    </Dialog>
  </SourceWizard>;
}


type ManageSourceProps = Readonly<{
  service: RemoteSourceService; source: RemoteSource; api: LibraryContentApi; serverId: string; viewerId: string;
  openSection?: (section: string) => void;
  isCurrent: () => boolean; reportError: (error: unknown) => void; onClose: () => void; onRemoved: () => void;
}>;

/** The dialog's library reader belongs to this opening: created in a layout
 * pass, retired with it, and the body is keyed to that exact resource so a
 * successor opening cannot inherit its state. */
function ManageSourceDialog(props: ManageSourceProps) {
  const owned = useOwnedResource(
    () => new LibraryManagementService({api: props.api, scope: {serverId: props.serverId, viewerId: props.viewerId}}),
    value => value.dispose(),
    // The opening is the service, the source row object and the scope: the
    // parent keys by source id, so a replacement row under the same id must
    // still be a new resource rather than the previous opened one.
    [props.api, props.serverId, props.viewerId, props.service, props.source]);
  if (!owned) return null;
  return <ManageSourceLoaded key={owned.id} {...props} libraries={owned.value} openingId={owned.id}/>;
}

function ManageSourceLoaded({service, source, api, serverId, viewerId, openSection, isCurrent, reportError, onClose, onRemoved, libraries, openingId}: ManageSourceProps & Readonly<{libraries: LibraryManagementService; openingId: number}>) {
  const [draft, setDraft] = useState<WebDavDraft>({name: source.name, root: '', username: '', password: '', insecureLocal: source.insecureLocal});
  const [tab, setTab] = useState('connection');
  const [result, setResult] = useState<DAVConnectionResult>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useLayoutEffect(() => {setBusy(false);}, [openingId]);
  const [typed, setTyped] = useState('');
  const [confirming, setConfirming] = useState(false);
  const [status, setStatus] = useState<RemoteSourceStatus>();
  // The saved test is admitted by a layout-published lease: the exact dialog
  // owner, service, source row and ready status object the render was made
  // for. A replaced source or status retires it whole.
  const [savedTestLease, setSavedTestLease] = useState<SavedTestLease | null>(null);
  const [savedTest, setSavedTest] = useState<Readonly<{result: SavedSourceTest; generation: string}> | null>(null);
  const phone = usePhone();
  const [savedTesting, setSavedTesting] = useState(false);
  const savedTestOwner = useRef<object | null>(null);
  const savedTestLeaseRef = useRef<SavedTestLease | null>(null);
  const savedTestRunning = useRef<SavedTestLease | null>(null);
  useLayoutEffect(() => {
    const owner = {};
    savedTestOwner.current = owner;
    const ready = status;
    const lease = ready && ready.sourceId === source.id && ready.generation
      ? Object.freeze({owner, service, source, status: ready, generation: ready.generation}) : null;
    savedTestLeaseRef.current = lease;
    setSavedTestLease(lease);
    // A new lease is a new admission: nothing from the old one stays displayed.
    setSavedTesting(false);
    setSavedTest(null);
    return () => {
      if (savedTestOwner.current !== owner) return;
      savedTestOwner.current = null;
      savedTestLeaseRef.current = null;
      if (savedTestRunning.current?.owner === owner) savedTestRunning.current = null;
    };
  }, [service, source, status]);
  const [pickingProgram, setPickingProgram] = useState(false);
  const [programNote, setProgramNote] = useState('');
  const librarySnapshot = useSyncExternalStore(libraries.subscribe, libraries.getSnapshot, libraries.getSnapshot);
  const [attachTo, setAttachTo] = useState('');
  const [picking, setPicking] = useState(false);
  const [attachBusy, setAttachBusy] = useState(false);
  const [attachNote, setAttachNote] = useState('');
  const testAttempt = useRef(0), active = useRef(true), pending = useRef(false), lifetime = useRef(0);
  const attemptGuard = () => { const captured = lifetime.current; return () => active.current && lifetime.current === captured && isCurrent(); };
  const profileCurrent = useCallback(() => { /* Preserve the original ownership/invalidation keys. */ void openingId; return active.current && isCurrent(); }, [isCurrent, openingId]);
  // The dialog's admission is layout-owned: a replayed mount admits again, and
  // the cleanup retires this exact render synchronously.
  useLayoutEffect(() => {
    const activeRef = active, lifetimeRef = lifetime;
    activeRef.current = true;
    return () => {activeRef.current=false;lifetimeRef.current++;};
  }, [openingId]);
  const updateDraft = (patch: Partial<WebDavDraft>) => {testAttempt.current++;setResult(undefined);setError('');setDraft(current => ({...current, ...patch}));};
  useEffect(() => {
    // The guard belongs to this rendered dialog, not to a later one.
    const captured = lifetime.current;
    const guard = () => active.current && lifetime.current === captured && isCurrent();
    service.readStatus(source.id, guard).then(next => {if (guard()) setStatus(next);}).catch(value => {if (guard()) {if (authFailure(value)) {active.current = false; reportError(value);} else setError(remoteErrorText(value));}});
    void libraries.loadLibraries();
  }, [service, source.id, source.generation, isCurrent, reportError, libraries, openingId]);
  useEffect(() => {if (!attachTo && status?.libraries.length) setAttachTo(status.libraries[0].libraryId);}, [attachTo, status]);
  useEffect(() => {if (!attachTo && librarySnapshot.directory.data?.items.length) setAttachTo(librarySnapshot.directory.data.items[0].id);}, [attachTo, librarySnapshot.directory.data]);
  const changeProgram = async (choice: Readonly<{useIncluded?: boolean; executable?: string}>) => {
    if (!status?.programRevision) {setProgramNote(t('next.remoteSources.programUnavailable'));return;}
    setProgramNote('');
    try {
      await service.setProgram(source.id, status.programRevision, choice, attemptGuard());
      const next = await service.readStatus(source.id, attemptGuard());
      if (active.current) {setStatus(next);setProgramNote(t('next.remoteSources.programChanged'));}
    } catch (value) {if (active.current) setProgramNote(attachFailure(value));}
  };
  const attach = async (folders: readonly RemoteFolder[]) => {
    if (!attachTo || attachBusy) return;
    setAttachBusy(true); setAttachNote('');
    try {
      for (const folder of folders) {
        const config = await readLibraryInventoryConfig(api, {serverId, viewerId}, attachTo);
        await saveLibraryFolder(api, attachTo, undefined, {name: folder.name, path: folder.path, classification: 'local', followSymlinks: false, intervalSeconds: SIX_HOURS, expectedRevision: config.revision, acceptReplacement: false});
      }
      const next = await service.readStatus(source.id, attemptGuard());
      if (active.current) setStatus(next);
    } catch (value) {if (active.current) setAttachNote(attachFailure(value));}
    finally {if (active.current) setAttachBusy(false);}
  };
  const test = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard()) return;
    pending.current = true;
    const attempt = ++testAttempt.current, captured = {...draft};
    setError('');setResult(undefined);setBusy(true);
    try {const next = await service.testConnection({name: captured.name.trim(), root: captured.root.trim(), username: captured.username, password: captured.password, insecureLocal: captured.insecureLocal}, guard);
      if (guard() && attempt === testAttempt.current) setResult(next);}
    catch (value) {if (guard() && attempt === testAttempt.current) setError(remoteErrorText(value));}
    finally {if (guard() && attempt === testAttempt.current) {pending.current=false;setBusy(false);}}
  };
  const saveConnection = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard()) return;
    pending.current = true;
    setError('');setBusy(true);
    try {
      await service.configure(source.id, davConfiguration(source, draft), source, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (state.error) {setError(state.error);return;}
      done();
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const disconnect = async () => {
    const guard = attemptGuard();
    if (pending.current || !guard() || typed.trim() !== source.name) return;
    const current = service.getSnapshot().sources.find(value => value.id === source.id && value.generation === source.generation);
    if (!current) {setError(remoteErrorText({code: 'revision_mismatch'}));return;}
    pending.current = true;setError('');setBusy(true);
    try {await service.remove(current, guard);const state = service.getSnapshot();if (!guard()) return;if (state.error) {setError(state.error);return;}active.current=false;lifetime.current++;onRemoved();}
    finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const done = () => {
    if (!active.current || !isCurrent()) return;
    active.current=false;pending.current=false;lifetime.current++;
    // Retire the test admission with the close gesture, before the unmount
    // layout runs: a retained Test cannot be admitted in between.
    savedTestOwner.current=null;
    savedTestLeaseRef.current=null;
    savedTestRunning.current=null;
    setDraft(current => ({...current, password: ''}));setTyped('');onClose();
  };
  const close = () => {if (busy || pending.current) return;done();};
  /** A saved-source test is its own command; its answer names the effective
   * observation generation, which must still be the one this page reports. */
  /** Runs only the lease the pressed button was rendered with; never a newer one. */
  const testSavedSource = async (lease: SavedTestLease) => {
    // The lease itself is the single-flight claim: a second press while this
    // one runs finds it taken.
    if (savedTestLeaseRef.current !== lease || savedTestRunning.current || pending.current || busy) return;
    const owns = () => savedTestLeaseRef.current === lease && savedTestOwner.current === lease.owner
      && savedTestRunning.current === lease && isCurrent()
      && status === lease.status && service.getSnapshot().sources.indexOf(lease.source) >= 0;
    savedTestRunning.current = lease;
    if (!owns()) { savedTestRunning.current = null; return; }
    setSavedTesting(true);setSavedTest(null);setError('');
    try {
      const result = await service.testSaved(lease.source.id, owns);
      if (!owns() || result.sourceId !== lease.source.id || result.generation !== lease.generation) return;
      setSavedTest({result, generation: lease.generation});
    } catch (value) {
      if (!owns()) return;
      // A failure in Portico's own local helper is not a provider answer:
      // the server reports administration_unavailable, with no contact fact.
      const code = value && typeof value === 'object' ? (value as {code?: unknown}).code : undefined;
      setError(code === 'administration_unavailable' ? t('next.remoteSources.savedTestLocalFailure') : remoteErrorText(value));
    }
    finally {
      if (savedTestRunning.current === lease) {
        savedTestRunning.current = null;
        if (savedTestLeaseRef.current === lease && isCurrent()) setSavedTesting(false);
      }
    }
  };
  const connection = <>
    <SourceHeader label={t('next.remoteSources.status')} phone={phone} status={<SourceStatus source={source}/>} origin={source.origin}
      notice={problemId(source) ? t(problemId(source)!) : undefined}>
      <dl className={styles.statusRows}>
        <div><dt>{t('next.remoteSources.rightNow')}</dt><dd>{!status ? '—'
          : status.connectionsInUse === 0 ? t('next.remoteSources.nothingReading')
            : status.playbackReads > 0 ? t('next.remoteSources.readingNow', {watching: status.playbackReads, connections: status.connectionsInUse})
              : t('next.remoteSources.connectionsNow', {connections: status.connectionsInUse})}</dd></div>
        <div><dt>{t('next.remoteSources.lastChecked')}</dt><dd>{!status ? '—' : status.lastCheckedAt
          ? t('next.remoteSources.checkedAt', {when: checkedWhen(status.lastCheckedAt)})
          : t('next.remoteSources.neverChecked')}</dd></div>
        <div><dt>{t('next.remoteSources.usedBy')}</dt><dd>{!status ? '—'
          : status.libraries.length
            ? <MetadataFacts parts={[...status.libraries.map(use => <MetadataFacts key={use.libraryId} parts={[use.name, use.folders,
              use.lastCompleteAt ? t('next.remoteSources.libraryComplete', {when: checkedWhen(use.lastCompleteAt)}) : undefined]}/>), status.librariesTruncated ? '…' : undefined]}/>
            : t('next.remoteSources.noLibraryUses')}</dd></div>
        {!!status?.scanIntervalSeconds && <div><dt>{t('next.remoteSources.scanning')}</dt>
          <dd>{t('next.remoteSources.scansEveryHours', {hours: Math.round(status.scanIntervalSeconds / 3600) || 1})}</dd></div>}
        <div><dt>{t('next.remoteSources.savedTest')}</dt><dd><span className={styles.rowActions}>
          <Button size="xs" variant="glass" loading={savedTesting} disabled={busy || savedTesting || !savedTestLease}
            onClick={() => {if (savedTestLease) void testSavedSource(savedTestLease);}}>{t('next.remoteSources.test')}</Button>
          {savedTest && savedTestLease?.status === status && savedTest.generation === status?.generation && <span className={styles.testCard} data-tone={savedTest.result.reachable ? 'good' : 'warning'}>{savedTest.result.reachable
            ? t(savedTest.result.rangeSupport === 'unsupported' ? 'next.remoteSources.savedTestLimited' : 'next.remoteSources.savedTestReachable')
            : t(contactWord(savedTest.result.errorCode!))}</span>}
        </span></dd></div>
        {status?.lastConnectedAt && <div><dt>{t('next.remoteSources.lastConnected')}</dt><dd>{checkedWhen(status.lastConnectedAt)}</dd></div>}
        {status?.lastErrorAt && status.lastErrorCode && <div><dt>{t('next.remoteSources.lastError')}</dt><dd><MetadataFacts parts={[checkedWhen(status.lastErrorAt), t(contactWord(status.lastErrorCode))]}/></dd></div>}
        {status?.health && <div><dt>{t('next.remoteSources.health')}</dt><dd>{t(healthWord(status.health))}</dd></div>}
        {status?.lastCompleteAt && <div><dt>{t('next.remoteSources.lastComplete')}</dt><dd>{checkedWhen(status.lastCompleteAt)}</dd></div>}
        {status?.scan && <>
          <div><dt>{t('next.remoteSources.scanState')}</dt><dd><MetadataFacts parts={[scanStateWord(status.scan.state), scanModeWord(status.scan.mode), scanPhaseWord(status.scan.phase)]
            .filter((word): word is MessageId => Boolean(word)).map(word => t(word))}/></dd></div>
          <div><dt>{t('next.remoteSources.scanFiles')}</dt><dd>{t('next.remoteSources.scanProcessedOf', {processed: status.scan.processed, discovered: status.scan.discovered})}</dd></div>
          <div><dt>{t('next.remoteSources.scanAnalyzed')}</dt><dd>{status.scan.analyzed}</dd></div>
          {status.scan.warnings > 0 && <div><dt>{t('next.remoteSources.scanWarnings')}</dt><dd>{status.scan.warnings}</dd></div>}
          {status.scan.queuedAt && <div><dt>{t('next.remoteSources.scanQueuedAt')}</dt><dd>{checkedWhen(status.scan.queuedAt)}</dd></div>}
          {status.scan.completedAt && <div><dt>{t('next.remoteSources.scanListed')}</dt><dd>{checkedWhen(status.scan.completedAt)}</dd></div>}
          {status.scan.finishedAt && <div><dt>{t('next.remoteSources.scanFinished')}</dt><dd>{checkedWhen(status.scan.finishedAt)}</dd></div>}
        </>}
      </dl>
    </SourceHeader>
    <section className={styles.group} id="source-connection"><h3>{t('next.remoteSources.connection')}</h3>
      <TextField label={t('next.remoteSources.name')} value={draft.name} maxLength={100} disabled={busy} onChange={event => updateDraft({name: event.target.value})}/>
      <TextField label={t('next.remoteSources.address')} value={draft.root} placeholder={source.origin ?? t('next.settingsSpec.server.remote-sources.panels.source.tabs.0.groups.0.rows.1.control.placeholder')} disabled={busy}
        autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateDraft({root: event.target.value})}/>
      <TextField label={t('next.remoteSources.username')} value={draft.username} disabled={busy} autoCapitalize="off" spellCheck={false} onChange={event => updateDraft({username: event.target.value})}/>
      <TextField label={t('next.remoteSources.password')} type="password" value={draft.password} disabled={busy} onChange={event => updateDraft({password: event.target.value})}/>
      <label className={styles.switchRow}><span>{t('next.remoteSources.insecureLocal')}</span><Switch aria-label={t('next.remoteSources.insecureLocal')} checked={draft.insecureLocal} disabled={busy} onCheckedChange={value => updateDraft({insecureLocal: value})}/></label>
      <div className={styles.testRow}><Button size="compact" variant="glass" disabled={busy || !draft.name.trim() || !draft.root.trim()} onClick={() => void test()}>{busy ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>
        {result && <span className={styles.testCard} data-tone={result.rangeSupport === 'unsupported' ? 'warning' : 'good'}><Icon name="i-check" size={18}/>{t(result.rangeSupport === 'unsupported' ? 'next.remoteSources.testedLimited' : 'next.remoteSources.tested', {origin: result.origin})}</span>}
      </div>
      <Button size="compact" variant="primary" disabled={busy || !draft.name.trim() || (!result && !draft.root.trim())} onClick={() => void saveConnection()}>{t('next.remoteSources.save')}</Button>
    </section>
    <section className={styles.danger}><h3>{t('next.remoteSources.disconnect')}</h3><p>{t('next.remoteSources.disconnectBody')}</p>
      {!confirming ? <Button size="compact" variant="text" onClick={() => setConfirming(true)}>{t('next.remoteSources.disconnect')}</Button> : <>
        <p>{t('next.remoteSources.disconnectConfirm')}</p>
        <TextField label={t('next.remoteSources.name')} value={typed} disabled={busy} onChange={event => setTyped(event.target.value)}/>
        <div className={styles.actions}><Button size="compact" variant="glass" disabled={busy} onClick={() => {setConfirming(false);setTyped('');}}>{t('next.remoteSources.cancel')}</Button>
          <Button size="compact" variant="primary" disabled={busy || typed.trim() !== source.name} onClick={() => void disconnect()}>{t('next.remoteSources.disconnect')}</Button></div>
      </>}
    </section>
  </>;
  const usedBy = <>
    <section className={styles.group}><h3>{t('next.remoteSources.foldersSection')}</h3>
      <p className={styles.note}>{t('next.remoteSources.foldersNote')}</p>
      {status?.program && <dl className={styles.statusRows}>
        <div><dt>{t('next.remoteSources.program')}</dt><dd><MetadataFacts parts={[status.program, status.programVersion]}/></dd></div>
        {status.remote && <div><dt>{t('next.remoteSources.remote')}</dt><dd className={styles.mono}>{status.remote}</dd></div>}
        {status.backgroundSpeed && <div><dt>{t('next.remoteSources.backgroundSpeed')}</dt><dd><MetadataFacts parts={[status.backgroundSpeed, t('next.remoteSources.backgroundSpeedHelp')]}/></dd></div>}
      </dl>}
      {!status ? <p className={styles.note} role="status">{error || t('next.remoteSources.loading')}</p> : status.libraries.length
        ? <ul className={styles.usedList}>{status.libraries.map(use => <li key={use.libraryId}>
          <Icon name="i-library" size={18}/><span>{use.name}<small>{t('next.remoteSources.foldersInLibrary', {count: use.folders})}</small></span></li>)}</ul>
        : <p className={styles.note}>{t('next.remoteSources.noLibraryUses')}</p>}
      {!!librarySnapshot.directory.data?.items.length && <div className={styles.attachRow}>
          <RichSelect aria-label={t('next.remoteSources.attachTo')} value={attachTo} disabled={attachBusy}
            options={librarySnapshot.directory.data.items.map(library => ({value: library.id, label: library.name, description: t(libraryKinds.find(value => value.id === library.kind)?.label ?? libraryKinds[0].label)}))}
            onValueChange={setAttachTo}/>
          <Button size="small" variant="glass" icon="i-folder" disabled={attachBusy || !attachTo} onClick={() => setPicking(true)}>{t('next.remoteSources.chooseFolders')}</Button>
        </div>}
      {attachNote && <p className={styles.error} role="alert">{attachNote}</p>}
      {status?.librariesTruncated && <p className={styles.note}>{t('next.remoteSources.usedBy')} …</p>}
      {openSection && <Button size="small" variant="glass" onClick={() => openSection('libraries')}>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.2.groups.0.rows.1.label')}</Button>}
    </section>
    {picking && <RemoteFolderPicker service={service} sourceId={source.id} sourceName={source.name} open={picking}
      onOpenChange={setPicking} onChoose={folders => {setPicking(false);void attach(folders);}}/>}
  </>;
  const advanced = <>
    <section className={styles.group}><h3>{t('next.remoteSources.connection')}</h3>
      {status?.program && <div className={styles.programRows}>
        <p className={styles.advTitle}>{t('next.remoteSources.connection')}</p>
        <label className={styles.switchRow}><span>{t('next.remoteSources.customProgram')}<small className={styles.note}>{t('next.remoteSources.customProgramHelp')}</small></span>
          <Switch aria-label={t('next.remoteSources.customProgram')} checked={!status.programIncluded} disabled={!status.programRevision}
            onCheckedChange={value => {if (!value) void changeProgram({useIncluded: true}); else setPickingProgram(true);}}/></label>
        <div className={styles.programRow}><span>{t('next.remoteSources.program')}</span>
          <MetadataFacts className={styles.mono} parts={[status.program, status.programVersion]}/>
          <Button size="xs" variant="glass" onClick={() => setPickingProgram(true)}>{t('next.remoteSources.chooseProgram')}</Button></div>
        {status.remote && <div className={styles.programRow}><span>{t('next.remoteSources.remote')}</span>
          <span className={styles.mono}>{status.remote}</span>
          <Button size="xs" variant="glass" onClick={() => setTab('connection')}>{t('next.remoteSources.change')}</Button></div>}
        {status.backgroundSpeed && <div className={styles.programRow}><span>{t('next.remoteSources.backgroundSpeed')}</span>
          <span>{status.backgroundSpeed}</span>
          <Button size="xs" variant="glass" disabled={!openSection} onClick={() => openSection?.('storage')}>{t('next.remoteSources.changeInSchedule')}</Button></div>}
        {programNote && <p className={styles.note} role="status">{programNote}</p>}
      </div>}
      {pickingProgram && <ProgramPicker open={pickingProgram} api={api} startAt={status?.program ?? ''} onOpenChange={setPickingProgram} onChoose={path => void changeProgram({executable: path})}/>}
    </section>
  </>;
  return <SourceReadingDialog embedded service={service} source={source} isCurrent={profileCurrent} reportError={reportError}
    onClose={done} errorText={remoteErrorText} isAuthFailure={authFailure} render={panels => <SettingsPanel open onClose={close}
      title={source.name} audience={t('next.shell.editorAudience.owner')} error={error || undefined} selectedTab={tab} onTabChange={setTab}
      tabs={[
        {id: 'connection', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.0.title'), content: connection, keepMounted: true},
        {id: 'reading', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.1.title'), content: panels.reading, keepMounted: true},
        {id: 'used', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.2.title'), content: usedBy, keepMounted: true},
        {id: 'advanced', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.title'), content: <>{advanced}{panels.advanced}</>, keepMounted: true},
      ]}/>}/>;
}

/** An imported source is replaced whole: the server needs its configuration
 * again and never reads the stored one back to the page. */
function ManageRcloneDialog({service, source, openSection, isCurrent, reportError, onClose, onRemoved}: Readonly<{
  service: RemoteSourceService; source: RemoteSource; openSection?: (section: string) => void; isCurrent: () => boolean; reportError: (error: unknown) => void; onClose: () => void; onRemoved: () => void;
}>) {
  const [draft, setDraft] = useState<RcloneDraft>({name: source.name, remote: '', config: ''});
  const [tab, setTab] = useState('connection');
  const [status, setStatus] = useState<RemoteSourceStatus>();
  const [program, setProgram] = useState<RcloneProgram>();
  const [result, setResult] = useState<RcloneTestResult>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [typed, setTyped] = useState('');
  const [confirming, setConfirming] = useState(false);
  const testAttempt = useRef(0), pending = useRef(false);
  const ownedLease = useOwnedResource(() => ({live: true, service, sourceId: source.id, generation: source.generation, isCurrent, reportError, onClose, onRemoved}),
    value => {value.live = false;}, [service, source, isCurrent]);
  const lease = ownedLease?.value;
  useLayoutEffect(() => {setBusy(false);}, [lease]);
  const profileCurrent = useCallback(() => !!lease && lease.live && lease.isCurrent(), [lease]);
  const held = (captured: typeof lease) => () => {try {return !!captured && captured.live && captured.isCurrent();} catch {return false;}};
  const retire = useCallback(() => {if (lease) lease.live = false;pending.current = false;}, [lease]);
  const refuse = useCallback((error: unknown) => {if (!held(lease)()) return;retire();lease?.reportError(error);}, [lease, retire]);
  const stillListed = (captured: typeof lease) => !!captured && held(captured)() && service.getSnapshot().sources.some(row => row.id === captured.sourceId && row.generation === captured.generation);
  const updateDraft = (patch: Partial<RcloneDraft>) => {testAttempt.current++;setResult(undefined);setError('');setDraft(current => ({...current, ...patch}));};
  useEffect(() => {
    // The guard stays with this lease, not with a later source or service.
    const captured = lease;
    const guard = held(captured);
    if (!guard()) return;
    setStatus(undefined);
    service.readStatus(source.id, guard).then(value => {if (guard()) setStatus(value);}).catch(value => {if (guard()) {if (authFailure(value)) refuse(value); else setError(remoteErrorText(value));}});
    service.rcloneProgram().then(next => {if (guard()) setProgram(next);}).catch(value => {
      if (guard() && authFailure(value)) {refuse(value);}
    });
  }, [service, source.id, source.generation, isCurrent, lease, refuse]);
  const test = async () => {
    const guard = held(lease);
    if (pending.current || !guard()) return;
    pending.current = true;
    const attempt = ++testAttempt.current, captured = {...draft};
    setError('');setResult(undefined);setBusy(true);
    try {const next = await service.testRclone({remote: captured.remote.trim(), config: captured.config}, guard);
      if (guard() && attempt === testAttempt.current) setResult(next);}
    catch (value) {
      if (!guard() || attempt !== testAttempt.current) return;
      if (authFailure(value)) {refuse(value);return;}
      setError(remoteErrorText(value));
    }
    finally {if (guard() && attempt === testAttempt.current) {pending.current=false;setBusy(false);}}
  };
  const saveConnection = async () => {
    const guard = held(lease);
    if (pending.current || !guard()) return;
    if (!stillListed(lease)) {setError(remoteErrorText({code: 'revision_mismatch'}));return;}
    pending.current = true;
    setError('');setBusy(true);
    const outgoing = {name: draft.name.trim(), remote: draft.remote.trim(), config: draft.config};
    // The configuration leaves the form as the mutation starts; only the request holds it now.
    setDraft(current => ({...current, config: ''}));
    try {
      const receipt = await service.configureRclone(source.id, outgoing, source, guard);
      if (!guard()) return;
      const state = service.getSnapshot();
      if (!receipt) {
        if (authFailure({code: state.errorCode})) {refuse({code: state.errorCode});return;}
        setError(state.error || t('next.remoteSources.unconfirmed'));return;
      }
      done();
    } finally {if (guard()) {pending.current=false;setBusy(false);}}
  };
  const done = () => {if (!held(lease)()) return;retire();setDraft(current => ({...current, config: ''}));setTyped('');lease?.onClose();};
  const close = () => {if (busy || pending.current) return;done();};
  if (!lease || !held(lease)()) return null;
  const connection = <>
    <section className={styles.group} id="source-connection"><h3>{t('next.remoteSources.connection')}</h3>
      <p className={styles.note}><SourceStatus source={source}/> {t('next.remoteSources.kindRclone')}</p>
      <TextField label={t('next.remoteSources.name')} value={draft.name} maxLength={100} disabled={busy} onChange={event => updateDraft({name: event.target.value})}/>
      <TextField label={t('next.remoteSources.remoteName')} description={t('next.remoteSources.remoteNameHelp')} value={draft.remote} placeholder={t('next.remoteSources.remoteNamePlaceholder')} maxLength={200} disabled={busy}
        autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => updateDraft({remote: event.target.value})}/>
      <label className={styles.configField}><span className={styles.configLabel}>{t('next.remoteSources.configuration')}</span>
        <textarea className={styles.config} aria-label={t('next.remoteSources.configuration')} value={draft.config} placeholder={t('next.remoteSources.replaceConfiguration')} maxLength={65536}
          disabled={busy} spellCheck={false} rows={8} onChange={event => updateDraft({config: event.target.value})}/>
        <small className={styles.note}>{t('next.remoteSources.configurationHelp')}</small>
      </label>
      <ProgramRow program={program} disabled={busy}/>
      <div className={styles.testRow}><Button size="compact" variant="glass" disabled={busy || !draft.remote.trim() || !draft.config.trim()} onClick={() => void test()}>{busy ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>
        {result && <span className={styles.testCard} data-tone="good"><Icon name="i-check" size={18}/>{result.version ? t('next.remoteSources.testedRclone', {version: result.version}) : t('next.remoteSources.testedRcloneUnknown')}</span>}
      </div>
      <Button size="compact" variant="primary" disabled={busy || !draft.name.trim() || !draft.remote.trim() || !draft.config.trim()} onClick={() => void saveConnection()}>{t('next.remoteSources.save')}</Button>
    </section>
  </>;
  return <SourceReadingDialog embedded service={service} source={source} isCurrent={profileCurrent} reportError={reportError}
    onClose={done} errorText={remoteErrorText} isAuthFailure={authFailure} render={panels => <SettingsPanel open onClose={close}
      title={source.name} audience={t('next.shell.editorAudience.owner')} error={error || undefined} selectedTab={tab} onTabChange={setTab}
      tabs={[
        {id: 'connection', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.0.title'), content: connection, keepMounted: true},
        {id: 'reading', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.1.title'), content: panels.reading, keepMounted: true},
        {id: 'used', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.2.title'), content: <section className={styles.group}>
          {!status ? <p className={styles.note} role="status">{error || t('next.remoteSources.loading')}</p> : status.libraries.length
            ? <ul className={styles.usedList}>{status.libraries.map(use => <li key={use.libraryId}><Icon name="i-library" size={18}/><span>{use.name}<small>{t('next.remoteSources.foldersInLibrary', {count: use.folders})}</small></span></li>)}</ul>
            : <p className={styles.note}>{t('next.remoteSources.noLibraryUses')}</p>}
          {status?.librariesTruncated && <p className={styles.note}>{t('next.remoteSources.usedBy')} …</p>}
          {openSection && <Button size="small" variant="glass" onClick={() => openSection('libraries')}>{t('next.settingsSpec.server.remote-sources.panels.source.tabs.2.groups.0.rows.1.label')}</Button>}
        </section>},
        {id: 'advanced', title: t('next.settingsSpec.server.remote-sources.panels.source.tabs.3.title'), content: panels.advanced, keepMounted: true},
      ]}/>}/>;
}
