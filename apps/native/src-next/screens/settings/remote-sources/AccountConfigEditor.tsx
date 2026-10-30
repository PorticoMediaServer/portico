import React, {useEffect, useMemo, useRef, useState} from 'react';
import {Text} from 'react-native';
import type {RcloneConfigView, RemoteSource} from '../../../../../../packages/client-core/src/remote-sources';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Select} from '../../../kit/controls/Select';
import {TextField} from '../../../kit/controls/TextField';
import {useTheme} from '../../../kit/theme';
import type {NativeRemoteSourcesController} from './controller';
import {Page, Problem, receiptCheckRequired, remoteSourceError, useAttempt} from './RemoteSourceForms';

/** Only server-published remote names and kinds enter this form, never sections. */
export function AccountConfigEditor({controller, t, isCurrent, onCancel, onSaved}: {
  controller: NativeRemoteSourcesController; t: typeof defaultI18n.t; isCurrent: () => boolean;
  onCancel: () => void; onSaved: (source: RemoteSource) => void;
}) {
  const theme = useTheme(), current = useAttempt(isCurrent), flight = useRef(false), revision = useRef(0);
  const renderedRevision = revision.current;
  const [view, setView] = useState<RcloneConfigView>(), [remote, setRemote] = useState(''), [name, setName] = useState('');
  const [busy, setBusy] = useState(false), [error, setError] = useState<string>();
  const draft = useMemo(() => Object.freeze({view, remote, name}), [view, remote, name]);
  const latestDraft = useRef(draft); latestDraft.current = draft;
  const ownsDraft = () => current() && revision.current === renderedRevision && latestDraft.current === draft;
  const load = async () => {
    if (flight.current || !current()) return;
    flight.current = true; revision.current++; setBusy(true); setError(undefined);
    try {const result = await controller.accountConfig(current); if (current()) {setView(result); setRemote('');}}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  useEffect(() => {void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const state = controller.getSnapshot().sources, recovery = receiptCheckRequired(state);
  const selected = view?.remotes.find(value => value.name === remote && value.supported);
  // An unsupported offer keeps the server's own reason; an unknown reason stays the generic sentence.
  const unsupportedReason = (reason: string): MessageId => reason === 'encrypted' ? 'next.remoteSources.configEncrypted'
    : reason === 'wrapper' ? 'next.remoteSources.configWrapper'
    : reason === 'duplicate' ? 'next.remoteSources.configDuplicate'
    : reason === 'ambiguous' ? 'next.remoteSources.configAmbiguous'
    : 'next.remoteSources.configUnsupportedKind';
  const finish = () => {
    const state = controller.getSnapshot().sources, receipt = state.rclone;
    const row = receipt && !receipt.removed ? state.sources.find(value => value.id === receipt.sourceId && value.generation === receipt.generation) : undefined;
    if (current() && row) onSaved(row);
    else if (current()) setError(remoteSourceError(t, {code: state.errorCode}));
  };
  const save = async () => {
    if (flight.current || !ownsDraft() || !selected || !name.trim() || name.length > 100 || recovery) return;
    flight.current = true; setBusy(true); setError(undefined);
    try {await controller.configureFromAccount({name: draft.name, remote: selected.name}, ownsDraft); if (ownsDraft()) finish();}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  const recover = async () => {
    if (flight.current || !current()) return;
    flight.current = true; setBusy(true); setError(undefined);
    try {await controller.recover(); if (current()) finish();}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  const editable = !busy && !recovery && !state.busy;
  return <Page title={t('next.remoteSources.connectFromConfig')} t={t} busy={busy} onCancel={onCancel}
    footer={recovery ? <Button disabled={busy} onPress={() => {void recover();}}>{t('next.remoteSources.checkResult')}</Button>
      : <Button disabled={!editable || !selected || !name.trim() || name.length > 100} onPress={() => {void save();}}>{t('next.remoteSources.continue')}</Button>}>
    {view && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.configPath', {path: view.path})}</Text>}
    {view && !view.remotes.length && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t(view.path ? 'next.remoteSources.configEmpty' : 'next.remoteSources.configAbsent')}</Text>}
    {!!view?.remotes.length && <Select label={t('next.remoteSources.configRemotes')} value={remote} disabled={!editable}
      options={view.remotes.map((value, index) => ({
        value: value.supported ? value.name : `unsupported:${index}`,
        label: `${t('next.remoteSources.configRemoteLabel', {name: value.name, kind: value.kind || t('next.remoteSources.configUnknownKind')})}${value.supported ? '' : ` · ${t(unsupportedReason(value.reason))}`}`,
        disabled: !value.supported,
      }))}
      onValueChange={value => {if (flight.current || !current()) return; if (view.remotes.some(row => row.supported && row.name === value)) {revision.current++; setRemote(value); setError(undefined);}}}/>}
    {!!view?.truncated && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.configTruncated')}</Text>}
    <TextField label={t('next.remoteSources.name')} value={name} editable={editable} autoCorrect={false} onChangeText={value => {if (flight.current || !current()) return; revision.current++; setName(value);}}/>
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.configChecksOnSave')}</Text>
    <Button disabled={!editable} variant="text" onPress={() => {void load();}}>{t('next.remoteSources.refresh')}</Button>
    <Problem message={error}/>
  </Page>;
}
