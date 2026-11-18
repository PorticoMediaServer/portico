import type {SelectionActionController, SelectionActionSnapshot} from '../../../../../packages/client-core/src/selection-actions-controller';
import type {QuerySelectionTarget} from '../../../../../packages/client-core/src/selection-model';

/** Props for the query Delete dialog still imported by the selection kit. */
export type NativeSelectionActionPresentation = Readonly<{
  controller: SelectionActionController;
  target: QuerySelectionTarget<unknown>;
  current: () => boolean;
  getSnapshot: () => SelectionActionSnapshot | undefined;
  subscribe: (listener: () => void) => () => void;
  openDelete: (deleteFiles?: boolean) => Promise<void>;
}>;
