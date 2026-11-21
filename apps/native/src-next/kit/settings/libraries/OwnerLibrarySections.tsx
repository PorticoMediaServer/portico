import {libraryLyricsJobState} from '../../../../../../packages/client-core/src/owner-library-requests';
import {jobStateName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {Text, View} from 'react-native';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import type {InventorySource} from '../../../../../../packages/client-core/src/library-inventory';
import type {LibraryAdminDocument, LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import type {LibraryMetadataAgent} from '../../../../../../packages/client-core/src/server-admin/metadata-agent';
import {librarySonicStatus} from '../../../../../../packages/client-core/src/server-admin/library-sonic-words';
import type {LibrarySonicDocument} from '../../../../../../packages/client-core/src/server-admin/library-sonic';
import {readLibrarySonicSettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button} from '../../controls/Button';
import {FormError} from '../../controls/FormError';
import {Focusable} from '../../controls/Focusable';
import {Select} from '../../controls/Select';
import {Switch} from '../../controls/Switch';
import {TextField} from '../../controls/TextField';
import {CoverFan} from '../../artwork';
import {BottomSheet} from '../../overlays/BottomSheet';
import {Icon} from '../../Icon';
import {alpha, shellType} from '../../shell/visual';
import {useTheme} from '../../theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {MetadataAgentChoice} from '../../../screens/settings-live/owner-libraries/AddLibraryPage';
import {SettingsRow} from '../../shell';
import {LibrarySettingsRows} from './LibrarySettingsRows';
import {sonicPageLease, type NativeOwnerLibrariesSnapshot, type SonicPageLease} from '../../../screens/settings-live/owner-libraries/controller';
import {ownerFolderLocation, ownerFolderState, ownerLibraryAudience, ownerLibraryCounts, ownerLibraryInterval, ownerLibraryKind, ownerLibraryScan, ownerLibrarySquare, ownerLibraryState, ownerUnmatchedReason} from '../../../screens/settings-live/owner-libraries/presentation';
import React, {useEffect, useLayoutEffect, useState} from 'react';
import {PanelBlock, SectionCard} from '../../frames';
import {SettingsHelp} from '../SettingsParts';


type Translate = typeof defaultI18n.t;

export function LibraryListRow({library, first, t, runtime, state, onOpen, onMenu}: {library: ManagedLibrary; first: boolean; t: Translate; runtime: NativeShellRuntime; state: Readonly<{text: string; live: boolean}>; onOpen: () => void; onMenu: () => void}) {
  const theme = useTheme();
  const counts = ownerLibraryCounts(library, t), square = ownerLibrarySquare(library.kind);
  const covers = (library.summary?.coverUrls ?? []).map(path => runtime.artwork(path)).filter((value): value is NonNullable<ReturnType<NativeShellRuntime['artwork']>> => Boolean(value));
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap,
    paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs, borderTopWidth: first ? 0 : theme.space.hairline,
    borderTopColor: theme.colors.lineSoft}}>
    <Focusable accessibilityRole="button" accessibilityLabel={library.name} onPress={onOpen} focusKind="row" focusRadius={10} style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <CoverFan covers={covers} shape={square ? 'square' : 'poster'} variant={square ? 'music' : 'library'} width={62} height={square ? 56 : 66}/>
      <View style={{flex: 1, minWidth: 0}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{library.name}</Text>
        {counts.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{counts.filter(Boolean).map((part, index) => <Text key={index} style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2}]}>{part}</Text>)}</View>}
        <Text style={[theme.type.rowHelp, {color: state.live ? theme.colors.accentStrong : theme.colors.textTertiary, marginTop: 3}]}>{state.text}</Text>
      </View>
    </Focusable>
    <Focusable accessibilityRole="button" accessibilityLabel={`${library.name}, ${t('next.ownerLibraries.libraryMenu')}`} onPress={onMenu} focusKind="chip" focusRadius={16} hitSlop={8}
      style={{width: 34, height: 34, borderRadius: 17, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .06)}}><Icon name="i-more" size={18} color={theme.colors.textSecondary}/></Focusable>
  </View>;
}

export function GeneralTab({library, document, t, renameLibrary, removeLibrary, busy, save}: {library: ManagedLibrary; document?: LibraryAdminDocument; t: Translate; renameLibrary: () => void; removeLibrary: () => void; busy: boolean; save: (patch: Partial<LibrarySettings>) => void}) {
  return <>
    <SectionCard>
      <SettingsRow navigates first label={t('next.ownerLibraries.nameLabel')} value={library.name} onPress={renameLibrary}/>
      <SettingsRow label={t('web.libraries.type')} value={t(ownerLibraryKind(library.kind).label)}/>
      {ownerLibraryAudience(library, t) !== undefined && <SettingsRow label={t('next.ownerLibraries.whoCanSee')} description={ownerLibraryAudience(library, t)}/>}
    </SectionCard>
    {document && <LibrarySettingsRows tab="general" document={document} busy={busy} t={t} save={save}/>}
    <SectionCard label={t('server.library.remove')}>
      <SettingsRow navigates first icon="i-trash" destructive label={t('server.library.removeRow')} description={t('server.library.removeHelp')} onPress={removeLibrary}/>
    </SectionCard>
  </>;
}

export function FoldersTab({state, retry, t, addFolder, folderMenu}: {state: NativeOwnerLibrariesSnapshot; retry: () => void; t: Translate; addFolder: () => void; folderMenu: (source: InventorySource) => void}) {
  const sources = state.inventoryError === undefined ? state.inventory?.sources : undefined;
  return <SectionCard label={t('next.ownerLibraries.folders')}>
    <PanelBlock>
      <Button variant="glass" size="compact" icon="i-plus" disabled={!state.inventory || state.inventoryError !== undefined}
        onPress={addFolder}>{t('web.libraries.addFolder')}</Button>
      {state.inventoryError !== undefined && <>
        <FormError placement="inline">{presentError(state.inventoryError, 'library').body}</FormError>
        <Button variant="glass" size="compact" onPress={retry}>{t('action.tryAgain')}</Button>
      </>}
    </PanelBlock>
    {state.inventoryLoading && <SettingsRow first label={t('web.libraries.loadingOne')}/>}
    {sources && !sources.length && <SettingsRow first label={t('web.libraries.noFolders')}/>}
    {sources?.map((folder, index) => <SettingsRow navigates key={folder.id} first={index === 0} icon="i-folder"
      label={folder.name || folder.path}
      description={`${folder.path} · ${ownerFolderLocation(folder, t)}`} value={ownerFolderState(folder, t)}
      onPress={() => folderMenu(folder)}/>)}
  </SectionCard>;
}

export function MetadataTab({document, metadata, busy, t, save, saveAgent, fetchLyrics, state, parentCurrent}: {document?: LibraryAdminDocument; metadata?: LibraryMetadataAgent; busy: boolean; t: Translate; save: (patch: Partial<LibrarySettings>) => void; saveAgent: (id: string) => void; fetchLyrics: (document: LibraryAdminDocument, current: () => boolean) => Promise<void>; state: NativeOwnerLibrariesSnapshot; parentCurrent: () => boolean}) {
  const theme = useTheme();
  if (!document) return null;
  return <>
    {metadata && metadata.agents.length > 0 && <SectionCard
      label={t('next.ownerLibraries.metadataSource')} help={t('next.ownerLibraries.metadataHelp')}>
      {metadata.agents.map((option, index) => <MetadataAgentChoice key={option.id} option={option} first={index === 0}
        checked={option.id === metadata.agent} onPress={() => {if (!busy && option.id !== metadata.agent) saveAgent(option.id);}}/>)}
    </SectionCard>}
    <LibrarySettingsRows tab="metadata" document={document} busy={busy || !!state.lyricsBusy} t={t} save={save} fetchLyrics={current => fetchLyrics(document, () => parentCurrent() && current())}/>
    {state.lyricsError !== undefined && <FormError placement="inline">{presentError(state.lyricsError, 'library').body}</FormError>}
    {state.lyricsJob && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{jobStateName(t, id => defaultI18n.has(id), libraryLyricsJobState(state.lyricsJob.state))}</Text>}
    <SectionCard><SettingsRow first label={t('server.library.ownKeys')} description={t('server.library.ownKeysHelp')}/></SectionCard>
  </>;
}

export function AttentionTab({state, t, more}: {state: NativeOwnerLibrariesSnapshot; t: Translate; more: () => void}) {
  return <>
    <SectionCard help={t('next.ownerLibraries.unmatchedLede')}>
      {!state.unmatched.length && <SettingsRow first label={t(state.unmatchedLoading ? 'web.libraries.loadingOne' : 'next.ownerLibraries.unmatchedEmpty')}/>}
      {state.unmatched.map((title, index) => <SettingsRow key={title.itemId} first={index === 0} icon="i-note" label={title.year ? `${title.title} (${title.year})` : title.title} description={ownerUnmatchedReason(title.reason, t)}/>)}
      {state.unmatchedCursor ? <SettingsRow navigates label={t('next.ownerLibraries.showMore')} onPress={more}/> : null}
    </SectionCard>
  </>;
}

export function ScanningTab({api, library, document, state, t, locale, skipped, cancel, dashboard, save, scanAll, parentCurrent, sonicToggle, sonicRetry, sonicReload}: {api: LibraryContentApi; library: ManagedLibrary; document?: LibraryAdminDocument; state: NativeOwnerLibrariesSnapshot; t: Translate; locale: string; skipped: () => void; cancel: () => void; dashboard: () => void; save: (patch: Partial<LibrarySettings>) => void; scanAll: () => void; parentCurrent: () => boolean;
  sonicToggle: (enabled: boolean, document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void;
  sonicRetry: (document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void;
  sonicReload: (document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void}) {
  const theme = useTheme(), active = ownerLibraryScan(library), settings = document?.settings;
  const [pageLease, setPageLease] = useState<SonicPageLease | null>(null);
  useLayoutEffect(() => {
    const controller = new AbortController();
    const lease: SonicPageLease = sonicPageLease(controller, parentCurrent);
    setPageLease(lease);
    return () => {controller.abort();};
  }, [parentCurrent]);
  if (!document || !settings) return null;
  const sonic = state.sonic?.libraryId === document.libraryId ? state.sonic : undefined;
  const toggle = sonic ? readLibrarySonicSettingsBinding('libraries.sonicAnalysis', document, sonic) : undefined;
  const sonicLine = sonic ? librarySonicStatus(sonic, t, at => defaultI18n.dateTime(new Date(at))) : undefined;
  const sonicUnavailable = !!sonic && (!sonic.available || sonic.state === 'unavailable');
  const sonicErrorText = state.sonicError !== undefined ? presentError(state.sonicError, 'library', {operation: state.sonicErrorOperation ?? 'load'}).body : undefined;
  const skippedCount = state.skipped?.items.length || (library.filesSkipped ?? 0);
  return <>
    {active && <View style={{gap: 8, padding: 12, marginTop: 4, borderRadius: 12, backgroundColor: alpha(theme.colors.accent, .12)}}>
      <View style={{flexDirection: 'row', gap: 10}}>
        <Icon name="i-info" size={18} color={theme.colors.accentStrong}/>
        <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textSecondary, flex: 1}]}>{t('next.ownerLibraries.scanningNote')}</Text>
      </View>
      <Button variant="glass" size="xs" trailingIcon="i-right" onPress={dashboard} style={{alignSelf: 'flex-start', marginLeft: 28}}>{t('next.ownerLibraries.openDashboard')}</Button>
    </View>}
    <SectionCard>
      <SettingsRow first label={t('next.ownerLibraries.lastScan')} value={ownerLibraryState(library, {t, locale}).text}
        control={active ? <Button variant="glass" size="xs" onPress={cancel}>{t('web.libraries.cancelScan')}</Button> : undefined}/>
      <SettingsRow navigates label={t('next.ownerLibraries.filesSkippedTitle')} description={t('next.ownerLibraries.filesSkippedHelp')} value={t('next.ownerLibraries.filesCount', {count: skippedCount})} onPress={skipped}/>
    </SectionCard>
    <SectionCard>
      <SettingsRow first label={t('next.ownerLibraries.introsAndCredits')} control={<Select testID="owner-libraries-segments" accessibilityLabel={t('next.ownerLibraries.introsAndCredits')} value={settings.segmentDetection}
        options={[{value: 'server', label: t('next.ownerLibraries.introsFollow')}, {value: 'standard', label: t('settings.server.segmentMode.standard')}, {value: 'thorough', label: t('settings.server.segmentMode.thorough')}, {value: 'off', label: t('settings.server.segmentMode.off')}]}
        onValueChange={value => save({segmentDetection: value as LibrarySettings['segmentDetection']})}/>}/>
    </SectionCard>
    <LibrarySettingsRows tab="scanning" document={document} busy={state.busy} t={t} save={save}/>
    {(toggle || sonicLine || sonicErrorText || (state.sonicNeedsReload && sonic)) && <SectionCard>
      {toggle && <SettingsRow first label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.label')} description={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.help')}
        control={<Switch testID="owner-libraries-sonic" checked={toggle.current === true} disabled={state.sonicBusy || state.sonicNeedsReload} accessibilityLabel={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.label')} onCheckedChange={enabled => {if (pageLease) sonicToggle(enabled, document, sonic!, pageLease);}}/>}/>}
      {sonicLine ? <SettingsRow first={!toggle} label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label')}
        description={state.sonicRetryUnavailable && sonicUnavailable ? t('next.settings.librarySonic.retryUnavailable') : sonicErrorText}
        control={<View style={{flexDirection: 'row', alignItems: 'center', gap: 10, flexShrink: 1}}><Text style={[shellType(14.5), {color: sonicLine.tone === 'warn' ? theme.colors.amber : sonicLine.tone === 'ok' ? theme.colors.healthy : theme.colors.textSecondary}]}>{sonicLine.text}</Text>{(sonicUnavailable || state.sonicNeedsReload) && !state.sonicBusy ? <Button variant="glass" size="xs" onPress={() => {if (!pageLease) return; if (state.sonicNeedsReload) sonicReload(document, sonic!, pageLease); else sonicRetry(document, sonic!, pageLease);}}>{t('action.tryAgain')}</Button> : undefined}</View>}/>
                : sonicErrorText ? <SettingsRow first label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label')} control={<View style={{flexDirection: 'row', alignItems: 'center', gap: 10, flexShrink: 1}}><Text accessibilityRole="alert" style={[shellType(13, 600, 18), {color: theme.colors.record}]}>{sonicErrorText}</Text>{state.sonicNeedsReload && sonic && !state.sonicBusy ? <Button variant="glass" size="xs" onPress={() => {if (pageLease) sonicReload(document, sonic!, pageLease);}}>{t('action.tryAgain')}</Button> : undefined}</View>}/>
          : state.sonicNeedsReload && sonic ? <SettingsRow first={!toggle} label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label')} control={!state.sonicBusy ? <Button variant="glass" size="xs" onPress={() => {if (pageLease) sonicReload(document, sonic!, pageLease);}}>{t('action.tryAgain')}</Button> : undefined}/> : null}
    </SectionCard>}
    {document.sources.some(folder => folder.eligibleControls?.includes('libraries.rescan')) && <SectionCard>
      {document.sources.filter(folder => folder.eligibleControls?.includes('libraries.rescan')).map((folder, index) => <SettingsRow key={folder.id} first={index === 0} label={t('next.ownerLibraries.rescan', {folder: folder.name || folder.path})} value={ownerLibraryInterval(folder.intervalSeconds, t)}/>)}
    </SectionCard>}
    <SectionCard>
      <SettingsRow navigates first icon="i-refresh" label={t('next.ownerLibraries.scanAll')} description={t('next.ownerLibraries.scanAllHelp')} onPress={scanAll}/>
    </SectionCard>
  </>;
}

export function AdvancedTab({document, t, owner, busy, save}: {document?: LibraryAdminDocument; t: Translate; owner: boolean; busy: boolean; save: (patch: Partial<LibrarySettings>) => void}) {
  if (!document || !owner || !document.eligibleControls.includes('libraries.allowDelete')) return null;
  return <SectionCard>
    <SettingsRow first label={t('next.ownerLibraries.allowDeletion')} description={t('next.ownerLibraries.allowDeletionHelp')}
      control={<Switch testID="owner-libraries-deletion" disabled={busy} checked={document.settings.allowMediaDeletion} accessibilityLabel={t('next.ownerLibraries.allowDeletion')} onCheckedChange={next => save({allowMediaDeletion: next})}/>}/>
  </SectionCard>;
}

export function SkippedSheet({open, onOpenChange, state, t, scan, more}: {open: boolean; onOpenChange: (open: boolean) => void; state: NativeOwnerLibrariesSnapshot; t: Translate; scan: () => void; more: () => void}) {
  const theme = useTheme(), items = state.skipped?.items ?? [];
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('next.ownerLibraries.filesSkippedTitle')} description={t('next.ownerLibraries.filesSkippedLede')} scrollable
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button size="small" icon="i-refresh" onPress={scan} style={{flex: 1}}>{t('next.ownerLibraries.scanAgain')}</Button><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.close')}</Button></View>}>
    <PanelBlock inset={false}>
      {state.skippedLoading && <Text style={[shellType(13, 500), {color: theme.colors.textTertiary}]}>{t('next.ownerLibraries.loading')}</Text>}
      {!state.skippedLoading && !items.length && <Text style={[shellType(13, 500), {color: theme.colors.textTertiary}]}>{t('next.ownerLibraries.skippedEmpty')}</Text>}
      {items.map((item, index) => <View key={item.path} style={{paddingVertical: 10, borderTopWidth: index ? 1 : 0, borderTopColor: theme.colors.lineSoft}}>
        <Text style={[shellType(12, 500, 16), {color: theme.colors.textSecondary, fontFamily: 'monospace'}]}>{item.path}</Text>
        <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary, marginTop: 3}]}>{item.message}</Text>
      </View>)}
      {state.skipped?.nextCursor ? <Button variant="glass" size="small" onPress={more} style={{marginTop: 10}}>{t('next.ownerLibraries.showMore')}</Button> : null}
    </PanelBlock>
  </BottomSheet>;
}

export function RemoveLibrarySheet({open, onOpenChange, library, busy, error, t, remove}: {open: boolean; onOpenChange: (open: boolean) => void; library: ManagedLibrary; busy: boolean; error?: unknown; t: Translate; remove: (confirmation: string) => void}) {
  const [typed, setTyped] = useState('');
  useEffect(() => {if (open) setTyped('');}, [open]);
  const confirmed = typed === library.name;
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('server.library.removeTitle', {name: library.name})} scrollable
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button variant="danger" size="small" loading={busy} disabled={!confirmed || busy} onPress={() => remove(typed)} style={{flex: 1}}>{t('server.library.remove')}</Button></View>}>
    <PanelBlock inset={false}>
      <SettingsHelp>{t('server.library.removeGoes')}</SettingsHelp>
      <SettingsHelp>{t('server.library.removeKept')}</SettingsHelp>
      <TextField focusId="owner-libraries-remove-name" value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('next.ownerLibraries.typeToConfirm')} placeholder={t('next.ownerLibraries.typeToConfirm')} showSoftInputOnFocus/>
      {error !== undefined && <FormError placement="inline">{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</FormError>}
    </PanelBlock>
  </BottomSheet>;
}

export function RemoveFolderSheet({open, onOpenChange, source, busy, error, t, remove}: {open: boolean; onOpenChange: (open: boolean) => void; source?: InventorySource; busy: boolean; error?: unknown; t: Translate; remove: () => void}) {
  const [typed, setTyped] = useState('');
  useEffect(() => {if (open) setTyped('');}, [open]);
  const name = source?.name || source?.path || '';
  const confirmed = Boolean(name) && typed === name;
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('web.libraries.removeFolderTitle', {name})} description={t('next.ownerLibraries.removeFolderBody')}
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button variant="danger" size="small" loading={busy} disabled={!confirmed || busy} onPress={remove} style={{flex: 1}}>{t('web.libraries.removeFolder')}</Button></View>}>
    <PanelBlock inset={false}>
      <TextField focusId="owner-libraries-remove-folder" value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('next.ownerLibraries.typeToConfirm')} placeholder={t('next.ownerLibraries.typeToConfirm')} showSoftInputOnFocus/>
      {error !== undefined && <FormError placement="inline">{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</FormError>}
    </PanelBlock>
  </BottomSheet>;
}

export function RenameSheet({open, onOpenChange, library, busy, error, t, save}: {open: boolean; onOpenChange: (open: boolean) => void; library: ManagedLibrary; busy: boolean; error?: unknown; t: Translate; save: (name: string) => void}) {
  const [name, setName] = useState(library.name);
  useEffect(() => {if (open) setName(library.name);}, [open, library.name]);
  const valid = name.trim().length > 0 && name.trim() !== library.name;
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('next.ownerLibraries.renameTitle')}
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button size="small" loading={busy} disabled={!valid || busy} onPress={() => save(name.trim())} style={{flex: 1}}>{t('next.ownerLibraries.save')}</Button></View>}>
    <PanelBlock inset={false}>
      <TextField focusId="owner-libraries-rename" value={name} onChangeText={setName} maxLength={100} accessibilityLabel={t('next.ownerLibraries.nameLabel')} showSoftInputOnFocus/>
      {error !== undefined && <FormError placement="inline">{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</FormError>}
    </PanelBlock>
  </BottomSheet>;
}
