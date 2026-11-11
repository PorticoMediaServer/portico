import {useCallback, useEffect, useRef, useState, type RefObject} from 'react';
import {RadioGroup} from 'radix-ui';
import {defaultI18n, type MessageId} from '@i18n';
import {
  addCloudTokenSource, addRcloneRemoteFromFields, addRemoteFolderToLibrary, addRemoteSource, changeRemoteSource, createLibraryOnRemoteFolder,
  defaultRemotePreset, isNotOnServerYet, listAdminLibraries, listRemoteSources, remotePresets, remoteSourceCreate, remoteSourceFolders,
  removeRemoteSource, startDriveSignIn, testRemoteSource, type RemoteFolder, type RemoteLimits, type RemotePreset, type RemoteSource,
} from '@core/remote-storage.ts';
import type {ServerRequester} from '@core/query/server-api.ts';
import type {AdminLibrary} from '../../../../../packages/contracts/src/next/api.ts';
import {Button, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {SettingsGroup} from '../../../kit/settings';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import {Icon} from '../../../kit/Icon';
import {MetadataFacts} from '../../../kit/MetadataFacts';
import styles from './remote-sources.module.css';
import {SourceWizard} from './SourceWizard';
import {SourceDone} from './SourceDone';

/**
 * Server › Remote sources, on the new server (definition 03, rules 7 to 14). Only the owner reaches it (rule 7).
 * A source is WebDAV, an rclone remote pasted from rclone's configuration, OneDrive or Dropbox with a token pasted
 * from rclone, a new rclone remote, or Google Drive with the owner's own key or OAuth client (rules 8, 9). Nothing is
 * saved until Test connection succeeds; a source left before a folder of it is in a library is "Setup not finished"
 * with Continue setup (rule 10). A source has one of four presets and its own limits (rules 11, 12).
 */
const t = defaultI18n.t;
export type RemoteSourcesPageProps = Readonly<{
  api: ServerRequester; serverId: string; viewerId: string; isCurrent: () => boolean; reportError: (error: unknown) => void;
  openSection?: (section: string) => void;
}>;

const stateWords: Record<string, MessageId> = {
  connected: 'next.remoteSources.state.ready', setup: 'next.remoteSources.state.setup', unreachable: 'next.remoteSources.state.unreachable',
  sign_in: 'next.remoteSources.state.credentials', waiting: 'next.remoteSources.state.waiting', limited: 'next.remoteSources.state.limited',
  needs_attention: 'next.remoteSources.state.attention',
};
const stateTone = (source: RemoteSource): 'healthy' | 'warning' | 'danger' | 'neutral' =>
  source.state === 'connected' ? 'healthy' : source.state === 'waiting' || source.state === 'limited' || source.state === 'setup' ? 'warning'
    : source.state === 'unreachable' || source.state === 'sign_in' || source.state === 'needs_attention' ? 'danger' : 'neutral';
function SourceStatus({source}: {source: RemoteSource}) {
  return <span className={styles.badge} data-tone={stateTone(source)}>{t(stateWords[source.state] ?? 'next.remoteSources.state.problem')}</span>;
}
const presetLabel = (preset: RemotePreset): MessageId => (`next.settingsSpec.server.remote-sources.panels.newSource.tabs.1.groups.0.rows.0.control.options.${remotePresets.findIndex(one => one.id === preset)}.label`) as MessageId;
const kindLabel = (source: RemoteSource): MessageId => source.kind === 'rclone' ? 'next.remoteSources.kindRclone' : source.kind === 'drive' ? 'next.remoteSources.connectDrive' : 'next.remoteSources.kindWebdav';

/** The server's refusal in the owner's words: its own message when it names what failed, otherwise a plain line. */
export function remoteErrorText(error: unknown): string {
  if (isNotOnServerYet(error)) return t('next.remoteSources.notOnServerYet');
  const code = error && typeof error === 'object' && typeof (error as {code?: unknown}).code === 'string' ? (error as {code: string}).code : '';
  if (code === 'unauthorized') return t('next.remoteSources.error.signIn');
  if (code === 'forbidden') return t('next.remoteSources.error.forbidden');
  // A field refusal (an address that did not answer, a configuration that is not one remote) and a source a library still
  // uses are the server's own sentence.
  const message = (error as {message?: unknown} | null)?.message;
  if (['invalid_field', 'invalid_request', 'remote_unreachable', 'source_in_use'].includes(code) && typeof message === 'string' && message.trim() && message.length < 300) return message;
  if (code === 'invalid_field' || code === 'invalid_request') return t('next.remoteSources.error.invalid');
  return t('next.remoteSources.error.generic');
}

export function RemoteSourcesPage({api, isCurrent, reportError}: RemoteSourcesPageProps) {
  const [sources, setSources] = useState<readonly RemoteSource[]>(), [error, setError] = useState('');
  const [adding, setAdding] = useState(false), [managing, setManaging] = useState<RemoteSource>(), [continuing, setContinuing] = useState<RemoteSource>();
  const addButton = useRef<HTMLButtonElement>(null);
  const load = useCallback(() => {
    void listRemoteSources(api).then(list => {if (isCurrent()) {setSources(list); setError('');}}, failure => {if (isCurrent()) setError(remoteErrorText(failure));});
    // isCurrent is read when the answer arrives; a new one each render must not read the list again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api]);
  useEffect(load, [load]);
  const needing = (sources ?? []).filter(source => stateTone(source) === 'danger');
  return <div className={styles.page}>
    <div className={styles.sourceList} hidden={adding}>
      {needing[0] && <div className={styles.pageNotice} role="status"><Icon name="i-warn" size={20}/>
        <span><b>{t('next.remoteSources.problemHeading')}</b> {needing[0].name}: {needing[0].problem ?? t(stateWords[needing[0].state] ?? 'next.remoteSources.state.problem')}
          <Button size="xs" variant="text" onClick={() => setManaging(needing[0])}>{t('next.remoteSources.openSource', {name: needing[0].name})}</Button></span></div>}
      <SettingsGroup title={t('next.settingsSpec.server.remote-sources.groups.0.title')}
        actions={<Button ref={addButton} size="compact" variant="primary" icon="i-plus" onClick={() => setAdding(true)}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.addLabel')}</Button>}>
        {error && <p className={styles.error} role="alert">{error}</p>}
        {sources === undefined && !error && <p className={styles.muted} role="status">{t('next.remoteSources.loading')}</p>}
        {sources?.length === 0 && <div className={styles.empty}><b>{t('next.remoteSources.empty')}</b><p>{t('next.remoteSources.emptyHelp')}</p></div>}
        {!!sources?.length && <p className={styles.sectionLabel}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.label')}</p>}
        {sources?.map(source => <SettingsRow key={source.id} label={<><Icon name="i-globe" size={18}/> {source.name}</>}
          help={<MetadataFacts parts={[t(kindLabel(source)), source.webdav?.address ?? source.rclone?.type, source.problem]}/>}>
          <span className={styles.rowActions}><SourceStatus source={source}/>
            {source.state === 'setup' && <Button size="small" variant="primary" onClick={() => setContinuing(source)}>{t('next.remoteSources.continueSetup')}</Button>}
            <Button size="small" variant="glass" onClick={() => setManaging(source)}>{t('next.settingsSpec.server.remote-sources.groups.0.rows.0.control.itemActions.0.label')}</Button></span>
        </SettingsRow>)}
      </SettingsGroup>
    </div>
    <AddSourceDialog api={api} open={adding} returnFocusRef={addButton} isCurrent={isCurrent} reportError={reportError}
      onClose={() => {setAdding(false); load();}} onCreated={load}/>
    {managing && <ManageSourceDialog key={managing.id} api={api} source={managing} isCurrent={isCurrent}
      onClose={() => {setManaging(undefined); load();}} onContinue={source => {setManaging(undefined); setContinuing(source);}}/>}
    {continuing && <Dialog open onOpenChange={open => {if (!open) {setContinuing(undefined); load();}}} title={t('next.remoteSources.foldersHeader', {name: continuing.name})}>
      <FolderStep api={api} source={continuing} isCurrent={isCurrent} onDone={() => {setContinuing(undefined); load();}}/>
    </Dialog>}
  </div>;
}

type Kind = 'webdav' | 'rclone' | 'cloud' | 'newRclone' | 'drive';
const kinds: readonly Readonly<{id: Kind; label: MessageId; hint: MessageId}>[] = [
  {id: 'webdav', label: 'next.remoteSources.connectWebdav', hint: 'next.remoteSources.kindCommon'},
  {id: 'rclone', label: 'next.remoteSources.connectRclone', hint: 'next.remoteSources.connectRcloneHint'},
  {id: 'cloud', label: 'next.remoteSources.connectCloud', hint: 'next.remoteSources.connectCloudHint'},
  {id: 'newRclone', label: 'next.remoteSources.remoteKind', hint: 'next.remoteSources.kindIncluded'},
  {id: 'drive', label: 'next.remoteSources.connectDrive', hint: 'next.remoteSources.connectDriveHint'},
];
type Step = 'what' | 'remote' | 'reading' | 'folders' | 'done';
type Fields = {name: string; address: string; user: string; password: string; config: string; path: string; token: string; provider: 'onedrive' | 'dropbox';
  remoteType: string; options: string; driveMode: 'key' | 'oauth'; key: string; clientId: string; clientSecret: string; signIn: string; signInAddress: string; redirectedUrl: string};
const emptyFields: Fields = {name: '', address: '', user: '', password: '', config: '', path: '', token: '', provider: 'onedrive', remoteType: '', options: '',
  driveMode: 'key', key: '', clientId: '', clientSecret: '', signIn: '', signInAddress: '', redirectedUrl: ''};

type AddSourceProps = Readonly<{api: ServerRequester; open: boolean; returnFocusRef?: RefObject<HTMLButtonElement | null>; isCurrent: () => boolean;
  reportError: (error: unknown) => void; onClose: () => void; onCreated: (source: RemoteSource) => void}>;

/** The wizard: what to connect, its details tested and saved together, how Portico reads it, a folder for a library, done. */
export function AddSourceDialog({api, open, returnFocusRef, isCurrent, onClose, onCreated}: AddSourceProps) {
  const [step, setStep] = useState<Step>('what'), [kind, setKind] = useState<Kind>('webdav'), [fields, setFields] = useState<Fields>(emptyFields);
  const [created, setCreated] = useState<RemoteSource>(), [busy, setBusy] = useState(false), [error, setError] = useState('');
  const [preset, setPreset] = useState<RemotePreset>(defaultRemotePreset), [limits, setLimits] = useState<RemoteLimits>(remotePresets[1]!.limits);
  useEffect(() => {if (!open) {setStep('what'); setFields(emptyFields); setCreated(undefined); setError(''); setPreset(defaultRemotePreset);}}, [open]);
  const set = (patch: Partial<Fields>) => setFields(value => ({...value, ...patch}));
  const run = (work: () => Promise<void>) => {
    if (busy) return;
    setBusy(true); setError('');
    void work().catch(failure => {if (isCurrent()) setError(remoteErrorText(failure));}).finally(() => {if (isCurrent()) setBusy(false);});
  };
  // Test connection and save are one step: the server saves only a source that works (rule 10).
  const testAndSave = () => run(async () => {
    let source: RemoteSource;
    if (kind === 'cloud') source = await addCloudTokenSource(api, {name: fields.name.trim(), provider: fields.provider, token: fields.token, path: fields.path, preset});
    else if (kind === 'newRclone') {
      const options = Object.fromEntries(fields.options.split('\n').map(line => line.split('=')).filter(parts => parts.length >= 2).map(([key, ...rest]) => [key!.trim(), rest.join('=').trim()]));
      source = await addRcloneRemoteFromFields(api, {name: fields.name.trim(), type: fields.remoteType.trim(), options, path: fields.path, preset});
    } else {
      const draft = kind === 'webdav' ? {kind, name: fields.name, address: fields.address, user: fields.user, password: fields.password} as const
        : kind === 'rclone' ? {kind, name: fields.name, config: fields.config, path: fields.path} as const
          : fields.driveMode === 'key' ? {kind: 'drive', name: fields.name, path: fields.path, serviceAccountKey: fields.key} as const
            : {kind: 'drive', name: fields.name, path: fields.path, signIn: fields.signIn, redirectedUrl: fields.redirectedUrl} as const;
      const body = remoteSourceCreate(draft, preset);
      if ('missing' in body) throw Object.assign(new Error(t('next.remoteSources.error.invalid')), {code: 'invalid_request'});
      source = await addRemoteSource(api, body);
    }
    if (!isCurrent()) return;
    // The secrets were needed only to try again after a refusal: they go the moment the source is saved.
    set({password: '', config: '', key: '', token: '', options: '', clientSecret: '', redirectedUrl: '', signIn: '', signInAddress: ''});
    setCreated(source); onCreated(source); setStep('reading');
  });
  const saveReading = () => run(async () => {
    if (!created) return;
    const source = await changeRemoteSource(api, created.id, {preset, limits});
    if (isCurrent()) {setCreated(source); setStep('folders');}
  });
  const signInToGoogle = () => run(async () => {
    const started = await startDriveSignIn(api, fields.clientId, fields.clientSecret);
    if (!isCurrent()) return;
    // Shown as a link the owner presses, so no pop-up blocker stands in the way.
    set({signIn: started.signIn, signInAddress: started.address});
  });
  const steps = [{title: t('next.remoteSources.whatToConnect'), detail: t(kinds.find(one => one.id === kind)!.label)},
    {title: t('next.remoteSources.remote'), detail: created?.name}, {title: t('next.remoteSources.howTreated'), detail: created ? t(presetLabel(preset)) : undefined},
    {title: t('next.remoteSources.foldersSection')}, {title: t('next.remoteSources.done')}];
  const order: Step[] = ['what', 'remote', 'reading', 'folders', 'done'];
  const field = (label: MessageId, key: keyof Fields, extra: Readonly<{type?: string; multiline?: boolean}> = {}) => extra.multiline
    // A pasted rclone configuration is several lines; a one-line box would join them.
    ? <label className={styles.multiline}><span>{t(label)}</span><textarea value={String(fields[key])} rows={6} disabled={busy} autoCapitalize="off" spellCheck={false}
      onChange={event => set({[key]: event.currentTarget.value} as Partial<Fields>)}/></label>
    : <TextField label={t(label)} value={String(fields[key])} type={extra.type} disabled={busy} autoCapitalize="off" spellCheck={false}
      onChange={event => set({[key]: event.currentTarget.value} as Partial<Fields>)}/>;
  const footer = step === 'what' ? <><Button size="compact" variant="glass" onClick={onClose}>{t('next.remoteSources.cancel')}</Button><Button size="compact" variant="primary" onClick={() => setStep('remote')}>{t('next.remoteSources.continue')}</Button></>
    : step === 'remote' ? <><Button size="compact" variant="glass" disabled={busy} onClick={() => setStep('what')}>{t('next.remoteSources.back')}</Button><Button size="compact" variant="primary" loading={busy} disabled={!fields.name.trim()} onClick={testAndSave}>{t('next.remoteSources.test')}</Button></>
      : step === 'reading' ? <Button size="compact" variant="primary" loading={busy} onClick={saveReading}>{t('next.remoteSources.saveAndContinue')}</Button>
        : step === 'folders' ? <Button size="compact" variant="glass" onClick={() => setStep('done')}>{t('next.remoteSources.continue')}</Button>
          : <Button size="compact" variant="primary" onClick={onClose}>{t('next.remoteSources.done')}</Button>;
  return <SourceWizard open={open} returnFocusRef={returnFocusRef} onClose={onClose} title={t('next.settingsSpec.server.remote-sources.panels.newSource.newTitle')}
    currentTitle={steps[order.indexOf(step)]!.title} step={order.indexOf(step) + 1} steps={steps} footer={footer}>
    {error && <p className={styles.error} role="alert">{error}</p>}
    {step === 'what' && <RadioGroup.Root className={styles.whatCards} aria-label={t('next.remoteSources.whatToConnect')} value={kind} onValueChange={value => setKind(value as Kind)}>
      {kinds.map(option => <RadioGroup.Item key={option.id} value={option.id} className={styles.whatCard} data-picked={kind === option.id || undefined}>
        <span className={styles.choiceRadio} aria-hidden="true"/><span className={styles.whatBody}><b>{t(option.label)}</b><span className={styles.choiceHint}>{t(option.hint)}</span></span>
      </RadioGroup.Item>)}
    </RadioGroup.Root>}
    {step === 'remote' && <div className={styles.fields}>
      {field('next.remoteSources.name', 'name')}
      {kind === 'webdav' && <>{field('next.remoteSources.address', 'address')}{field('next.remoteSources.username', 'user')}{field('next.remoteSources.password', 'password', {type: 'password'})}</>}
      {kind === 'rclone' && <><p className={styles.muted}>{t('next.remoteSources.configChecksOnSave')}</p>{field('next.remoteSources.connectRclone', 'config', {multiline: true})}{field('next.remoteSources.cloudFolder', 'path')}</>}
      {kind === 'cloud' && <><RadioGroup.Root className={styles.remoteKinds} aria-label={t('next.remoteSources.cloudProvider')} value={fields.provider} onValueChange={value => set({provider: value as Fields['provider']})}>
        {(['onedrive', 'dropbox'] as const).map(provider => <RadioGroup.Item key={provider} value={provider} className={styles.whatCard} data-picked={fields.provider === provider || undefined}>
          <span className={styles.choiceRadio} aria-hidden="true"/><b>{provider === 'onedrive' ? t('next.remoteSources.cloudOneDrive') : t('next.remoteSources.cloudDropbox')}</b></RadioGroup.Item>)}
      </RadioGroup.Root><p className={styles.muted}>{t('next.remoteSources.cloudTokenHelp')}</p>{field('next.remoteSources.cloudToken', 'token')}{field('next.remoteSources.cloudFolder', 'path')}</>}
      {kind === 'newRclone' && <>{field('next.remoteSources.remoteKind', 'remoteType')}{field('next.remoteSources.connectRcloneHint', 'options', {multiline: true})}{field('next.remoteSources.cloudFolder', 'path')}</>}
      {kind === 'drive' && <><RadioGroup.Root className={styles.remoteKinds} aria-label={t('next.remoteSources.driveSignIn')} value={fields.driveMode} onValueChange={value => set({driveMode: value as Fields['driveMode']})}>
        <RadioGroup.Item value="key" className={styles.whatCard} data-picked={fields.driveMode === 'key' || undefined}><span className={styles.choiceRadio} aria-hidden="true"/><span className={styles.whatBody}><b>{t('next.remoteSources.driveServiceAccount')}</b><span className={styles.choiceHint}>{t('next.remoteSources.driveServiceAccountHint')}</span></span></RadioGroup.Item>
        <RadioGroup.Item value="oauth" className={styles.whatCard} data-picked={fields.driveMode === 'oauth' || undefined}><span className={styles.choiceRadio} aria-hidden="true"/><span className={styles.whatBody}><b>{t('next.remoteSources.driveOAuth')}</b><span className={styles.choiceHint}>{t('next.remoteSources.driveOAuthHint')}</span></span></RadioGroup.Item>
      </RadioGroup.Root>
        {fields.driveMode === 'key' ? <><p className={styles.muted}>{t('next.remoteSources.driveKeyHelp')}</p>{field('next.remoteSources.driveKey', 'key')}</> : <>
          {field('next.remoteSources.driveClientId', 'clientId')}{field('next.remoteSources.driveClientSecret', 'clientSecret', {type: 'password'})}
          <Button size="small" disabled={busy || !fields.clientId.trim() || !fields.clientSecret} onClick={signInToGoogle}>{t('next.remoteSources.driveOAuthSignIn')}</Button>
          {fields.signIn && <><a href={fields.signInAddress} target="_blank" rel="noopener noreferrer">{t('next.remoteSources.driveOAuthSignIn')}</a>
            <p className={styles.muted}>{t('next.remoteSources.driveOAuthPaste')}</p>{field('next.remoteSources.driveOAuthPasteAction', 'redirectedUrl')}</>}</>}
        {field('next.remoteSources.cloudFolder', 'path')}</>}
    </div>}
    {step === 'reading' && created && <ReadingFields name={created.name} preset={preset} limits={limits} disabled={busy}
      onPreset={value => {setPreset(value); setLimits(remotePresets.find(one => one.id === value)!.limits);}} onLimits={setLimits}/>}
    {step === 'folders' && created && <FolderStep api={api} source={created} isCurrent={isCurrent} onDone={() => setStep('done')}/>}
    {step === 'done' && created && <SourceDone title={t('next.remoteSources.doneTitle', {name: created.name})} lede={t('next.remoteSources.doneLede', {name: created.name})} rows={[]}/>}
  </SourceWizard>;
}

/** The preset and its limits (rules 11, 12): choosing a preset starts its limits, which stay editable. */
function ReadingFields({name, preset, limits, disabled, onPreset, onLimits}: Readonly<{name: string; preset: RemotePreset; limits: RemoteLimits; disabled: boolean;
  onPreset: (preset: RemotePreset) => void; onLimits: (limits: RemoteLimits) => void}>) {
  const number = (value: string, fallback: number) => {const parsed = Number.parseInt(value, 10); return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback;};
  return <div className={styles.fields}>
    <RadioGroup.Root className={styles.whatCards} aria-label={t('next.remoteSources.howTreated')} value={preset} disabled={disabled} onValueChange={value => onPreset(value as RemotePreset)}>
      {remotePresets.map(option => <RadioGroup.Item key={option.id} value={option.id} className={styles.whatCard} data-picked={preset === option.id || undefined}>
        <span className={styles.choiceRadio} aria-hidden="true"/><b>{t(presetLabel(option.id))}</b></RadioGroup.Item>)}
    </RadioGroup.Root>
    <TextField label={t('next.remoteSources.connections')} inputMode="numeric" value={String(limits.connections)} disabled={disabled}
      onChange={event => onLimits({...limits, connections: number(event.currentTarget.value, limits.connections)})}/>
    <p className={styles.muted}>{t('next.remoteSources.connectionsHelp', {name})}</p>
    <TextField label={t('next.remoteSources.perSecond')} inputMode="numeric" value={String(limits.perSecond)} disabled={disabled}
      onChange={event => onLimits({...limits, perSecond: number(event.currentTarget.value, limits.perSecond)})}/>
  </div>;
}

/** Continue setup: open the source's folders, choose one, and put it in a library, new or existing. */
function FolderStep({api, source, isCurrent, onDone}: Readonly<{api: ServerRequester; source: RemoteSource; isCurrent: () => boolean; onDone: () => void}>) {
  const [path, setPath] = useState(''), [folders, setFolders] = useState<readonly RemoteFolder[]>(), [libraries, setLibraries] = useState<readonly AdminLibrary[]>([]);
  const [target, setTarget] = useState(''), [newName, setNewName] = useState(source.name), [newKind, setNewKind] = useState('movies');
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  useEffect(() => {
    setFolders(undefined);
    void remoteSourceFolders(api, source.id, path).then(list => {if (isCurrent()) setFolders(list);}, failure => {if (isCurrent()) setError(remoteErrorText(failure));});
    // isCurrent is read when the answer arrives; a new one each render must not read again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, source.id, path]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {void listAdminLibraries(api).then(list => {if (isCurrent()) setLibraries(list);}, () => {});}, [api]);
  const add = () => {
    if (busy) return;
    setBusy(true); setError('');
    const folder = {source: source.id, path};
    void (target ? addRemoteFolderToLibrary(api, target, folder) : createLibraryOnRemoteFolder(api, {name: newName, kind: newKind}, folder))
      .then(() => {if (isCurrent()) onDone();}, failure => {if (isCurrent()) setError(remoteErrorText(failure));}).finally(() => {if (isCurrent()) setBusy(false);});
  };
  const up = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
  return <div className={styles.fields}>
    {error && <p className={styles.error} role="alert">{error}</p>}
    <p className={styles.muted}>{t('next.remoteSources.foldersNote')}</p>
    <p><b>{t('next.remoteSources.folderPath')}</b> /{path}</p>
    {path && <Button size="xs" variant="text" onClick={() => setPath(up)}>{t('next.remoteSources.back')}</Button>}
    {folders === undefined && !error && <p className={styles.muted} role="status">{t('next.remoteSources.folderListing')}</p>}
    {folders?.map(folder => <Button key={folder.path} size="small" variant="glass" icon="i-folder" onClick={() => setPath(folder.path)}>{folder.name}</Button>)}
    <RadioGroup.Root className={styles.remoteKinds} aria-label={t('next.remoteSources.foldersSection')} value={target} onValueChange={setTarget}>
      {libraries.map(library => <RadioGroup.Item key={library.id} value={library.id} className={styles.whatCard} data-picked={target === library.id || undefined}>
        <span className={styles.choiceRadio} aria-hidden="true"/><b>{library.name}</b></RadioGroup.Item>)}
      <RadioGroup.Item value="" className={styles.whatCard} data-picked={target === '' || undefined}><span className={styles.choiceRadio} aria-hidden="true"/><b>{t('next.remoteSources.newLibrary')}</b></RadioGroup.Item>
    </RadioGroup.Root>
    {!target && <><TextField label={t('next.remoteSources.libraryName')} value={newName} onChange={event => setNewName(event.currentTarget.value)}/>
      <RadioGroup.Root className={styles.remoteKinds} aria-label={t('next.remoteSources.libraryKind')} value={newKind} onValueChange={setNewKind}>
        {(['movies', 'shows', 'anime', 'music', 'audiobooks', 'books', 'comics'] as const).map((kind, index) => <RadioGroup.Item key={kind} value={kind} className={styles.whatCard} data-picked={newKind === kind || undefined}>
          <span className={styles.choiceRadio} aria-hidden="true"/><b>{t(`next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.${index}.label` as MessageId)}</b></RadioGroup.Item>)}
      </RadioGroup.Root></>}
    <Button size="compact" variant="primary" loading={busy} disabled={!target && !newName.trim()} onClick={add}>{t('next.remoteSources.saveAndContinue')}</Button>
  </div>;
}

/** A saved source: test it, change its preset, limits, name or credentials (tested again), continue its setup, or disconnect it. */
function ManageSourceDialog({api, source, isCurrent, onClose, onContinue}: Readonly<{api: ServerRequester; source: RemoteSource; isCurrent: () => boolean;
  onClose: () => void; onContinue: (source: RemoteSource) => void}>) {
  const [shown, setShown] = useState(source), [name, setName] = useState(source.name), [secret, setSecret] = useState('');
  const [preset, setPreset] = useState<RemotePreset>((remotePresets.find(one => one.id === source.preset)?.id ?? defaultRemotePreset)), [limits, setLimits] = useState(source.limits);
  const [busy, setBusy] = useState(false), [error, setError] = useState(''), [confirm, setConfirm] = useState<string>();
  const run = (work: () => Promise<void>) => {
    if (busy) return;
    setBusy(true); setError('');
    void work().catch(failure => {if (isCurrent()) setError(remoteErrorText(failure));}).finally(() => {if (isCurrent()) setBusy(false);});
  };
  const save = () => run(async () => {
    const replaced = !secret ? {} : source.kind === 'webdav' ? {webdav: {address: shown.webdav!.address, ...(shown.webdav?.user ? {user: shown.webdav.user} : {}), password: secret}}
      : source.kind === 'rclone' ? {rclone: {config: secret}} : {drive: {serviceAccountKey: secret}};
    const next = await changeRemoteSource(api, source.id, {name: name.trim(), preset, limits, ...replaced});
    if (isCurrent()) {setShown(next); setSecret('');}
  });
  return <Dialog open onOpenChange={open => {if (!open && !busy) onClose();}} title={shown.name}
    footer={<><Button size="compact" variant="glass" onClick={onClose}>{t('next.remoteSources.cancel')}</Button><Button size="compact" variant="primary" loading={busy} disabled={!name.trim()} onClick={save}>{t('next.remoteSources.save')}</Button></>}>
    <div className={styles.fields}>
      <p><SourceStatus source={shown}/> {shown.problem}</p>
      {error && <p className={styles.error} role="alert">{error}</p>}
      <span className={styles.rowActions}>
        <Button size="small" disabled={busy} onClick={() => run(async () => {const tested = await testRemoteSource(api, source.id); if (isCurrent()) setShown(tested);})}>{t('next.remoteSources.test')}</Button>
        {shown.state === 'setup' && <Button size="small" variant="primary" onClick={() => onContinue(shown)}>{t('next.remoteSources.continueSetup')}</Button>}
      </span>
      <TextField label={t('next.remoteSources.name')} value={name} disabled={busy} onChange={event => setName(event.currentTarget.value)}/>
      <ReadingFields name={shown.name} preset={preset} limits={limits} disabled={busy} onPreset={value => {setPreset(value); setLimits(remotePresets.find(one => one.id === value)!.limits);}} onLimits={setLimits}/>
      {source.kind === 'rclone'
        // A replacement configuration is several lines, like the one pasted when the source was added.
        ? <label className={styles.multiline}><span>{t('next.remoteSources.connectRclone')}</span><textarea value={secret} rows={6} disabled={busy} autoCapitalize="off" spellCheck={false}
          onChange={event => setSecret(event.currentTarget.value)}/></label>
        : <TextField label={t(source.kind === 'webdav' ? 'next.remoteSources.password' : 'next.remoteSources.driveKey')}
          type={source.kind === 'webdav' ? 'password' : undefined} value={secret} disabled={busy} autoCapitalize="off" spellCheck={false} onChange={event => setSecret(event.currentTarget.value)}/>}
      {confirm === undefined ? <Button size="small" variant="text" onClick={() => setConfirm('')}>{t('next.remoteSources.disconnect')}</Button> : <>
        <p>{t('next.remoteSources.disconnectBodyNext')}</p>
        <TextField label={t('next.remoteSources.disconnectConfirm')} value={confirm} onChange={event => setConfirm(event.currentTarget.value)}/>
        <Button size="small" disabled={busy || confirm !== shown.name} onClick={() => run(async () => {await removeRemoteSource(api, source.id); if (isCurrent()) onClose();})}>{t('next.remoteSources.disconnect')}</Button></>}
    </div>
  </Dialog>;
}
