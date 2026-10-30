import {dependsOn} from '@core/freshness-hub.ts';
import {useFreshness} from '../../../../platform/freshness';
import {mountQuietInput, SettingsOpenContext} from '../../quiet-refresh';
import {useContext, useEffect, useCallback, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {useLocation} from '@tanstack/react-router';
import {settingsRoute} from '../../../../kit/settings/search';
import {defaultI18n} from '@i18n';
import type {LibraryChannel, LibraryChannelTemplate} from '@core/library-channels.ts';
import {Button} from '../../../../kit/controls';
import {Dialog} from '../../../../kit/overlays';
import {LibraryChannels, type ChannelAction} from './LibraryChannels';
import {ChannelEditor} from './ChannelEditor';
import {BlockPresets} from './BlockPresets';
import type {LibraryChannelsPageProps} from './types';
import styles from './channels.module.css';
import {createSources, forwardChannelPorts} from './channel-sources';

const t = defaultI18n.t;
let editorSequence = 0;
type Sources = ReturnType<typeof createSources>;
type Modal = Readonly<{kind: 'logo' | 'delete'; channel: LibraryChannel}>;

/** Mounted only with the real captured primary-owner/manageServer admission. */
export function LibraryChannelsPage(input: LibraryChannelsPageProps | Readonly<{ports: LibraryChannelsPageProps}>) {
  const props = 'ports' in input ? input.ports : input;
  const [,redrawFacts]=useState(0);
  useEffect(()=>props.subscribe?.(()=>redrawFacts(value=>value+1)),[props]);
  const location = useLocation();
  const initialChannelId = settingsRoute(Object.fromEntries(new URLSearchParams(location.searchStr)), () => true).channelId;
  const {api, serverId, isCurrent, reportError, uploadLogo} = props;
  const [sources, setSources] = useState<Sources>();
  useLayoutEffect(() => {
    const next = createSources({api, serverId, isCurrent, reportError, uploadLogo});setSources(next);
    return () => next.dispose();
  }, [api, serverId, isCurrent, reportError, uploadLogo]);
  return sources?.current() && isCurrent() ? <ChannelPageBody key={serverId} sources={sources} ports={forwardChannelPorts(props,{initialChannelId})}/> : null;
}

function ChannelPageBody({sources, ports}: Readonly<{sources: Sources; ports: LibraryChannelsPageProps}>) {
  const {hub}=useFreshness();
  const visibility=useContext(SettingsOpenContext);
  useEffect(() => {
    const interest=mountQuietInput(hub,'settings/channels',[dependsOn.list('libraryChannels'),dependsOn.settings('server'),dependsOn.libraries],
      {id:'directory-health-editor',refresh:sources.refreshQuiet,cancel:()=>{sources.reader.invalidateQuiet();sources.editor.invalidateQuiet();},subscribe:sources.subscribe,blocked:sources.blocked,settlement:sources.settlement},visibility);
    return () => interest.dispose();
  },[hub,sources,visibility]);
  const reader = useSyncExternalStore(sources.reader.subscribe, sources.reader.getSnapshot, sources.reader.getSnapshot);
  const deepLink = useRef<string | undefined>(undefined);
  const editor = useSyncExternalStore(sources.editor.subscribe, sources.editor.getSnapshot, sources.editor.getSnapshot);
  const action = useSyncExternalStore(sources.actions.subscribe, sources.actions.getSnapshot, sources.actions.getSnapshot);
  const logo = useSyncExternalStore(sources.logos.subscribe, sources.logos.getSnapshot, sources.logos.getSnapshot);
  const presets = useSyncExternalStore(sources.presets.subscribe, sources.presets.getSnapshot, sources.presets.getSnapshot);
  const [presetToken, setPresetToken] = useState<{id:number}>();
  const [modal, setModal] = useState<Modal>(), [editorToken, setEditorToken] = useState<{id: number; creating: boolean}>();
  const lastCreating = useRef(false), moving = useRef(false);
  const [reordering, setReordering] = useState(false), [reorderError, setReorderError] = useState(false), [announcement, setAnnouncement] = useState('');
  const view = useMemo<{active: boolean; source: Sources; modal?: Modal; editor?: object; preset?: object}>(() => ({active: true, source: sources}), [sources]);
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.modal = undefined;view.editor = undefined;view.preset = undefined;};}, [view]);
  const current = useCallback(() => view.active && view.source === sources && sources.current(), [view, sources]);
  const editorCurrent = useCallback(() => current() && editorToken !== undefined && view.editor === editorToken, [current, editorToken, view]);
  const presetCurrent = useCallback(() => current() && presetToken !== undefined && view.preset === presetToken, [current, presetToken, view]);
  const openPresets = () => {if (!current() || moving.current || sources.presets.getSnapshot().busy) return;const token = {id:++editorSequence};view.preset = token;setPresetToken(token);};
  const closePresets = () => {if (!presetCurrent() || sources.presets.getSnapshot().busy) return;view.preset = undefined;setPresetToken(undefined);};
  const blocked = reordering || presets.busy || !!presets.pending || presets.reconcile || editor.busy || action.busy || logo.busy || !!editor.pending || !!action.pending || !!logo.pending || editor.phase !== 'closed';
  const create = (template?: LibraryChannelTemplate) => {
    if (!current() || blocked || moving.current) return;
    const token = {id: ++editorSequence, creating: true};lastCreating.current = true;view.editor = token;setEditorToken(token);
    if (template) sources.editor.template(template, ports.timeZone);else void sources.editor.blank(ports.timeZone);
  };
  const closeEditor = () => {if (!editorCurrent() || sources.editor.getSnapshot().busy) return;sources.editor.close();view.editor = undefined;setEditorToken(undefined);};
  const close = () => {if (!current() || view.modal !== modal || sources.actions.getSnapshot().busy || sources.logos.getSnapshot().busy) return;view.modal = undefined;sources.setOpeningBlocked(false);setModal(undefined);};
  const actionOn = useCallback((channel: LibraryChannel, kind: ChannelAction) => {
    if (!current() || blocked || moving.current || !sources.reader.getSnapshot().channels?.includes(channel)) return;
    if (kind === 'edit') {const token = {id: ++editorSequence, creating: false};lastCreating.current = false;view.editor = token;setEditorToken(token);sources.editor.edit(channel);}
    else if (kind === 'logo' || kind === 'delete') {const next = {kind, channel};sources.setOpeningBlocked(true);view.modal = next;setModal(next);}
    else void sources.actions.perform(channel, kind === 'up' || kind === 'down' ? 'move' : kind, kind === 'up' ? -1 : kind === 'down' ? 1 : undefined);
  }, [current, blocked, sources, view]);
  useLayoutEffect(() => {
    const id = ports.initialChannelId;
    if (!id || deepLink.current === id || reader.phase !== 'ready' || blocked || !current()) return;
    deepLink.current = id;
    const channel = reader.channels?.find(value => value.config.id === id);
    if (channel) actionOn(channel, 'edit');
  }, [ports.initialChannelId, reader, blocked, current, actionOn]);
  const reorder = async (channel: LibraryChannel, target: number) => {
    if (!current() || blocked || moving.current) return;
    moving.current = true;setReordering(true);setReorderError(false);setAnnouncement('');
    try {
      const done = await sources.actions.moveTo(channel, target);
      if (current()) {
        setReorderError(!done);
        if (done) setAnnouncement(t('next.settings.moved', {name: channel.config.name, position: target + 1, count: sources.reader.getSnapshot().channels?.length ?? 0}));
      }
    } catch {if (current()) setReorderError(true);}
    finally {moving.current = false;if (current()) setReordering(false);}
  };
  useLayoutEffect(() => {sources.setOpeningBlocked(!!modal);return () => sources.setOpeningBlocked(false);},[sources,modal]);
  const held = modal && current() && view.modal === modal ? modal : undefined;
  const selected = () => !!held && current() && view.modal === held && sources.reader.getSnapshot().channels?.includes(held.channel) === true;
  const remove = async () => {const captured = held;if (!captured || captured.kind !== 'delete' || !selected()) return;const done = await sources.actions.perform(captured.channel, 'delete');if (done && current() && view.modal === captured) close();};
  const chooseLogo = async (file: File | undefined) => {const captured = held;if (!captured || captured.kind !== 'logo' || !file || !selected()) return;const done = await sources.logos.choose(captured.channel, file);if (done && current() && view.modal === captured) close();};
  if (!current()) return null;
  return <>
    {editor.pending && editor.phase === 'closed' && <div className={styles.warning} role="alert"><p>{t('error.unconfirmedChange')}</p><Button size="small" onClick={() => {if (!current() || sources.editor.getSnapshot().busy) return;const token = {id: ++editorSequence, creating: lastCreating.current};view.editor = token;setEditorToken(token);sources.editor.resume();}}>{t('action.tryAgain')}</Button></div>}
    {action.error && <div className={styles.warning} role="alert"><p>{t(action.pending ? 'error.unconfirmedChange' : 'error.unavailable')}</p>{action.pending && <Button size="small" disabled={action.busy} onClick={() => {if (current()) void sources.actions.retry();}}>{t('action.tryAgain')}</Button>}</div>}
    {logo.error && <div className={styles.warning} role="alert"><p>{t(logo.pending ? 'error.unconfirmedChange' : 'web.libraryChannels.logoSaveFailed')}</p>{logo.pending && <Button size="small" disabled={logo.busy} onClick={() => {if (current()) void sources.logos.retry();}}>{t('action.tryAgain')}</Button>}</div>}
    {(presets.pending || presets.reconcile) && !presetToken && <div className={styles.warning} role="alert"><p>{t('error.unconfirmedChange')}</p><Button size="small" disabled={presets.busy} onClick={openPresets}>{t('action.tryAgain')}</Button></div>}
    {reorderError && <p role="alert" className={styles.warning}>{t('error.unavailable')}</p>}
    <span className={styles.srOnly} role="status">{announcement}</span>
    <LibraryChannels source={sources.reader} isCurrent={current} busy={blocked} logo={channel => ports.logo(channel, sources.logos.revision(channel.config.id))} create={create} action={actionOn} reorder={(channel, target) => {void reorder(channel, target);}}/>
    {editorToken && <ChannelEditor key={editorToken.id} creating={editorToken.creating} sampleArt={ports.sampleArt} source={sources.editor} api={ports.api} reportError={sources.report} openPresets={() => {if (editorCurrent()) openPresets();}} presets={sources.presets} libraries={ports.libraries} isCurrent={editorCurrent} close={closeEditor}/>}
    {presetToken && <BlockPresets key={presetToken.id} source={sources.presets} api={ports.api} libraries={ports.libraries} isCurrent={presetCurrent} reportError={sources.report} close={closePresets}/>}
    <Dialog open={held?.kind === 'delete'} title={t('web.libraryChannels.deleteTitle', {name: held?.channel.config.name ?? ''})} description={t('web.libraryChannels.deleteBody')} onOpenChange={value => {if (!value) close();}} dismissOnBack={!action.busy} dismissOnOutsidePress={!action.busy} showCloseButton={!action.busy}
      footer={<><Button size="compact" variant="glass" disabled={action.busy} onClick={close}>{t('action.cancel')}</Button><Button size="compact" variant="glass" className={styles.danger} disabled={action.busy} onClick={() => {void remove();}}>{t('web.libraryChannels.delete')}</Button></>}><></></Dialog>
    <Dialog open={held?.kind === 'logo'} title={t('web.libraryChannels.uploadLogo')} onOpenChange={value => {if (!value) close();}} dismissOnBack={!logo.busy} dismissOnOutsidePress={!logo.busy} showCloseButton={!logo.busy}
      footer={<Button size="compact" variant="glass" disabled={logo.busy} onClick={close}>{t('action.cancel')}</Button>}>
      {held?.kind === 'logo' && <input key={`${held.channel.config.id}/${held.channel.revision}`} type="file" accept="image/jpeg,image/png,image/webp" aria-label={t('web.libraryChannels.uploadLogo')} disabled={logo.busy || !!logo.pending} onChange={event => {const file = event.target.files?.[0];void chooseLogo(file);event.target.value = '';}}/>}
    </Dialog>
  </>;
}
