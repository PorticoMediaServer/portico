import {defaultI18n} from '@i18n';
import {LibraryManagementService} from '@core/library-management.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {Button} from '../../../kit/controls';
import {router} from '../../../router';
import type {SettingsContext} from '../Settings';
import type {SettingsController} from '../controller';
import {LibrariesPanel} from '../libraries/LibrariesPanel';
import {MetadataSource} from '../libraries/MetadataSource';
import {LibraryHeader} from '../libraries/LibraryHeader';
import {RenameLibrary} from '../libraries/RenameLibrary';
import {LibraryTrash, LibraryTrashRule} from '../libraries/LibraryTrash';
import {LibraryScanAction} from '../libraries/LibraryActions';
import {LibraryLyricsAction} from '../libraries/LibraryLyricsAction';
import {RemoveLibraryButton} from '../libraries/RemoveLibrary';
import {LibraryFolders} from '../libraries/LibraryFolders';
import {SkippedFiles} from '../libraries/SkippedFiles';
import {isLibraryPanelContext, libraryScanAction} from '../libraries/model';
import {activeServerSection, type ServerSectionGateInput} from './access-gate';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';

const ownerLibraryPanelRows = new Set(['libraries.fetchLyrics', 'libraries.remove', 'libraries.trash', 'libraries.trashRule']);

/** Server › Libraries: bounded library list, per-library panel header/intro, folders, scans, and removal. */
export function createServerLibrariesSection(
  env: SettingsRouteEnv,
  gate: ServerSectionGateInput,
): SettingsSectionSurface {
  const {api, context, buildContext, route, role, viewer, viewerId, current, reportActionError} = env;
  const libraryScope = Object.freeze({serverId: viewer.serverId, viewerId});
  // The owner's Libraries page owns its own bounded directory read and scan commands.
  const libraries = activeServerSection(route, 'libraries', gate)
    ? new LibraryManagementService({api, scope: libraryScope, queries: context.freshness.queries}) : undefined;

  const isLibraryPanelRow = (rowId: string) =>
    rowId === 'libraries.folders'
    || (rowId === 'libraries.skipped' || rowId === 'libraries.unmatched') && !!libraries
    || ownerLibraryPanelRows.has(rowId) && !!libraries && role === 'owner';

  const lyricsSourceIsLrclib = (controller: SettingsController, selected: SettingsContext) => {
    const row = settingsRowIndex.get('libraries.lyrics')?.row;
    return !!row && controller.rowState(row, selected).value === 'lrclib';
  };

  return {
    panelIntro: selected => selected.recordId && selected.tabId === 'metadata' && libraries
      ? <MetadataSource key={selected.recordId} libraryId={selected.recordId} api={api} isCurrent={current} /> : undefined,
    panelHeader: (selected, title, controller) => selected.recordId && libraries
      ? <LibraryHeader key={selected.recordId} libraryId={selected.recordId} name={title}
        rename={name => controller.renameLibrary(selected.recordId!, name)} service={libraries} artwork={context.artwork}
        onError={reportActionError} lyrics={{api, available: lyricsSourceIsLrclib(controller, selected), isCurrent: current}} />
      : undefined,
    eligible: (row, selected, controller) => {
      if (row.id === 'libraries.list') return !!libraries && current() && controller.sectionVisible(selected.section);
      if (isLibraryPanelRow(row.id)) return current() && isLibraryPanelContext(selected) && controller.sectionVisible(selected.section);
      if (libraryScanAction(row.id)) return current() && !!libraries && selected.panelId === 'library' && !!selected.recordId && controller.sectionVisible(selected.section);
      return undefined;
    },
    rowState: (row, selected, controller) => {
      if (row.id === 'libraries.list') return {present: !!libraries && current() && controller.sectionVisible(selected.section)};
      if (isLibraryPanelRow(row.id)) return {present: current() && isLibraryPanelContext(selected) && controller.sectionVisible(selected.section)};
      if (libraryScanAction(row.id)) return {present: current() && !!libraries && selected.panelId === 'library' && !!selected.recordId && controller.sectionVisible(selected.section)};
      return undefined;
    },
    special: (row, state, rowContext, openPanel, controller) => {
      if (row.id === 'libraries.name' && isLibraryPanelContext(rowContext) && rowContext.recordId && typeof state.value === 'string') {
        const libraryId = rowContext.recordId;
        return <RenameLibrary key={libraryId} name={state.value} disabled={state.disabled === true || state.saving === true} rename={name => controller.renameLibrary(libraryId, name)} />;
      }
      if (row.id === 'libraries.list' && libraries) {
        return <LibrariesPanel api={api} service={libraries} serverName={buildContext.facts.identity.serverName} artwork={context.artwork}
          onEdit={libraryId => {if (current()) openPanel('library', libraryId);}} onError={reportActionError} />;
      }
      if (row.id === 'libraries.skipped' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries) {
        return <SkippedFiles libraryId={rowContext.recordId} api={api} service={libraries} isCurrent={current} label={defaultI18n.t(row.control.label ?? row.label)} />;
      }
      if (row.id === 'libraries.trashRule' && isLibraryPanelContext(rowContext) && rowContext.recordId) {
        return <LibraryTrashRule key={rowContext.recordId} libraryId={rowContext.recordId} api={api} isCurrent={current} label={defaultI18n.t(row.label)} />;
      }
      if (row.id === 'libraries.trash' && isLibraryPanelContext(rowContext) && rowContext.recordId) {
        return <LibraryTrash key={rowContext.recordId} libraryId={rowContext.recordId} api={api} isCurrent={current} label={defaultI18n.t(row.label)} />;
      }
      if (row.id === 'libraries.folders' && isLibraryPanelContext(rowContext) && rowContext.recordId) {
        return <LibraryFolders key={rowContext.recordId} libraryId={rowContext.recordId} api={api} service={controller.folders} snapshot={controller.folders.getSnapshot()}
          serverName={buildContext.facts.identity.serverName} isCurrent={current} onError={reportActionError} />;
      }
      if (row.id === 'libraries.remove' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries && role === 'owner') {
        return <RemoveLibraryButton key={rowContext.recordId} libraryId={rowContext.recordId} service={libraries} api={api}
          label={defaultI18n.t(row.control.label ?? row.label)}
          onRemoved={() => {if (current()) {openPanel(''); void libraries.loadLibraries().catch(reportActionError);}}} onError={reportActionError} />;
      }
      if (row.id === 'libraries.unmatched' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries) {
        const libraryId = rowContext.recordId;
        return <Button variant="glass" size="small" disabled={state.disabled === true} onClick={() => {
          if (current()) void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {unmatched: true}}).catch(reportActionError);
        }}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
      }
      if (row.id === 'libraries.fetchLyrics' && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries && role === 'owner') {
        return <LibraryLyricsAction key={rowContext.recordId} api={api} libraryId={rowContext.recordId} isCurrent={current}
          disabled={state.disabled === true || state.saving === true || controller.rowState(settingsRowIndex.get('libraries.lyrics')!.row, rowContext).value !== 'lrclib'}
          label={defaultI18n.t(row.control.label ?? row.label)} onError={reportActionError} />;
      }
      if (row.control.type === 'action' && libraryScanAction(row.id) && isLibraryPanelContext(rowContext) && rowContext.recordId && libraries) {
        return <LibraryScanAction rowId={row.id} label={defaultI18n.t(row.control.label ?? row.label)} libraryId={rowContext.recordId} service={libraries} onError={reportActionError} />;
      }
      return undefined;
    },
    dispose: () => {libraries?.dispose();},
  };
}
