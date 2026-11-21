import {useEffect, useRef, useState} from 'react';
import type {LiveSourceConfig, LiveLogoResult} from '../../../../../../packages/contracts/src/next/api';
import type {SourceSettingsDocument} from '../../../../../../packages/client-core/src/server-admin/live-source-settings';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {MessageId} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import {liveDocumentPicker} from '../../../screens/settings-live/live-document-picker';
import {sourceSettingsDraft as draftOf, sourceSettingsChange, type LiveSource, type SourceSettingsDraft as Draft} from '../../../screens/settings-live/live-owner-model';


/** A Live TV source's own settings on the rebuilt server (GET/PATCH /v1/admin/live/sources/{id} with its revision), the
 * same rows web's source dialog draws: name, on/off, who may watch, where it comes from, tuners, guide, how often it is
 * read again, which channels, numbering, and channel logos. Only what changed is sent. */

export function useLiveSourceSettings({controller, runtime, source, sources, open, onOpenChange, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; sources: readonly LiveSource[]; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const t = runtime.text.t;
  const [document, setDocument] = useState<SourceSettingsDocument>(), [draft, setDraft] = useState<Draft>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false);
  const [included, setIncluded] = useState<string>(), [choices, setChoices] = useState<readonly {id: string; name: string}[]>([]), [replace, setReplace] = useState(false), [logos, setLogos] = useState<LiveLogoResult>(), [saved, setSaved] = useState(false);
  const opening = useRef<object | undefined>(undefined);
  const id = source?.id;
  useEffect(() => {
    if (!open || !id) return;
    const ticket = {}; opening.current = ticket;
    setDocument(undefined); setDraft(undefined); setError(undefined); setIncluded(undefined); setChoices([]); setLogos(undefined); setSaved(false); setReplace(false);
    void controller.sourceSettings(id).then(value => {if (opening.current === ticket) {setDocument(value); setDraft(draftOf(value));}}, failure => {if (opening.current === ticket) setError(failure);});
    return () => {opening.current = undefined;};
  }, [open, id, controller]);
  const cfg = draft?.config, guideOnly = document?.source.kind === 'guide';
  const mode = included ?? (cfg?.channels?.length ? 'picked' : cfg?.groups?.length ? 'groups' : 'all');
  useEffect(() => {
    if (!id || !open || mode === 'all') {setChoices([]); return;}
    let live = true;
    void controller.sourceChoices(id, mode === 'groups' ? 'groups' : 'channels', '').then(page => {if (live) setChoices(page.items);}, () => {if (live) setChoices([]);});
    return () => {live = false;};
  }, [id, open, mode, controller]);
  const edit = (patch: Partial<Draft>) => {setSaved(false); setDraft(current => current && {...current, ...patch});};
  const config = (patch: Partial<LiveSourceConfig>) => {if (cfg) edit({config: {...cfg, ...patch}});};
  const changed = !!draft && !!document && (JSON.stringify([draft.name, draft.audience, draft.paused, draft.tunerCount, draft.config]) !== JSON.stringify([document.source.name, document.source.audience, document.source.paused, document.source.tunerCount, document.source.config]) || !!draft.username || !!draft.password || !!draft.playlist || !!draft.guide);
  const pair = !(draft?.username || draft?.password) || !!draft?.username && !!draft?.password;
  const save = async () => {
    if (!id || !draft || !document || busy) return;
    const body = sourceSettingsChange(document, draft);
    setBusy(true); setError(undefined);
    try {const next = await controller.saveSourceSettings(id, body, document.etag); const value = {...next, imagesEnabled: document.imagesEnabled}; setDocument(value); setDraft(draftOf(value)); setSaved(true); onSaved();}
    catch (failure) {setError(failure);}
    finally {setBusy(false);}
  };
  const pick = async (field: 'playlist' | 'guide') => {
    if (!liveDocumentPicker || busy) return;
    const picked = await liveDocumentPicker.pick(field, new AbortController().signal).catch(failure => {setError(failure); return undefined;});
    if (picked) edit(field === 'playlist' ? {playlist: picked.data, config: {...cfg!, uploaded: true, playlistUrl: ''}} : {guide: picked.data, config: {...cfg!, guideUploaded: true, guideUrl: ''}});
  };
  const importLogos = async () => {
    if (!id || busy) return;
    setBusy(true); setError(undefined);
    try {setLogos(await controller.importSourceLogos(id, replace));} catch (failure) {setError(failure);} finally {setBusy(false);}
  };
  const guides = sources.filter(value => value.id !== id);
  const toggle = (list: readonly string[] | undefined, value: string) => list?.includes(value) ? list.filter(item => item !== value) : [...(list ?? []), value];
  return {t, document, error, saved, busy, changed, pair, draft, cfg, guideOnly, guides, mode, choices, logos, replace, setReplace, setIncluded, edit, config, pick, importLogos, save, toggle};
}
