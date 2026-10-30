import type {RemoteSource, RemoteState} from '../../../../../../packages/client-core/src/remote-sources';
import type {ChosenSourceFolder} from './SourceLibraryAssignment';

export type SourceConnectionForm = 'connection' | 'imported' | 's3' | 'drive' | 'cloud' | 'driveOAuth';
export type SourceMode = (
  {kind: 'assignment'; source: RemoteSource; folders: readonly ChosenSourceFolder[]}
  | {kind: 'folders'; source: RemoteSource; chosen: readonly ChosenSourceFolder[]; from: 'detail' | 'assignment' | 'reading'}
  | {kind: 'choose' | 'accountConfig'}
  | {kind: SourceConnectionForm; source?: RemoteSource}
  | {kind: 'detail' | 'reading' | 'disconnect' | 'advanced' | 'complete'; source: RemoteSource}
) & {wizard?: boolean; connection?: SourceConnectionForm};

export function sourceWizardStep(mode: SourceMode | undefined): number | undefined {
  if (!mode?.wizard) return;
  if (mode.kind === 'choose') return 1;
  if (mode.kind === 'reading') return 3;
  if (mode.kind === 'folders' || mode.kind === 'assignment') return 4;
  if (mode.kind === 'complete') return 5;
  return mode.kind === 'detail' || mode.kind === 'advanced' || mode.kind === 'disconnect' ? undefined : 2;
}

export function sourceBackMode(mode: SourceMode, completed = false): SourceMode | undefined {
  const wizard = mode.wizard, connection = mode.connection;
  if (mode.kind === 'folders') {
    if (mode.from === 'assignment') return {kind: 'assignment', source: mode.source, folders: mode.chosen, wizard, connection};
    return {kind: mode.from === 'reading' ? 'reading' : 'detail', source: mode.source, wizard, connection};
  }
  if (mode.kind === 'assignment') return wizard && !completed ? {kind: 'folders', source: mode.source, chosen: mode.folders, from: 'reading', wizard, connection} : {kind: 'detail', source: mode.source};
  if (mode.kind === 'reading' && wizard) return {kind: connection ?? (mode.source.kind === 'webdav' ? 'connection' : 'imported'), source: mode.source, wizard, connection};
  if (mode.kind === 'complete') return {kind: 'detail', source: mode.source};
  if (wizard && mode.kind !== 'choose') return 'source' in mode && mode.source ? {kind: 'reading', source: mode.source, wizard, connection} : {kind: 'choose', wizard: true};
  return;
}

/** A successful profile write can publish its new generation before the list refreshes. */
export function currentSourceGeneration(source: RemoteSource, state: Pick<RemoteState, 'sources' | 'receipt'>): boolean {
  const listed = state.sources.find(row => row.id === source.id);
  const receipt = state.receipt?.source.id === source.id ? state.receipt : undefined;
  const newest = Math.max(listed?.generation ?? 0, receipt?.source.generation ?? 0);
  if (receipt?.removed && receipt.source.generation >= newest) return false;
  return newest === 0 || source.generation === newest;
}
