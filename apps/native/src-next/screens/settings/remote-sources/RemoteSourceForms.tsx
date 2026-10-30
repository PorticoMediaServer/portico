import React, {createContext, useCallback, useContext, useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {type DAVConnectionResult, type RemoteState, type RemoteSource, type SourceReadPreset, type SourceReadSettings, type SourceReadValues, type SourceAdvancedSettings, type SourceChoiceApplyResult, type SourceChoiceIntent} from '../../../../../../packages/client-core/src/remote-sources';
import {sourceChoiceAdopt, sourceChoiceCommand, sourceChoiceComparison, sourceChoiceResetDefinition, type SourceChoiceCommand, type SourceChoiceDefinition} from '../../../../../../packages/client-core/src/remote-source-choice';
import {Button} from '../../../kit/controls/Button';
import {TextField} from '../../../kit/controls/TextField';
import {Switch} from '../../../kit/controls/Switch';
import {useTheme} from '../../../kit/theme';
import type {NativeRemoteSourcesController} from './controller';
import {SourceAnalysisConsent} from './SourceAnalysisConsent';
import {SourceReadingControls} from './SourceReadingControls';
import {ConnectionTestCard} from './ConnectionTestCard';
import {connectionTestField} from './reading-presentation';
import {LibraryGroup, LibraryRow} from '../../settings-live/owner-libraries/controls';
import {StandardBackdrop} from '../../../kit/artwork';
import {FormScrollView} from '../../../platform/keyboard';

type Translate = typeof defaultI18n.t;
type Common = {controller: NativeRemoteSourcesController; t: Translate; onCancel: () => void; isCurrent: () => boolean};

export function remoteSourceError(t: Translate, error: unknown): string {
  const code = error && typeof error === 'object' && 'code' in error && typeof error.code === 'string' ? error.code : '';
  const keys = {unauthorized: 'next.remoteSources.error.signIn', forbidden: 'next.remoteSources.error.forbidden', not_permitted: 'next.remoteSources.error.forbidden',
    storage_command_conflict: 'next.remoteSources.error.changed', revision_mismatch: 'next.remoteSources.error.changed', remote_source_changed: 'next.remoteSources.error.changed',
    idempotency_key_reused: 'next.remoteSources.error.reused', request_in_progress: 'next.remoteSources.error.busy', refresh_failed: 'next.remoteSources.error.refreshFailed',
    remote_invalid_configuration: 'next.remoteSources.error.invalid', invalid_request: 'next.remoteSources.error.invalid'} as const;
  return t(Object.prototype.hasOwnProperty.call(keys, code) ? keys[code as keyof typeof keys] : 'next.remoteSources.error.generic');
}

export function receiptCheckRequired(state: RemoteState): boolean {
  // A confirmed command's follow-up list read can fail after the service cleared its receipt.
  // Check the original operation; never turn that refresh failure into a new mutation.
  return state.ambiguous || !!state.operationId && !state.receipt && !state.rclone
    && (!state.errorCode || state.errorCode === 'remote_source_not_listed');
}

export function useAttempt(parentCurrent: () => boolean) {
  const live = useRef(true);
  useEffect(() => {live.current = true; return () => {live.current = false;};}, []);
  return useCallback(() => live.current && parentCurrent(), [parentCurrent]);
}

export const SourceWizardContext = createContext<Readonly<{step: number; back: () => void}> | undefined>(undefined);

export function Page({title, t, busy, onCancel, children, footer, wizardStep, scrollable = true}: {title: string; t: Translate; busy: boolean; onCancel: () => void; children: React.ReactNode; footer: React.ReactNode; wizardStep?: number; scrollable?: boolean}) {
  const theme = useTheme(), wizard = useContext(SourceWizardContext), step = wizardStep ?? wizard?.step;
  return <View style={{flex: 1}}><StandardBackdrop/>
    <View style={{padding: 20, flexDirection: 'row', alignItems: 'center', gap: 16}}>
      <Button variant="text" size="small" disabled={busy} onPress={onCancel}>{t('next.remoteSources.cancel')}</Button>
      <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text, flex: 1}]}>{title}</Text>
    </View>
    {step !== undefined && <View style={{paddingHorizontal: 20, gap: 8}}>
      <View style={{flexDirection: 'row', gap: 4}}>{[1, 2, 3, 4, 5].map(value => <View key={value} style={{flex: 1, height: 3, borderRadius: 2, backgroundColor: value <= step ? theme.colors.accentStrong : theme.colors.lineSoft}}/>)}</View><Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.stepOf', {step})}</Text></View>}
    {scrollable ? <FormScrollView keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive" style={{flex: 1}} contentContainerStyle={{padding: 20, gap: 16}}>{children}</FormScrollView> : <View style={{flex: 1, minHeight: 0, padding: 20}}>{children}</View>}
    <View style={{padding: 20, borderTopWidth: 1, borderTopColor: theme.colors.line, gap: 12}}>
      {wizard && step !== undefined && step > 1 && step < 5 && <Button variant="glass" disabled={busy} onPress={wizard.back}>{t('next.remoteSources.back')}</Button>}{footer}
    </View>
  </View>;
}

export function Problem({message}: {message?: string}) {
  const theme = useTheme();
  return message ? <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.danger}]}>{message}</Text> : null;
}

/** The connection is a local draft. It is never copied into a store, receipt or persisted setting. */
export function ConnectionEditor({controller, t, source, onCancel, onSaved, isCurrent}: Common & {source?: RemoteSource; onSaved: (source: RemoteSource) => void}) {
  const theme = useTheme(), wizard = useContext(SourceWizardContext), current = useAttempt(isCurrent), inFlight = useRef(false), revision = useRef(0), testedRevision = useRef(-1);
  const [name, setName] = useState(source?.name ?? ''), [root, setRoot] = useState(''), [username, setUsername] = useState(''), [password, setPassword] = useState('');
  const [insecureLocal, setInsecureLocal] = useState(source?.insecureLocal ?? false), [busy, setBusy] = useState(false);
  const [tested, setTested] = useState<DAVConnectionResult>(), [error, setError] = useState<string>(), [errorField, setErrorField] = useState<'password'>(), [testing, setTesting] = useState(false);
  const changedConnection = !source || !!root || !!username || !!password;
  const input = () => ({name, root, username, password, insecureLocal});
  const edit = (setter: (value: string) => void) => (value: string) => {revision.current++; setter(value); setTested(undefined); setError(undefined); setErrorField(undefined);};
  const test = async () => {
    if (inFlight.current || !current()) return;
    inFlight.current = true;
    const captured = revision.current, guard = () => current() && revision.current === captured;
    setBusy(true); setTesting(true); setError(undefined); setErrorField(undefined); setTested(undefined);
    try {const result = await controller.testConnection(input(), guard); if (guard()) {testedRevision.current = captured; setTested(result);}}
    catch (problem) {if (guard()) {const field = connectionTestField(problem); setErrorField(field); setError(field ? 'The storage rejected the sign-in. Check the user name and password.' : remoteSourceError(t, problem));}}
    finally {inFlight.current = false; if (current()) {setBusy(false); setTesting(false);}}
  };
  const finish = () => {
    const state = controller.getSnapshot().sources;
    if (state.receipt && !state.receipt.removed && current()) {onSaved(state.receipt.source); return;}
    if (current()) setError(remoteSourceError(t, {code: state.errorCode}));
  };
  const save = async () => {
    if (inFlight.current || !current() || !name.trim() || changedConnection && (!tested || testedRevision.current !== revision.current)) return;
    inFlight.current = true;
    const draft = {...input(), keepConnection: !changedConnection, keepPassword: !!source && !changedConnection, expectedGeneration: source?.generation};
    setBusy(true); setError(undefined); setPassword(''); setTested(undefined);
    try {await controller.configure(source?.id ?? null, draft, source, current); if (current()) finish();}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  const recover = async () => {
    if (inFlight.current || !current()) return;
    inFlight.current = true;
    setBusy(true); setError(undefined);
    try {await controller.recover(); if (current()) finish();}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  const ambiguous = receiptCheckRequired(controller.getSnapshot().sources);
  return <Page title={source?.name ?? t('next.remoteSources.add')} t={t} busy={busy} onCancel={onCancel}
    footer={ambiguous ? <Button onPress={() => {void recover();}} disabled={busy}>{t('next.remoteSources.checkResult')}</Button>
      : <Button onPress={() => {void save();}} loading={busy} disabled={busy || !name.trim() || changedConnection && !tested}>{t(wizard ? 'next.remoteSources.continue' : 'next.remoteSources.saveAndContinue')}</Button>}>
    {tested ? <LibraryGroup><LibraryRow first label={name} help={tested.origin} trailing={<Button variant="text" size="small" disabled={busy || ambiguous} onPress={() => {revision.current++; setTested(undefined); setError(undefined); setErrorField(undefined);}}>{t('web.selection.edit')}</Button>}/></LibraryGroup> : <>
    <TextField label={t('next.remoteSources.name')} value={name} onChangeText={edit(setName)} editable={!busy && !ambiguous} autoCorrect={false}/>
    {!!source?.origin && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{source.origin}</Text>}
    <TextField label={t('next.remoteSources.address')} value={root} onChangeText={edit(setRoot)} editable={!busy && !ambiguous} autoCapitalize="none" autoCorrect={false} keyboardType="url"/>
    <TextField label={t('next.remoteSources.username')} value={username} onChangeText={edit(setUsername)} editable={!busy && !ambiguous} autoCapitalize="none" autoCorrect={false} autoComplete="off"/>
    <TextField label={t('next.remoteSources.password')} error={errorField === 'password' ? error : undefined} value={password} onChangeText={edit(setPassword)} editable={!busy && !ambiguous} secureTextEntry autoCapitalize="none" autoCorrect={false} autoComplete="off"/>
    <Switch checked={insecureLocal} onCheckedChange={value => {revision.current++; setInsecureLocal(value); setTested(undefined); setError(undefined); setErrorField(undefined);}} disabled={busy || ambiguous} variant="phone">{t('next.remoteSources.insecureLocal')}</Switch>
    </>}
    {!tested && !error && <Button variant="glass" onPress={() => {void test();}} disabled={busy || ambiguous || !name.trim() || !root.trim()}>{testing ? t('next.remoteSources.testing') : t('next.remoteSources.test')}</Button>}
    <ConnectionTestCard testing={testing} result={tested} error={error} t={t} disabled={busy || ambiguous || !name.trim() || !root.trim()} onTestAgain={() => {void test();}}/>

  </Page>;
}

const presets: readonly SourceReadPreset[] = ['list', 'once', 'everything', 'ask'];
const presetLabels = [
  'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.0.label',
  'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.1.label',
  'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.2.label',
  'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.3.label',
] as const;

/** The drawn name of one known reading preset, from the same options the editor uses. */
export function sourceReadPresetLabel(t: Translate, preset: SourceReadPreset): string {
  return t(presetLabels[presets.indexOf(preset)]);
}

export function ReadingEditor({controller, t, source, onCancel, onSaved, onAccepted, isCurrent}: Common & {source: RemoteSource; onSaved: (source: RemoteSource) => void; onAccepted?: (source: RemoteSource) => void}) {
  const current = useAttempt(isCurrent), inFlight = useRef(false), wizard = useContext(SourceWizardContext);
  const [profile, setProfile] = useState<SourceReadSettings>(), [advanced, setAdvanced] = useState<SourceAdvancedSettings>(), [values, setValues] = useState<SourceReadValues>();
  const profileOwner = useRef<SourceReadSettings | undefined>(undefined), revision = useRef(0), renderedRevision = revision.current;
  const owns = () => current() && profileOwner.current === profile && revision.current === renderedRevision;
  const [consent, setConsent] = useState<boolean>(), [consentDirty, setConsentDirty] = useState(false);
  const [busy, setBusy] = useState(false), [error, setError] = useState<string>();
  const [unknown, setUnknown] = useState<SourceChoiceIntent>();
  const [accepted, setAccepted] = useState<{result: Exclude<SourceChoiceApplyResult, {kind: 'unknown'}>; command: SourceChoiceCommand}>();
  const choiceOpening = useRef<AbortController | undefined>(undefined);
  useEffect(() => {const opening = new AbortController(); choiceOpening.current = opening; return () => opening.abort();}, []);
  const accept = (next: SourceReadSettings, nextAdvanced: SourceAdvancedSettings) => {
    profileOwner.current = next; revision.current++; setProfile(next); setAdvanced(nextAdvanced); setValues(next.values);
    setConsent(next.values.allowAdvancedMediaAnalysis); setConsentDirty(false);
  };
  const load = async () => {
    if (inFlight.current || !current()) return;
    inFlight.current = true; setBusy(true); setError(undefined);
    try {
      const [next, nextAdvanced] = await Promise.all([controller.readProfile(source, current), controller.readAdvanced(source, current)]);
      if (current()) accept(next, nextAdvanced);
    } catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  useEffect(() => {void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const matchingSource = async (settings: SourceReadSettings) => {
    await controller.load();
    if (!current()) return;
    const state = controller.getSnapshot().sources;
    return [state.receipt && !state.receipt.removed ? state.receipt.source : undefined, ...state.sources]
      .find(row => row?.id === settings.sourceId && String(row.generation) === settings.generation);
  };
  const adoptChoice = async (result: SourceChoiceApplyResult, command: SourceChoiceCommand) => {
    if (!owns()) return;
    if (result.kind === 'unknown') {setUnknown(result.intent); setError(remoteSourceError(t, result.error)); return;}
    setUnknown(undefined); setAccepted({result, command});
    if (result.kind !== 'applied') {setError(t('next.remoteSources.error.refreshFailed')); return;}
    const nextSource = await matchingSource(result.settings);
    if (!owns()) return;
    if (!nextSource) {setError(t('next.remoteSources.error.refreshFailed')); return;}
    onAccepted?.(nextSource);
  };
  // Choices write the exact offered Reading/Advanced pair. Dirty independent
  // consent is resolved only by its explicit Save action below.
  const select = async (offered: SourceChoiceDefinition) => {
    if (inFlight.current || !owns() || !profile || !advanced || consentDirty || unknown || accepted || !choiceOpening.current) return;
    const command = sourceChoiceCommand(profile, advanced, offered);
    if (!command) return;
    inFlight.current = true; setBusy(true); setError(undefined);
    try {await adoptChoice(await controller.saveProfileStartChoice(profile, advanced, offered, {signal: choiceOpening.current.signal, isCurrent: owns, consentDirty: false}), command);}
    catch (problem) {if (owns()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  const recoverChoice = async () => {
    if (inFlight.current || !owns()) return;
    inFlight.current = true; setBusy(true); setError(undefined);
    try {
      if (unknown) await adoptChoice(await controller.recoverStartChoice(unknown, owns), unknown.command);
      else if (accepted) {
        const next = await controller.reconcileChoiceProfile(source.id, owns);
        if (!sourceChoiceAdopt(next, accepted.command, accepted.result.accepted)) throw {code: 'refresh_failed'};
        const nextSource = await matchingSource(next);
        if (owns() && nextSource) onAccepted?.(nextSource); else if (owns()) throw {code: 'refresh_failed'};
      }
    } catch (problem) {if (owns()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  const save = async (stay = false) => {
    if (inFlight.current || !profile || !values || !owns() || unknown || accepted || consentDirty && !stay) return;
    // The read's own consent fact is dropped again here: an ordinary save omits
    // the field, so it can neither grant nor revoke the recorded permission.
    const {scanDepth, wholeFileReads, connections, requestsPerSecond, bytesPerSecond} = values;
    const command = {scanDepth, wholeFileReads, connections, requestsPerSecond, bytesPerSecond,
      ...(consentDirty && typeof consent === 'boolean' ? {allowAdvancedMediaAnalysis: consent} : {})};
    inFlight.current = true; setBusy(true); setError(undefined);
    try {
      const next = await controller.saveProfile(profile, command, owns);
      if (!owns()) return;
      const nextSource = await matchingSource(next);
      if (!owns()) return;
      if (!nextSource) throw {code: 'refresh_failed'};
      if (stay) onAccepted?.(nextSource); else onSaved(nextSource);
    } catch (problem) {if (owns()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  const ambiguous = receiptCheckRequired(controller.getSnapshot().sources);
  const recover = async () => {
    if (inFlight.current || !current()) return;
    inFlight.current = true; setBusy(true); setError(undefined);
    try {
      await controller.recover();
      const state = controller.getSnapshot().sources;
      if (state.ambiguous || !state.receipt || state.receipt.removed || state.receipt.source.id !== source.id) throw {code: state.errorCode};
      await controller.readProfile(state.receipt.source, current);
      if (current()) onAccepted?.(state.receipt.source);
    } catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  const comparison = profile && sourceChoiceComparison(profile), reset = profile && sourceChoiceResetDefinition(profile);
  const blocked = busy || ambiguous || !!unknown || !!accepted;
  return <Page title={t('next.remoteSources.reading')} t={t} busy={busy} onCancel={onCancel}
    footer={unknown || accepted ? <Button disabled={busy} onPress={() => {void recoverChoice();}}>{t('next.remoteSources.checkResult')}</Button>
      : ambiguous ? <Button disabled={busy} onPress={() => {void recover();}}>{t('next.remoteSources.checkResult')}</Button>
      : <Button disabled={busy || !profile || !values || consentDirty} onPress={() => {void save();}}>{t(wizard ? 'next.remoteSources.continue' : 'next.remoteSources.save')}</Button>}>
    {profile && values && <>
      <SourceReadingControls values={values} profile={profile} comparison={comparison} baseline={comparison ? profile.choiceBaseline?.reading : undefined} disabled={blocked} choicesDisabled={blocked || consentDirty || !advanced}
        onSelect={offered => {void select(offered);}} t={t}
        onLimit={(field, value) => {if (blocked || !owns()) return; revision.current++; setValues({...values, [field]: value});}}
        onReset={reset ? () => {void select(reset);} : undefined}/>
      <SourceAnalysisConsent value={consent} disabled={blocked} t={t} onChange={value => {if (!blocked && owns()) {revision.current++; setConsent(value); setConsentDirty(true);}}}/>
      {consentDirty && <Button variant="glass" disabled={blocked} onPress={() => {void save(true);}}>{t('next.remoteSources.saveAnalysisConsent')}</Button>}
    </>}
    {!profile && !busy && <Button variant="glass" onPress={() => {void load();}}>{t('next.remoteSources.retry')}</Button>}
    <Problem message={error}/>
  </Page>;
}

export function DisconnectEditor({controller, t, source, onCancel, onSaved, isCurrent}: Common & {source: RemoteSource; onSaved: () => void}) {
  const theme = useTheme(), current = useAttempt(isCurrent), inFlight = useRef(false);
  const [name, setName] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState<string>();
  const remove = async () => {
    if (inFlight.current || name !== source.name || !current()) return;
    inFlight.current = true;
    setBusy(true); setError(undefined);
    try {
      await controller.remove(source, current);
      const state = controller.getSnapshot().sources;
      if (current() && (state.alreadyDone || state.receipt?.removed || state.rclone?.sourceId === source.id && state.rclone.removed)) onSaved();
      else if (current()) setError(remoteSourceError(t, {code: state.errorCode}));
    } catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  const ambiguous = receiptCheckRequired(controller.getSnapshot().sources);
  const recover = async () => {
    if (inFlight.current || !current()) return;
    inFlight.current = true; setBusy(true); setError(undefined);
    try {
      await controller.recover();
      const state = controller.getSnapshot().sources;
      if (!state.ambiguous && (state.receipt?.removed || state.rclone?.sourceId === source.id && state.rclone.removed) && current()) onSaved();
      else if (current()) setError(remoteSourceError(t, {code: state.errorCode}));
    } catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {inFlight.current = false; if (current()) setBusy(false);}
  };
  return <Page title={t('next.remoteSources.disconnectTitle', {name: source.name})} t={t} busy={busy} onCancel={onCancel}
    footer={ambiguous ? <Button onPress={() => {void recover();}} disabled={busy}>{t('next.remoteSources.checkResult')}</Button>
      : <Button variant="glass" style={{backgroundColor: `${theme.colors.record}24`}} textStyle={{color: theme.colors.record}} onPress={() => {void remove();}} disabled={busy || name !== source.name}>{t('next.remoteSources.disconnect')}</Button>}>
    <Text style={[theme.type.body, {color: theme.colors.text}]}>{t('next.remoteSources.disconnectBody')}</Text>
    <TextField label={t('next.remoteSources.disconnectConfirm')} value={name} onChangeText={setName} editable={!busy} autoCorrect={false} autoCapitalize="none"/>
    <Problem message={error}/>
  </Page>;
}
