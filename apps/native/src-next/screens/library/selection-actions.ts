import type {DownloadQualityChoice} from '../../../../../packages/client-core/src/downloads';
import type {SelectionActionController, SelectionActionSnapshot} from '../../../../../packages/client-core/src/selection-actions-controller';
import type {QuerySelectionTarget} from '../../../../../packages/client-core/src/selection-model';

/** Props still read by the selection kit's query surfaces. */
export type NativeSelectionActionPresentation = Readonly<{
  controller: SelectionActionController;
  target: QuerySelectionTarget<unknown>;
  current: () => boolean;
  getSnapshot: () => SelectionActionSnapshot | undefined;
  subscribe: (listener: () => void) => () => void;
  downloadQualities: () => Promise<readonly DownloadQualityChoice[]>;
  openDelete: (deleteFiles?: boolean) => Promise<void>;
  openDownload: (quality: string) => Promise<void>;
  dispose: () => void;
}>;
