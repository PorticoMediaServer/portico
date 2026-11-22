import React, {useEffect, useState, type ReactNode} from 'react';
import type {LibraryAdminDocument, LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import type {InventorySource} from '../../../../../../packages/client-core/src/library-inventory';
import {libraryLyricsJobState} from '../../../../../../packages/client-core/src/owner-library-requests';
import {jobStateName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button, FormError, Select, Switch, Tabs, TextField} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {LibraryScanningNote, LibraryStatusControl, OwnerLibraryHeader, OwnerLibraryRow, OwnerLibraryRowsLoading, SkippedFileLine} from '../../../kit/settings/LibraryParts';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import {AddFolderPage, AddLibraryPage, LibraryTrashGroup, MetadataAgentChoice} from './LibraryFormPages';
import {LibrarySettingsRows} from '../../../kit/settings/LibrarySettingsRows';
import {ownerFolderLocation, ownerFolderState, ownerLibraryAudience, ownerLibraryInterval, ownerLibraryKind, ownerLibraryState, ownerUnmatchedReason} from '../../../screens/settings-live/owner-libraries/presentation';
import {useLibrarySonic, useServerLibraries, type LibraryPageTab} from '../../shared/settings/useServerLibraries';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;
type Translate = typeof defaultI18n.t;
type View = ReturnType<typeof useServerLibraries>;

/** Settings › Server › Libraries (in the settings kit): the owner's libraries with Add library, and one library's page
 * with its tabs (General, Folders, Metadata, Scanning, Advanced), its menus and its sheets. Adding a library or a folder
 * opens that form in the page's place. */
export function ServerLibrariesPage({ports, runtime, retry, initialLibraryId, lifetime, ...frame}: Frame & {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime;
  retry?: ReactNode; initialLibraryId?: string; lifetime?: NativeScreenLifetime}) {
  const view = useServerLibraries({ports, runtime, initialLibraryId, lifetime}), {t, state, library, document} = view, context = ports.context;
  if (view.adding) return <AddLibraryPage api={view.api} serverName={context.serverName} ports={ports} runtime={runtime} busy={state.busy} error={state.error}
    onCancel={() => view.setAdding(false)} onCreate={view.create}/>;
  if (view.folderForm && document) return <AddFolderPage api={view.api} serverName={context.serverName} ports={ports} runtime={runtime} source={view.folderForm.source}
    expectedRevision={state.inventory?.revision} busy={state.busy} error={state.error} onCancel={() => view.setFolderForm(undefined)} onSave={view.saveFolder}/>;
  const menu = <Menu open={Boolean(view.libraryMenu)} onOpenChange={() => view.setLibraryMenu(undefined)} title={view.libraryMenu?.name} items={view.menuItems}/>;
  if (library && view.header) return <>
    <SettingsPageFrame server={frame.server} title={library.name} testID="owner-library" back={{label: t('next.ownerLibraries.backLibraries'), onPress: view.closeLibrary}}
      tabs={<Tabs testID="owner-libraries-tabs" value={view.tab} onValueChange={value => view.setTab(value as LibraryPageTab)} accessibilityLabel={library.name}
        items={(['general', 'folders', 'metadata', 'scanning', 'advanced'] as const).map(id => ({value: id, label: t(id === 'folders' ? 'next.ownerLibraries.folders' : `next.ownerLibraries.tab.${id}`)}))}/>}>
      {retry}
      <SectionCard plain><OwnerLibraryHeader counts={view.header.counts} scanning={view.header.scanning ? t('next.ownerLibraries.scanning') : undefined}
        status={view.header.scanning ? undefined : view.header.status} actions={<>
          <Button size="compact" icon="i-refresh" variant={view.header.scanning ? 'glass' : 'primary'} disabled={view.header.scanning || !view.header.canScan} loading={state.busy}
            onPress={() => {void view.controller.scanNow();}}>{t(view.header.scanning ? 'next.ownerLibraries.scanning' : 'web.libraries.scanNow')}</Button>
          <Button variant="icon" size="compact" icon="i-more" accessibilityLabel={t('next.ownerLibraries.libraryMenu')} onPress={view.openPageMenu}/>
        </>}/></SectionCard>
      {state.detail === 'loading' && !document && <SectionCard plain><StateView variant="inline" title={t('web.libraries.loadingOne')}/></SectionCard>}
      {state.detail === 'error' && <StateView variant="inline" {...runtime.error(state.detailError, () => {void runtime.reportError(state.detailError);}, 'server-console')}/>}
      {state.error !== undefined && <SectionCard plain><FormError placement="inline">{view.errorText(state.error, 'save')}</FormError></SectionCard>}
      {view.tab === 'general' && <GeneralTab view={view} library={library} document={document}/>}
      {view.tab === 'folders' && <FoldersTab view={view}/>}
      {view.tab === 'metadata' && <MetadataTab view={view} document={document}/>}
      {view.tab === 'scanning' && <ScanningTab view={view} library={library} document={document} runtime={runtime}/>}
      {view.tab === 'scanning' && view.owner && <SectionCard plain><LibraryTrashGroup api={view.api} libraryId={library.id} parentCurrent={view.parentCurrent} t={t}/></SectionCard>}
      {view.tab === 'advanced' && <AdvancedTab view={view} document={document}/>}
    </SettingsPageFrame>
    {menu}
    <SkippedSheet view={view}/>
    <RemoveLibrarySheet view={view} library={library}/>
    <RenameSheet view={view} library={library}/>
    <RemoveFolderSheet view={view} source={view.removingFolder}/>
    <Menu open={Boolean(view.folderMenu)} onOpenChange={() => view.setFolderMenu(undefined)} title={view.folderMenu?.name || view.folderMenu?.path} items={view.folderMenuItems}/>
  </>;
  return <>
    <SettingsPageFrame {...frame} title={t('settings.server.libraries')} testID="owner-libraries" intro={t('next.ownerLibraries.lede')}>
      {retry}
      <SectionCard plain><Button size="compact" icon="i-plus" align="start" onPress={() => view.setAdding(true)}>{t('web.empty.addLibrary')}</Button></SectionCard>
      {state.directory.phase === 'error' && <StateView variant="inline" {...runtime.error(state.directory.error, () => {void view.controller.refresh();}, 'server-console')}/>}
      {view.listPhase === 'empty' && <SectionCard><SettingsRow first label={t('next.ownerLibraries.empty')} description={t('next.ownerLibraries.emptyHelp')}/></SectionCard>}
      {(view.rows.length > 0 || view.listPhase === 'loading') && <SectionCard help={t('next.ownerLibraries.moveHint')}>
        {view.rows.map((row, index) => <OwnerLibraryRow key={row.key} first={index === 0} name={row.name} counts={row.counts} status={row.status} live={row.live}
          covers={row.covers} square={row.square} onOpen={row.open} onMenu={row.menu} menuLabel={`${row.name}, ${t('next.ownerLibraries.libraryMenu')}`}/>)}
        {view.listPhase === 'loading' && <OwnerLibraryRowsLoading label={t('next.ownerLibraries.loading')}/>}
      </SectionCard>}
    </SettingsPageFrame>
    {menu}
  </>;
}

function GeneralTab({view, library, document}: {view: View; library: ManagedLibrary; document?: LibraryAdminDocument}) {
  const {t, state} = view, audience = ownerLibraryAudience(library, t);
  return <>
    <SectionCard>
      <SettingsRow navigates first label={t('next.ownerLibraries.nameLabel')} value={library.name} onPress={() => view.setRename(true)}/>
      <SettingsRow label={t('web.libraries.type')} value={t(ownerLibraryKind(library.kind).label)}/>
      {audience !== undefined && <SettingsRow label={t('next.ownerLibraries.whoCanSee')} description={audience}/>}
    </SectionCard>
    {document && <SectionCard plain><LibrarySettingsRows tab="general" document={document} busy={state.busy} t={t} save={patch => {void view.controller.save(patch);}}/></SectionCard>}
    <SectionCard label={t('server.library.remove')}>
      <SettingsRow navigates first icon="i-trash" destructive label={t('server.library.removeRow')} description={t('server.library.removeHelp')} onPress={() => view.setLeaving(true)}/>
    </SectionCard>
  </>;
}

function FoldersTab({view}: {view: View}) {
  const {t, state} = view, sources = state.inventoryError === undefined ? state.inventory?.sources : undefined, canAdd = Boolean(state.inventory) && state.inventoryError === undefined;
  return <>
    {state.inventoryError !== undefined && <StateView variant="inline" title={view.errorText(state.inventoryError)}
      primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: () => {void view.controller.reloadInventory();}}}/>}
    {state.inventoryLoading && <SectionCard plain><StateView variant="inline" title={t('web.libraries.loadingOne')}/></SectionCard>}
    {sources && <SectionCard label={t('next.ownerLibraries.folders')}>
      {!sources.length && <SettingsRow first label={t('web.libraries.noFolders')}/>}
      {sources.map((folder, index) => <SettingsRow navigates key={folder.id} first={index === 0} icon="i-folder" label={folder.name || folder.path}
        description={`${folder.path} · ${ownerFolderLocation(folder, t)}`} value={ownerFolderState(folder, t)} onPress={() => view.setFolderMenu(folder)}/>)}
      <SettingsRow navigates icon="i-plus" label={t('web.libraries.addFolder')} onPress={canAdd ? () => view.setFolderForm({}) : undefined}/>
    </SectionCard>}
  </>;
}

function MetadataTab({view, document}: {view: View; document?: LibraryAdminDocument}) {
  const {t, state} = view, metadata = state.metadata;
  if (!document) return null;
  return <>
    {metadata && metadata.agents.length > 0 && <SectionCard label={t('next.ownerLibraries.metadataSource')} help={t('next.ownerLibraries.metadataHelp')}>
      {metadata.agents.map((option, index) => <MetadataAgentChoice key={option.id} option={option} first={index === 0} checked={option.id === metadata.agent}
        onPress={() => {if (!state.busy && option.id !== metadata.agent) void view.controller.saveAgent(option.id);}}/>)}
    </SectionCard>}
    <SectionCard plain><LibrarySettingsRows tab="metadata" document={document} busy={state.busy || !!state.lyricsBusy} t={t} save={patch => {void view.controller.save(patch);}}
      fetchLyrics={current => view.controller.fetchLyrics(document, () => view.parentCurrent() && current())}/></SectionCard>
    {state.lyricsError !== undefined && <SectionCard plain><FormError placement="inline">{view.errorText(state.lyricsError)}</FormError></SectionCard>}
    {state.lyricsJob && <SectionCard plain><StateView variant="inline" title={jobStateName(t, id => defaultI18n.has(id), libraryLyricsJobState(state.lyricsJob.state))}/></SectionCard>}
    <SectionCard><SettingsRow first label={t('server.library.ownKeys')} description={t('server.library.ownKeysHelp')}/></SectionCard>
    <SectionCard label={t('next.ownerLibraries.tab.attention')} help={t('next.ownerLibraries.unmatchedLede')}>
      {!state.unmatched.length && <SettingsRow first label={t(state.unmatchedLoading ? 'web.libraries.loadingOne' : 'next.ownerLibraries.unmatchedEmpty')}/>}
      {state.unmatched.map((title, index) => <SettingsRow key={title.itemId} first={index === 0} icon="i-note" label={title.year ? `${title.title} (${title.year})` : title.title}
        description={ownerUnmatchedReason(title.reason, t)}/>)}
      {state.unmatchedCursor ? <SettingsRow navigates label={t('next.ownerLibraries.showMore')} onPress={() => {void view.controller.loadUnmatchedMore();}}/> : null}
    </SectionCard>
  </>;
}

function ScanningTab({view, library, document, runtime}: {view: View; library: ManagedLibrary; document?: LibraryAdminDocument; runtime: NativeShellRuntime}) {
  const {t, state, controller} = view, sonic = useLibrarySonic(state, document, view.parentCurrent, t), settings = document?.settings;
  if (!document || !settings) return null;
  const locale = runtime.text.locale ?? defaultI18n.region.formatLocale, scanning = view.header?.scanning;
  const skippedCount = state.skipped?.items.length || (library.filesSkipped ?? 0), lease = sonic.pageLease;
  const sonicLabel = t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.label'), statusLabel = t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label');
  const tryAgain = (reload: boolean) => <Button variant="glass" size="xs" onPress={() => {if (!lease || !sonic.sonic) return; if (reload) void controller.reloadSonic(document, sonic.sonic, lease); else void controller.retrySonic(document, sonic.sonic, lease);}}>{t('action.tryAgain')}</Button>;
  const rescans = document.sources.filter(folder => folder.eligibleControls?.includes('libraries.rescan'));
  return <>
    {scanning && <SectionCard plain><LibraryScanningNote text={t('next.ownerLibraries.scanningNote')}
      action={<Button variant="glass" size="xs" trailingIcon="i-right" onPress={view.dashboard}>{t('next.ownerLibraries.openDashboard')}</Button>}/></SectionCard>}
    <SectionCard>
      <SettingsRow first label={t('next.ownerLibraries.lastScan')} value={ownerLibraryState(library, {t, locale}).text}
        control={scanning ? <Button variant="glass" size="xs" onPress={() => {void controller.cancelScan();}}>{t('web.libraries.cancelScan')}</Button> : undefined}/>
      <SettingsRow navigates label={t('next.ownerLibraries.filesSkippedTitle')} description={t('next.ownerLibraries.filesSkippedHelp')} value={t('next.ownerLibraries.filesCount', {count: skippedCount})}
        onPress={() => view.setSkipped(true)}/>
    </SectionCard>
    <SectionCard>
      <SettingsRow first label={t('next.ownerLibraries.introsAndCredits')} control={<Select testID="owner-libraries-segments" accessibilityLabel={t('next.ownerLibraries.introsAndCredits')} value={settings.segmentDetection}
        options={[{value: 'server', label: t('next.ownerLibraries.introsFollow')}, {value: 'standard', label: t('settings.server.segmentMode.standard')}, {value: 'thorough', label: t('settings.server.segmentMode.thorough')}, {value: 'off', label: t('settings.server.segmentMode.off')}]}
        onValueChange={value => {void controller.save({segmentDetection: value as LibrarySettings['segmentDetection']});}}/>}/>
    </SectionCard>
    <SectionCard plain><LibrarySettingsRows tab="scanning" document={document} busy={state.busy} t={t} save={patch => {void controller.save(patch);}}/></SectionCard>
    {(sonic.toggle || sonic.line || sonic.errorText || (state.sonicNeedsReload && sonic.sonic)) && <SectionCard>
      {sonic.toggle && <SettingsRow first label={sonicLabel} description={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.help')}
        control={<Switch testID="owner-libraries-sonic" checked={sonic.toggle.current === true} disabled={state.sonicBusy || state.sonicNeedsReload} accessibilityLabel={sonicLabel}
          onCheckedChange={enabled => {if (lease && sonic.sonic) void controller.setSonicEnabled(enabled, document, sonic.sonic, lease);}}/>}/>}
      {sonic.line ? <SettingsRow first={!sonic.toggle} label={statusLabel}
        description={state.sonicRetryUnavailable && sonic.unavailable ? t('next.settings.librarySonic.retryUnavailable') : sonic.errorText}
        control={<LibraryStatusControl text={sonic.line.text} tone={sonic.line.tone === 'warn' ? 'warn' : sonic.line.tone === 'ok' ? 'ok' : 'quiet'}
          action={(sonic.unavailable || state.sonicNeedsReload) && !state.sonicBusy ? tryAgain(!!state.sonicNeedsReload) : undefined}/>}/>
        : sonic.errorText ? <SettingsRow first label={statusLabel} control={<LibraryStatusControl text={sonic.errorText} alert
          action={state.sonicNeedsReload && sonic.sonic && !state.sonicBusy ? tryAgain(true) : undefined}/>}/>
          : state.sonicNeedsReload && sonic.sonic ? <SettingsRow first={!sonic.toggle} label={statusLabel} control={!state.sonicBusy ? tryAgain(true) : undefined}/> : null}
    </SectionCard>}
    {rescans.length > 0 && <SectionCard>
      {rescans.map((folder, index) => <SettingsRow key={folder.id} first={index === 0} label={t('next.ownerLibraries.rescan', {folder: folder.name || folder.path})} value={ownerLibraryInterval(folder.intervalSeconds, t)}/>)}
    </SectionCard>}
    <SectionCard>
      <SettingsRow navigates first icon="i-refresh" label={t('next.ownerLibraries.scanAll')} description={t('next.ownerLibraries.scanAllHelp')} onPress={() => {void controller.scanAll();}}/>
    </SectionCard>
  </>;
}

function AdvancedTab({view, document}: {view: View; document?: LibraryAdminDocument}) {
  const {t, state} = view;
  if (!document || !view.owner || !document.eligibleControls.includes('libraries.allowDelete')) return null;
  return <SectionCard>
    <SettingsRow first label={t('next.ownerLibraries.allowDeletion')} description={t('next.ownerLibraries.allowDeletionHelp')}
      control={<Switch testID="owner-libraries-deletion" disabled={state.busy} checked={document.settings.allowMediaDeletion} accessibilityLabel={t('next.ownerLibraries.allowDeletion')}
        onCheckedChange={next => {void view.controller.save({allowMediaDeletion: next});}}/>}/>
  </SectionCard>;
}

function SkippedSheet({view}: {view: View}) {
  const {t, state} = view, items = state.skipped?.items ?? [];
  return <BottomSheet open={view.skipped} onOpenChange={next => view.setSkipped(next)} title={t('next.ownerLibraries.filesSkippedTitle')} description={t('next.ownerLibraries.filesSkippedLede')} scrollable
    footer={<OverlayFootRow layout="halves"><Button size="small" icon="i-refresh" onPress={() => {void view.controller.scanNow();}}>{t('next.ownerLibraries.scanAgain')}</Button>
      <Button variant="glass" size="small" onPress={() => view.setSkipped(false)}>{t('action.close')}</Button></OverlayFootRow>}>
    <SheetColumn>
      {state.skippedLoading && <StateView variant="inline" title={t('next.ownerLibraries.loading')}/>}
      {!state.skippedLoading && !items.length && <StateView variant="inline" title={t('next.ownerLibraries.skippedEmpty')}/>}
      {items.map((item, index) => <SkippedFileLine key={item.path} first={index === 0} path={item.path} message={item.message}/>)}
      {state.skipped?.nextCursor ? <Button variant="glass" size="small" onPress={() => {void view.controller.loadSkippedMore();}}>{t('next.ownerLibraries.showMore')}</Button> : null}
    </SheetColumn>
  </BottomSheet>;
}

/** The column of a sheet's lines and fields. */
function SheetColumn({children}: {children: ReactNode}) {return <SectionCard plain>{children}</SectionCard>;}

function failureText(error: unknown, t: Translate) {return error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed');}

function RemoveLibrarySheet({view, library}: {view: View; library: ManagedLibrary}) {
  const {t, state} = view, [typed, setTyped] = useState('');
  useEffect(() => {if (view.leaving) setTyped('');}, [view.leaving]);
  const confirmed = typed === library.name;
  return <BottomSheet open={view.leaving} onOpenChange={next => view.setLeaving(next)} title={t('server.library.removeTitle', {name: library.name})} scrollable
    footer={<OverlayFootRow layout="halves"><Button variant="glass" size="small" onPress={() => view.setLeaving(false)}>{t('action.cancel')}</Button>
      <Button variant="danger" size="small" loading={state.busy} disabled={!confirmed || state.busy} onPress={() => view.removeLibrary(typed)}>{t('server.library.remove')}</Button></OverlayFootRow>}>
    <SheetColumn>
      <StateView variant="inline" title={t('server.library.removeGoes')} body={t('server.library.removeKept')}/>
      <TextField focusId="owner-libraries-remove-name" value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('next.ownerLibraries.typeToConfirm')} placeholder={t('next.ownerLibraries.typeToConfirm')} showSoftInputOnFocus/>
      {state.error !== undefined && <FormError placement="inline">{failureText(state.error, t)}</FormError>}
    </SheetColumn>
  </BottomSheet>;
}

function RemoveFolderSheet({view, source}: {view: View; source?: InventorySource}) {
  const {t, state} = view, [typed, setTyped] = useState(''), open = Boolean(source);
  useEffect(() => {if (open) setTyped('');}, [open]);
  const name = source?.name || source?.path || '', confirmed = Boolean(name) && typed === name;
  return <BottomSheet open={open} onOpenChange={() => view.setRemovingFolder(undefined)} title={t('web.libraries.removeFolderTitle', {name})} description={t('next.ownerLibraries.removeFolderBody')}
    footer={<OverlayFootRow layout="halves"><Button variant="glass" size="small" onPress={() => view.setRemovingFolder(undefined)}>{t('action.cancel')}</Button>
      <Button variant="danger" size="small" loading={state.busy} disabled={!confirmed || state.busy} onPress={view.removeFolder}>{t('web.libraries.removeFolder')}</Button></OverlayFootRow>}>
    <SheetColumn>
      <TextField focusId="owner-libraries-remove-folder" value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('next.ownerLibraries.typeToConfirm')} placeholder={t('next.ownerLibraries.typeToConfirm')} showSoftInputOnFocus/>
      {state.error !== undefined && <FormError placement="inline">{failureText(state.error, t)}</FormError>}
    </SheetColumn>
  </BottomSheet>;
}

function RenameSheet({view, library}: {view: View; library: ManagedLibrary}) {
  const {t, state} = view, [name, setName] = useState(library.name);
  useEffect(() => {if (view.rename) setName(library.name);}, [view.rename, library.name]);
  const valid = name.trim().length > 0 && name.trim() !== library.name;
  return <BottomSheet open={view.rename} onOpenChange={next => view.setRename(next)} title={t('next.ownerLibraries.renameTitle')}
    footer={<OverlayFootRow layout="halves"><Button variant="glass" size="small" onPress={() => view.setRename(false)}>{t('action.cancel')}</Button>
      <Button size="small" loading={state.busy} disabled={!valid || state.busy} onPress={() => view.renameLibrary(name.trim())}>{t('next.ownerLibraries.save')}</Button></OverlayFootRow>}>
    <SheetColumn>
      <TextField focusId="owner-libraries-rename" value={name} onChangeText={setName} maxLength={100} accessibilityLabel={t('next.ownerLibraries.nameLabel')} showSoftInputOnFocus/>
      {state.error !== undefined && <FormError placement="inline">{failureText(state.error, t)}</FormError>}
    </SheetColumn>
  </BottomSheet>;
}

