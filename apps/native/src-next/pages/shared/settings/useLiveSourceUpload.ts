import {useLayoutEffect, useRef, useState} from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import type {UploadPreview} from '../../../screens/settings-live/live-owner-model';
import {liveDocumentPicker} from '../../../screens/settings-live/live-document-picker';

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

export function useLiveSourceUpload({controller, runtime, open, onOpenChange, onSaved}: {
  controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void;
}) {
  const t = runtime.text.t;
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
      else {target.phase = 'open';target.error = controller.getSnapshot().error ?? new Error('save_failed');changed();}
    } catch (failure) {
      if (active.current === target && !target.retired && target.phase === 'saving' && !target.abort.signal.aborted) {
        target.phase = 'open';target.error = failure;changed();
      }
    }
  };
  const clearError = () => {
    if (current && active.current === current && !current.retired) {current.error = undefined; changed();}
  };
  return {t, current, busy, input, reading, files, draft, revision, preview, close, choose, edit, check, save, clearError};
}
