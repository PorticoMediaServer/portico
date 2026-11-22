import {useEffect, useRef, useState} from 'react';
import type {LiveSourceConfig, LiveLogoResult} from '../../../../../../packages/contracts/src/next/api';
import type {SourceSettingsDocument} from '../../../../../../packages/client-core/src/server-admin/live-source-settings';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {MessageId} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import {liveDocumentPicker} from '../../../screens/settings-live/live-document-picker';
import {sourceSettingsDraft as draftOf, sourceSettingsChange, type LiveSource, type SourceSettingsDraft as Draft} from '../../../screens/settings-live/live-owner-model';

const K = 'next.settingsSpec.server.live.groups.2.rows', N = 'next.settingsSpec.server.live.groups.3.rows.0';

/** A Live TV source's own settings on the rebuilt server (GET/PATCH /v1/admin/live/sources/{id} with its revision), the
 * same rows web's source dialog draws: name, on/off, who may watch, where it comes from, tuners, guide, how often it is
 * read again, which channels, numbering, and channel logos. Only what changed is sent. Moved as it was. */
export function useSourceSettings({controller, runtime, source, sources, open, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; sources: readonly LiveSource[]; open: boolean; onSaved: () => void}) {
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
  const toggle = (list: readonly string[] | undefined, value: string) => list?.includes(value) ? list.filter(item => item !== value) : [...(list ?? []), value];
  const number = (value: string, low: number, high: number, use: (n: number) => void) => {const n = Number(value); if (Number.isInteger(n) && n >= low && n <= high) use(n);};
  return {t, document, draft, cfg, guideOnly, busy, saved, canPick: !!liveDocumentPicker,
    error: error === undefined ? undefined : presentError(error, 'server-console', {operation: 'action'}).body,
    canSave: changed && !busy && pair && !!draft?.name.trim(), changed, save: () => {void save();},
    edit, config, pick: (field: 'playlist' | 'guide') => {void pick(field);},
    onLabel: t(`${K}.0.label` as MessageId),
    audience: {label: t(`${K}.1.label` as MessageId), options: ['everyone', 'owner'].map((value, index) => ({value, label: t(`${K}.1.control.options.${index}.label` as MessageId)}))},
    tunerCount: (value: string) => number(value, 1, 256, n => edit({tunerCount: n})),
    guide: cfg && {label: t(`${K}.2.label` as MessageId), value: cfg.guideSource || (cfg.guideUrl || cfg.guideUploaded ? 'url' : 'own'),
      options: [{value: 'own', label: t('player.quality.automatic')}, ...sources.filter(value => value.id !== id).map(value => ({value: value.id, label: value.name})), {value: 'url', label: t(`${K}.2.control.options.2.label` as MessageId)}],
      choose: (value: string) => config(value === 'own' ? {guideSource: '', guideUrl: ''} : value === 'url' ? {guideSource: ''} : {guideSource: value, guideUrl: ''}),
      ownAddress: guideOnly || !cfg.guideSource && (cfg.guideUrl !== undefined && cfg.guideUrl !== '' || cfg.guideUploaded)},
    refresh: {label: t(`${K}.3.label` as MessageId), set: (value: string) => number(value, 1, 168, n => config({refreshHours: n}))},
    include: cfg && {label: t(`${N}.label` as MessageId), value: mode, options: ['all', 'groups', 'picked'].map((value, index) => ({value, label: t(`${N}.control.options.${index}.label` as MessageId)})),
      choose: (value: string) => {setIncluded(value); config({groups: value === 'groups' ? cfg.groups : [], channels: value === 'picked' ? cfg.channels : []});},
      choices: mode === 'all' ? [] : choices.map(choice => ({key: choice.id, name: choice.name, on: (mode === 'groups' ? cfg.groups : cfg.channels)?.includes(choice.id) === true,
        flip: () => config(mode === 'groups' ? {groups: toggle(cfg.groups, choice.id)} : {channels: toggle(cfg.channels, choice.id)})}))},
    numbering: (numbering: string) => {if (cfg) config({numbering, ...(numbering === 'sequential' ? {firstNumber: cfg.firstNumber || 1} : {})});},
    firstNumber: (value: string) => number(value, 1, 99999, n => config({firstNumber: n})),
    replace, setReplace, importLogos: () => {void importLogos();},
    logos: logos && t('web.liveSource.logoResult', {imported: logos.imported, skipped: logos.skipped, requested: logos.requested})};
}
