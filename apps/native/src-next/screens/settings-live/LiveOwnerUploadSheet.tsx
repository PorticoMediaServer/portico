import React, {useLayoutEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Button, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeLiveOwnerController} from './live-owner-controller';
import type {UploadPreview} from './live-owner-model';
import {liveDocumentPicker} from './live-document-picker';

type Picked = Readonly<{title: string; bytes: number}>;
type UploadInput = Readonly<{id: string; requestId: string; expectedRevision: number; name: string; playlist: string; guide: string; tunerCount: number}>;
/** The committed lease for one drawn confirmation. Edits and flights mutate the
 * same family; cleanup always retires this family and its latest picker/text. */
type Opening = {
  readonly id: number;
  readonly abort: AbortController;
  phase: 'open' | 'checking' | 'saving' | 'closed';
  draft: UploadInput;
  revision: number;
  preview?: Readonly<{revision: number; value: UploadPreview}>;
  files: Readonly<{playlist?: Picked; guide?: Picked}>;
  reading?: 'playlist' | 'guide';
  pick?: AbortController;
  error?: unknown;
  retired: boolean;
};

const limit = 16 * 1024 * 1024;
const blank: UploadInput = Object.freeze({id: '', requestId: '', expectedRevision: 0, name: '', playlist: '', guide: '', tunerCount: 0});
const sizeLabel = (bytes: number) => defaultI18n.bytes(bytes);
/** Idempotency/source ids are not secrets; use the device crypto when present. */
const makeId = () => {
  const bytes = new Uint8Array(24), crypto = globalThis.crypto;
  if (crypto?.getRandomValues) crypto.getRandomValues(bytes);
  else for (let index = 0; index < bytes.length; index += 1) bytes[index] = Math.floor(Math.random() * 256);
  return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('');
};
const emptyInput = (): UploadInput => Object.freeze({id: makeId(), requestId: makeId(), expectedRevision: 0, name: '', playlist: '', guide: '', tunerCount: 0});
const byteLength = (value: string) => new TextEncoder().encode(value).length;

/** live-owner.html 13b: an uploaded M3U plus an optional XMLTV guide. The same
 * direct live-source preview/save contracts the web uses; files stay in memory. */
export function UploadSourceSheet({controller, runtime, open, onOpenChange, onSaved}: {
  controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void;
}) {
  const theme = useTheme(), t = runtime.text.t;
  const active = useRef<Opening | undefined>(undefined), sequence = useRef(0);
  const [, render] = useState(0);
  const changed = () => render(value => value + 1);
  const opening = active.current;
  const current = opening && !opening.retired ? opening : undefined;
  const retire = (target: Opening) => {
    if (target.retired) return;
    target.retired = true;target.phase = 'closed';target.abort.abort();target.pick?.abort();
    // Uploaded text does not outlive the lease that collected it.
    target.draft = emptyInput();target.preview = undefined;target.files = {};target.reading = undefined;target.pick = undefined;target.error = undefined;
  };
  useLayoutEffect(() => {
    let owned: Opening | undefined;
    if (open) {
      owned = {id: ++sequence.current, abort: new AbortController(), phase: 'open', draft: emptyInput(), revision: 0, files: {}, retired: false};
      active.current = owned;changed();
    }
    return () => {
      if (!owned) return;
      if (active.current === owned) active.current = undefined;
      retire(owned);
    };
  }, [open, controller]);
  const close = (target: Opening) => {
    if (active.current !== target) return;
    active.current = undefined;
    retire(target);
    changed();
    onOpenChange(false);
  };
  const draft = current?.draft, input = draft ?? blank, files = current?.files ?? {}, preview = current?.preview, revision = current?.revision, busy = current?.phase === 'checking' || current?.phase === 'saving', reading = current?.reading;
  const edit = (target: Opening, patch: Pick<Partial<UploadInput>, 'name' | 'tunerCount'>) => {
    if (active.current !== target || target.retired || target.phase !== 'open' || target.reading || target.abort.signal.aborted) return;
    target.draft = Object.freeze({...target.draft, ...patch, requestId: makeId()});
    target.revision += 1;target.preview = undefined;target.error = undefined;changed();
  };
  const choose = async (target: Opening, field: 'playlist' | 'guide') => {
    if (!liveDocumentPicker || active.current !== target || target.retired || target.phase !== 'open' || target.reading || target.abort.signal.aborted) return;
    target.pick?.abort();
    const pickController = new AbortController();
    target.pick = pickController;target.reading = field;target.revision += 1;target.preview = undefined;target.error = undefined;changed();
    try {
      const picked = await liveDocumentPicker.pick(field, pickController.signal);
      if (!picked || active.current !== target || target.retired || target.phase !== 'open' || target.reading !== field ||
        target.abort.signal.aborted || pickController.signal.aborted) return;
      const other = field === 'playlist' ? target.draft.guide : target.draft.playlist;
      if (byteLength(picked.data) + byteLength(other) > limit) throw new Error('The playlist and guide together must be no larger than 16 MB.');
      target.draft = Object.freeze({...target.draft, [field]: picked.data, requestId: makeId()});
      target.revision += 1;target.reading = undefined;target.pick = undefined;
      target.files = {...target.files, [field]: {title: picked.title, bytes: picked.bytes}};
      changed();
    } catch (failure) {
      if (active.current === target && !target.retired && !target.abort.signal.aborted && !pickController.signal.aborted && target.reading === field) {
        target.reading = undefined;target.pick = undefined;target.error = failure;changed();
      }
    } finally {
      if (active.current === target && !target.retired && target.reading === field) {
        target.reading = undefined;target.pick = undefined;changed();
      }
    }
  };
  const check = async (target: Opening, renderedRevision: number, renderedDraft: UploadInput) => {
    if (active.current !== target || target.retired || target.phase !== 'open' || target.reading || target.pick ||
      target.revision !== renderedRevision || target.draft !== renderedDraft ||
      !renderedDraft.name.trim() || !renderedDraft.playlist || target.abort.signal.aborted) return;
    target.phase = 'checking';target.preview = undefined;target.error = undefined;changed();
    const caller = {isCurrent: () => active.current === target && !target.retired && target.phase === 'checking' && !target.abort.signal.aborted, signal: target.abort.signal};
    try {
      const value = await controller.previewFiles(renderedDraft, caller);
      if (active.current !== target || target.retired || target.phase !== 'checking' || target.abort.signal.aborted) return;
      target.phase = 'open';target.preview = {revision: target.revision, value};changed();
    } catch (failure) {
      if (active.current === target && !target.retired && target.phase === 'checking' && !target.abort.signal.aborted) {
        target.phase = 'open';target.error = failure;changed();
      }
    }
  };
  const save = async (target: Opening, renderedRevision: number, renderedDraft: UploadInput, renderedPreview: Readonly<{revision: number; value: UploadPreview}>) => {
    if (active.current !== target || target.retired || target.phase !== 'open' || target.reading || target.pick ||
      target.revision !== renderedRevision || target.draft !== renderedDraft || target.preview !== renderedPreview ||
      renderedPreview.revision !== renderedRevision || target.abort.signal.aborted) return;
    target.phase = 'saving';target.error = undefined;changed();
    const caller = {isCurrent: () => active.current === target && !target.retired && target.phase === 'saving' && !target.abort.signal.aborted, signal: target.abort.signal};
    try {
      const saved = await controller.saveFiles(renderedDraft, caller);
      if (active.current !== target || target.retired || target.phase !== 'saving' || target.abort.signal.aborted) return;
      if (saved) {onSaved();close(target);}
      else {target.phase = 'open';target.error = controller.getSnapshot().error ?? new Error('save_live_source');changed();}
    } catch (failure) {
      if (active.current === target && !target.retired && target.phase === 'saving' && !target.abort.signal.aborted) {
        target.phase = 'open';target.error = failure;changed();
      }
    }
  };
  const fileRow = (field: 'playlist' | 'guide', label: string) => <View style={{gap: 6}}>
    <Text style={[shellType(13, 700), {color: theme.colors.textSecondary}]}>{t(label as Parameters<typeof t>[0])}</Text>
    <Button testID={`live-owner-upload-${field}`} variant="glass" icon="i-upload" loading={reading === field} disabled={busy || !!reading}
      onPress={() => {if (current) void choose(current, field);}}>{files[field]?.title ?? t('web.metadata.chooseImageFile')}</Button>
    {files[field] && <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{sizeLabel(files[field]!.bytes)}</Text>}
  </View>;
  return <Dialog open={open && !!current} onOpenChange={value => {if (!busy && !value && current) close(current);}} title={t('web.liveTv.upload')} description={t('web.liveTv.uploadLede')} dismissOnBack={!busy} dismissOnOutsidePress={!busy} showClose={!busy}
    footer={<View style={{flexDirection: 'row', gap: 10}}>
      <Button variant="glass" disabled={busy} onPress={() => {if (current) close(current);}}>{t('action.cancel')}</Button>
      <Button variant="primary" loading={busy} disabled={busy || !!reading || !input.name.trim() || !input.playlist}
        onPress={() => {if (current && draft && revision !== undefined) {if (preview) void save(current, revision, draft, preview);else void check(current, revision, draft);}}}>{t(preview ? 'web.liveTv.addSource' : 'web.liveTv.checkFiles')}</Button>
    </View>} testID="live-owner-upload">
    <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{gap: 16}}>
      <TextField testID="live-owner-upload-name" label={t('web.liveTv.sourceName')} value={input.name} maxLength={100} editable={!(busy || !!reading)} onChangeText={name => {if (current) edit(current, {name});}}/>
      {fileRow('playlist', 'web.liveTv.playlistFile')}
      {fileRow('guide', 'web.liveTv.guideFile')}
      <TextField testID="live-owner-upload-tuners" label={t('web.dvrSettings.tuners')} description={t('web.liveTv.tunersHelp')} keyboardType="number-pad"
        value={String(input.tunerCount)} editable={!(busy || !!reading)}
        onChangeText={value => {const count = Number(value);if (current && Number.isSafeInteger(count) && count >= 0 && count <= 64) edit(current, {tunerCount: count});}}/>
      {current && current.error !== undefined && <StateView testID="live-owner-upload-error" {...runtime.error(current.error, () => {if (active.current === current && !current.retired) {current.error = undefined;changed();}})}/>}
      {preview && <View testID="live-owner-upload-preview" style={{backgroundColor: theme.colors.raisedHigh, borderRadius: 12, padding: 14, gap: 6}}>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[t('server.live.channelCount', {count: preview.value.channels}), t('server.live.programCount', {count: preview.value.programmes})].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(15, 700), {color: theme.colors.text}]}>{part}</Text>)}</View>
        {!!preview.value.names.length && <Text numberOfLines={3} style={[shellType(13), {color: theme.colors.textSecondary}]}>{preview.value.names.slice(0, 6).join(', ')}{preview.value.names.length > 6 ? '…' : ''}</Text>}
      </View>}
    </ScrollView>
  </Dialog>;
}
