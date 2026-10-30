import React, {useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {Linking, Text} from 'react-native';
import {s3SourceDraft, driveServiceAccountDraft, driveOAuthDraft, cloudTokenDraft, type CloudTokenFields, type DriveKeyFields, type S3SourceFields, type RemoteSource, type RcloneProgram, type RcloneTestResult} from '../../../../../../packages/client-core/src/remote-sources';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Select} from '../../../kit/controls/Select';
import {TextField} from '../../../kit/controls/TextField';
import {useTheme} from '../../../kit/theme';
import {copyText} from '../../../platform/clipboard';
import type {NativeRemoteSourcesController} from './controller';
import {Page, Problem, SourceWizardContext, receiptCheckRequired, remoteSourceError, useAttempt} from './RemoteSourceForms';

type FormTicket = {live: boolean; scope: object};

/** Connection fields remain local write-only drafts. Never replay credentials. */
export function ImportedSourceEditor({controller, source, t, isCurrent, onCancel, onSaved, form = 'imported'}: {
  controller: NativeRemoteSourcesController; source?: RemoteSource; form?: 'imported' | 's3' | 'drive' | 'cloud' | 'driveOAuth'; t: typeof defaultI18n.t;
  isCurrent: () => boolean; onCancel: () => void; onSaved: (source: RemoteSource) => void;
}) {
  const theme = useTheme(), wizard = useContext(SourceWizardContext), current = useAttempt(isCurrent), flight = useRef(false), revision = useRef(0), testedDraft = useRef<Readonly<{name: string; remote: string; config: string}> | undefined>(undefined);
  const [name, setName] = useState(source?.name ?? ''), [remote, setRemote] = useState(''), [config, setConfig] = useState('');
  const [s3, setS3] = useState<S3SourceFields>({endpoint: '', region: '', accessKey: '', secretKey: '', bucket: '', folder: ''});
  const [drive, setDrive] = useState<DriveKeyFields>({keyJson: '', location: 'folder', locationId: ''});
  const [clientId, setClientId] = useState(''), [clientSecret, setClientSecret] = useState(''), [redirect, setRedirect] = useState('');
  const [, setOAuthVersion] = useState(0);
  const oauth = useRef<{ticket: FormTicket; clientId: string; clientSecret: string; state?: string; token: string; phase: string} | undefined>(undefined);
  const retireOAuth = useCallback(() => {const old = oauth.current; oauth.current = undefined; if (old?.state) void controller.cancelDriveOAuth(old.state);}, [controller]);
  const scope = useMemo(() => ({controller, sourceId: source?.id, generation: source?.generation, form, isCurrent}), [controller, source?.id, source?.generation, form, isCurrent]);
  const activeForm = useRef<FormTicket | undefined>(undefined);
  const [committedForm, setCommittedForm] = useState<FormTicket>();
  // Render replacement closes the old ticket immediately; only layout setup can
  // issue a fresh ticket. A retained callback never adopts that successor.
  if (activeForm.current && (activeForm.current.scope !== scope || !isCurrent())) {
    activeForm.current.live = false; activeForm.current = undefined;
  }
  useLayoutEffect(() => {
    if (!isCurrent()) return;
    const ticket: FormTicket = {live: true, scope};
    activeForm.current = ticket; setCommittedForm(ticket);
    if (form === 'driveOAuth') {
      flight.current = false; testedDraft.current = undefined;
      setBusy(false); setTested(undefined); setOAuthVersion(value => value + 1);
    }
    return () => {
      ticket.live = false;
      if (activeForm.current === ticket) activeForm.current = undefined;
      if (oauth.current?.ticket === ticket) retireOAuth();
    };
  }, [scope, form, isCurrent, retireOAuth]);
  const renderedForm = committedForm?.scope === scope ? committedForm : undefined;
  const admitted = () => current() && (form !== 'driveOAuth' || !!renderedForm && renderedForm.live && activeForm.current === renderedForm);
  const latestRedirect = useRef(redirect); latestRedirect.current = redirect;
  const oauthInput = oauth.current, oauthToken = oauthInput?.token;
  const oauthResult = useMemo(() => form === 'driveOAuth' && oauthInput && oauthToken ? driveOAuthDraft({...drive,
    clientId: oauthInput.clientId, clientSecret: oauthInput.clientSecret, token: oauthToken}) : undefined, [form, drive, oauthInput, oauthToken]);
  const [cloud, setCloud] = useState<CloudTokenFields>({provider: 'onedrive', token: '', folder: ''});
  const cloudResult = useMemo(() => form === 'cloud' ? cloudTokenDraft(cloud) : undefined, [form, cloud]);
  const driveResult = useMemo(() => form === 'drive' ? driveServiceAccountDraft(drive) : undefined, [form, drive]);
  const built = useMemo(() => form === 's3' ? s3SourceDraft(s3) : cloudResult?.ok ? cloudResult.draft : oauthResult?.ok ? oauthResult.draft : driveResult?.ok ? driveResult.draft : undefined, [form, s3, driveResult, oauthResult, cloudResult]);
  const [busy, setBusy] = useState(false), [error, setError] = useState<string>(), [tested, setTested] = useState<RcloneTestResult>();
  const [copiedEmail, setCopiedEmail] = useState(false), [copyError, setCopyError] = useState(false);
  const [program, setProgram] = useState<RcloneProgram>(), [providers, setProviders] = useState<readonly string[]>([]);
  const draft = useMemo(() => Object.freeze({name, remote: form !== 'imported' ? built?.remote ?? '' : remote, config: form !== 'imported' ? built?.config ?? '' : config}), [name, remote, config, built, form]);
  const latestDraft = useRef(draft); latestDraft.current = draft;
  const saveTicket = useRef<object | undefined>(undefined);
  const renderedRevision = revision.current;
  const renderedLease = oauth.current;
  const ownsDraft = () => admitted() && revision.current === renderedRevision && latestDraft.current === draft && (form !== 'driveOAuth' || oauth.current === renderedLease);
  const invalidate = () => {setCopiedEmail(false); setCopyError(false); retireOAuth(); setOAuthVersion(value => value + 1); revision.current++; testedDraft.current = undefined; setTested(undefined); setError(undefined);};
  const facts = async () => {
    if (flight.current || !admitted()) return;
    flight.current = true; setBusy(true); setError(undefined);
    try {const result = await controller.importedFacts(admitted); if (admitted()) {setProgram(result.program); setProviders(result.providers);}}
    catch (problem) {if (admitted()) setError(remoteSourceError(t, problem));}
    finally {if (form !== 'driveOAuth' || renderedForm?.live && activeForm.current === renderedForm) flight.current = false; if (admitted()) setBusy(false);}
  };
  useEffect(() => {void facts();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [renderedForm]);
  const signIn = async () => {
    if (flight.current || !ownsDraft() || recovery || !program?.available || clientId.trim().length < 8 || clientSecret.length < 8) return;
    retireOAuth();
    if (!renderedForm) return;
    const lease = {ticket: renderedForm, clientId, clientSecret, token: '', phase: 'pending', state: undefined as string | undefined}; oauth.current = lease;
    const guard = () => admitted() && revision.current === renderedRevision && oauth.current === lease;
    flight.current = true; setBusy(true); setError(undefined); setClientSecret(''); setRedirect('');
    try {
      const started = await controller.startDriveOAuth(lease.clientId, lease.clientSecret, guard);
      if (!guard()) {void controller.cancelDriveOAuth(started.state); return;}
      lease.state = started.state; setOAuthVersion(value => value + 1);
      if (!guard()) return;
      await Linking.openURL(started.authorizationUrl);
    } catch (problem) {if (guard()) {retireOAuth(); setError(remoteSourceError(t, problem)); setOAuthVersion(value => value + 1);}}
    finally {if (form !== 'driveOAuth' || renderedForm?.live && activeForm.current === renderedForm) flight.current = false; if (admitted()) setBusy(false);}
  };
  const checkSignIn = async (paste: boolean) => {
    if (flight.current || !ownsDraft() || recovery || paste && latestRedirect.current !== redirect || !renderedLease?.state || oauth.current !== renderedLease) return;
    const guard = () => admitted() && revision.current === renderedRevision && oauth.current === renderedLease;
    flight.current = true; setBusy(true); setError(undefined);
    const address = paste ? redirect : undefined; latestRedirect.current = ''; setRedirect('');
    try {
      const answer = await controller.driveOAuthStatus(renderedLease.state, address, guard);
      if (guard()) {renderedLease.token = answer.token; renderedLease.phase = answer.phase; setOAuthVersion(value => value + 1);}
    } catch (problem) {if (guard()) setError(remoteSourceError(t, problem));}
    finally {if (form !== 'driveOAuth' || renderedForm?.live && activeForm.current === renderedForm) flight.current = false; if (admitted()) setBusy(false);}
  };
  const snapshot = controller.getSnapshot().sources, recovery = receiptCheckRequired(snapshot);
  const valid = !!name.trim() && name.length <= 100 && !!draft.remote.trim() && draft.remote.length <= 512 && !!draft.config.trim() && draft.config.length <= 65536;
  const test = async () => {
    if (flight.current || !ownsDraft() || !valid || !program?.available || recovery) return;
    flight.current = true; const guard = ownsDraft;
    setBusy(true); setError(undefined); setTested(undefined); testedDraft.current = undefined;
    try {const result = await controller.testImported({remote: draft.remote, config: draft.config}, guard); if (guard()) {setTested(result); testedDraft.current = draft;}}
    catch (problem) {if (guard()) setError(remoteSourceError(t, problem));}
    finally {if (form !== 'driveOAuth' || renderedForm?.live && activeForm.current === renderedForm) flight.current = false; if (admitted()) setBusy(false);}
  };
  const finish = () => {
    const state = controller.getSnapshot().sources, receipt = state.rclone;
    const row = receipt && !receipt.removed ? state.sources.find(value => value.id === receipt.sourceId && value.generation === receipt.generation) : undefined;
    if (admitted() && row) onSaved(row);
    else if (admitted()) setError(remoteSourceError(t, {code: state.errorCode}));
  };
  const save = async () => {
    if (flight.current || !ownsDraft() || !valid || !tested || testedDraft.current !== draft || recovery) return;
    flight.current = true;
    const ticket = Object.freeze({draft, revision: renderedRevision}); saveTicket.current = ticket;
    const guard = () => admitted() && saveTicket.current === ticket && revision.current === ticket.revision;
    retireOAuth(); setClientSecret(''); setRedirect(''); setCloud(value => ({...value, token: ''})); setConfig(''); setS3(value => ({...value, accessKey: '', secretKey: ''})); setDrive(value => ({...value, keyJson: ''})); setTested(undefined); testedDraft.current = undefined; setBusy(true); setError(undefined);
    try {await controller.configureImported(source, ticket.draft, guard); if (guard()) finish();}
    catch (problem) {if (guard()) setError(remoteSourceError(t, problem));}
    finally {if (saveTicket.current === ticket) saveTicket.current = undefined; if (form !== 'driveOAuth' || renderedForm?.live && activeForm.current === renderedForm) flight.current = false; if (admitted()) setBusy(false);}
  };
  const recover = async () => {
    if (flight.current || !admitted()) return;
    flight.current = true; setBusy(true); setError(undefined);
    try {await controller.recover(); if (admitted()) finish();}
    catch (problem) {if (admitted()) setError(remoteSourceError(t, problem));}
    finally {if (form !== 'driveOAuth' || renderedForm?.live && activeForm.current === renderedForm) flight.current = false; if (admitted()) setBusy(false);}
  };
  const copyEmail = async () => {
    if (!admitted() || !driveResult?.ok) return;
    const captured = revision.current, email = driveResult.draft.email;
    try {await copyText(email); if (admitted() && revision.current === captured) {setCopiedEmail(true); setCopyError(false);}}
    catch {if (admitted() && revision.current === captured) setCopyError(true);}
  };
  const editable = !busy && !recovery && !snapshot.busy;
  return <Page title={source?.name ?? t(form === 's3' ? 'next.remoteSources.connectS3' : form === 'drive' || form === 'driveOAuth' ? 'next.remoteSources.connectDrive' : form === 'cloud' ? 'next.remoteSources.connectCloud' : 'next.remoteSources.kindRclone')} t={t} busy={busy} onCancel={onCancel}
    footer={recovery ? <Button disabled={busy} onPress={() => {void recover();}}>{t('next.remoteSources.checkResult')}</Button>
      : <Button disabled={!editable || !valid || !tested} onPress={() => {void save();}}>{t(wizard ? 'next.remoteSources.continue' : 'next.remoteSources.saveAndContinue')}</Button>}>
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{program?.available && program.version
      ? t('next.remoteSources.bundledVersion', {version: program.version}) : t('next.remoteSources.bundledProgram')}</Text>
    {program && !program.available && <Problem message={t('next.remoteSources.bundledUnavailable')}/>}
    {!!providers.length && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.providers', {providers: defaultI18n.list(providers)})}</Text>}
    {(!program || !program.available) && <Button disabled={busy} variant="text" onPress={() => {void facts();}}>{t('next.remoteSources.refresh')}</Button>}
    <TextField label={t('next.remoteSources.name')} value={name} editable={editable} autoCorrect={false}
      onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setName(value);}}/>
    {form === 's3' ? <>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.s3Checks')}</Text>
      {(['endpoint', 'bucket', 'region', 'accessKey', 'secretKey', 'folder'] as const).map(key => <TextField key={key}
        label={`${t(({endpoint: 'next.remoteSources.s3Endpoint', region: 'next.remoteSources.s3Region', accessKey: 'next.remoteSources.s3AccessKey',
          secretKey: 'next.remoteSources.s3SecretKey', bucket: 'next.remoteSources.s3Bucket', folder: 'next.remoteSources.s3Folder'} as const)[key])}${key === 'region' || key === 'folder' ? ` · ${t('field.optional')}` : ''}`}
        description={key === 'endpoint' ? t('next.remoteSources.s3EndpointHelp') : key === 'region' ? t('next.remoteSources.s3RegionHelp')
          : key === 'bucket' ? t('next.remoteSources.s3BucketHelp') : key === 'folder' ? t('next.remoteSources.s3FolderHelp') : undefined}
        value={s3[key]} editable={editable} autoCapitalize="none" autoCorrect={false} secureTextEntry={key === 'secretKey'} textContentType="none"
        onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setS3(previous => ({...previous, [key]: value,
          ...((key === 'endpoint' || key === 'accessKey') ? {secretKey: ''} : {})}));}}/>)}
      {!built && Object.values(s3).some(value => value !== '') && <Problem message={t('next.remoteSources.s3Invalid')}/>}
    </> : form === 'cloud' ? <>
      <Select label={t('next.remoteSources.cloudProvider')} value={cloud.provider} disabled={!editable} options={[
        {value: 'onedrive', label: t('next.remoteSources.cloudOneDrive')}, {value: 'dropbox', label: t('next.remoteSources.cloudDropbox')},
      ]} onValueChange={value => {if (flight.current || !admitted() || value !== 'onedrive' && value !== 'dropbox') return; invalidate(); setCloud(previous => ({...previous, provider: value, token: ''}));}}/>
      <TextField label={t('next.remoteSources.cloudToken')} description={t('next.remoteSources.cloudTokenHelp')} value={cloud.token}
        editable={editable} multiline autoCapitalize="none" autoCorrect={false} textContentType="none"
        onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setCloud(previous => ({...previous, token: value}));}}/>
      {cloud.token !== '' && cloudResult && !cloudResult.ok && <Problem message={t(({json: 'next.remoteSources.cloudTokenNotJson', token: 'next.remoteSources.cloudTokenMissing',
        client: 'next.remoteSources.cloudTokenClient', google: 'next.remoteSources.cloudTokenGoogle', folder: 'next.remoteSources.cloudFolderInvalid'} as const)[cloudResult.reason])}/>}
      <TextField label={t('next.remoteSources.cloudFolder')} description={t('next.remoteSources.cloudFolderHelp')} value={cloud.folder}
        editable={editable} autoCapitalize="none" autoCorrect={false}
        onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setCloud(previous => ({...previous, folder: value}));}}/>
    </> : form === 'driveOAuth' ? <>
      <TextField label={t('next.remoteSources.driveClientId')} value={clientId} editable={editable} autoCapitalize="none" autoCorrect={false}
        onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setClientId(value); setClientSecret('');}}/>
      <TextField label={t('next.remoteSources.driveClientSecret')} value={clientSecret} editable={editable} secureTextEntry autoCapitalize="none" autoCorrect={false} textContentType="none"
        onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setClientSecret(value);}}/>
      <Select label={t('next.remoteSources.driveLocation')} value={drive.location} disabled={!editable} options={[
        {value: 'folder', label: t('next.remoteSources.driveFolder')}, {value: 'drive', label: t('next.remoteSources.driveDrive')},
      ]} onValueChange={value => {if (flight.current || !admitted() || value !== 'folder' && value !== 'drive') return; invalidate(); setDrive(previous => ({...previous, location: value, locationId: ''}));}}/>
      <TextField label={t(drive.location === 'drive' ? 'next.remoteSources.driveDriveId' : 'next.remoteSources.driveFolderId')} value={drive.locationId} editable={editable} autoCapitalize="none" autoCorrect={false}
        onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setDrive(previous => ({...previous, locationId: value}));}}/>
      <Button disabled={!editable || !program?.available || clientId.trim().length < 8 || clientSecret.length < 8} onPress={() => {void signIn();}}>{t('next.remoteSources.driveOAuthSignIn')}</Button>
      {renderedLease?.state && <>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t(renderedLease.phase === 'ready' ? 'next.remoteSources.driveOAuthReady' : renderedLease.phase === 'pending' ? 'next.remoteSources.driveOAuthWaiting' : 'next.remoteSources.driveOAuthExpired')}</Text>
        {renderedLease.phase === 'pending' && <>
          <Button disabled={!editable} onPress={() => {void checkSignIn(false);}}>{t('next.remoteSources.refresh')}</Button>
          <TextField label={t('next.remoteSources.driveOAuthPaste')} value={redirect} editable={editable} autoCapitalize="none" autoCorrect={false}
            onChangeText={value => {if (flight.current || !ownsDraft() || oauth.current !== renderedLease) return; latestRedirect.current = value; setRedirect(value);}}/>
          <Button disabled={!editable || !redirect.trim()} onPress={() => {void checkSignIn(true);}}>{t('next.remoteSources.driveOAuthPasteAction')}</Button>
        </>}
      </>}
      {oauthResult && !oauthResult.ok && <Problem message={t('next.remoteSources.driveOAuthInvalid')}/>}
    </> : form === 'drive' ? <>
      {!drive.keyJson && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.driveKeyEmptyHint')}</Text>}
      <TextField label={t('next.remoteSources.driveKey')} value={drive.keyJson} multiline
        editable={editable} autoCapitalize="none" autoCorrect={false} textContentType="none"
        onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setDrive(previous => ({...previous, keyJson: value}));}}/>
      {driveResult?.ok && <>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.remoteSources.driveEmailHeading')}</Text>
        <Text selectable style={[theme.type.body, {color: theme.colors.text}]}>{driveResult.draft.email}</Text>
        <Button variant="glass" size="small" icon="i-copy" onPress={() => {void copyEmail();}}>{t('web.twoStep.copy')}</Button>
        {copiedEmail && <Text style={[theme.type.caption, {color: theme.colors.healthy}]}>{t('api.credential.copied')}</Text>}
        {copyError && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{t('next.remoteSources.driveEmailCopyFailed')}</Text>}
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.driveEmailHelp')}</Text>
      </>}
      {drive.keyJson !== '' && driveResult && !driveResult.ok && <Problem message={t(({json: 'next.remoteSources.driveKeyNotJson', oauth: 'next.remoteSources.driveKeyOAuth',
        type: 'next.remoteSources.driveKeyWrongType', email: 'next.remoteSources.driveKeyEmail', key: 'next.remoteSources.driveKeyUnreadable', location: 'next.remoteSources.driveLocationInvalid'} as const)[driveResult.reason])}/>}
      <Select label={t('next.remoteSources.driveLocation')} value={drive.location} disabled={!editable} options={[
        {value: 'folder', label: t('next.remoteSources.driveFolder')}, {value: 'drive', label: t('next.remoteSources.driveDrive')},
      ]} onValueChange={value => {if (flight.current || !admitted() || value !== 'folder' && value !== 'drive') return; invalidate(); setDrive(previous => ({...previous, location: value, locationId: ''}));}}/>
      <TextField label={t(drive.location === 'drive' ? 'next.remoteSources.driveDriveId' : 'next.remoteSources.driveFolderId')} value={drive.locationId}
        editable={editable} autoCapitalize="none" autoCorrect={false} onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setDrive(previous => ({...previous, locationId: value}));}}/>
    </> : <>
    <TextField label={t('next.remoteSources.remoteName')} description={t('next.remoteSources.remoteNameHelp')} value={remote} editable={editable} autoCapitalize="none" autoCorrect={false}
      onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setConfig(''); setRemote(value);}}/>
    <TextField label={t('next.remoteSources.configuration')} description={t(source ? 'next.remoteSources.replaceConfiguration' : 'next.remoteSources.configurationHelp')}
      value={config} editable={editable} multiline autoCapitalize="none" autoCorrect={false} textContentType="none"
      onChangeText={value => {if (flight.current || !admitted()) return; invalidate(); setConfig(value);}}/>
    </>}
    <Button variant="glass" disabled={!editable || !valid || !program?.available} onPress={() => {void test();}}>{t('next.remoteSources.test')}</Button>
    {tested && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{tested.version ? t('next.remoteSources.testedRclone', {version: tested.version}) : t('next.remoteSources.testedRcloneUnknown')}</Text>}
    <Problem message={error}/>
  </Page>;
}
