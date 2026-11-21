import React from 'react';
import type {ReadingCollection} from '../../../../../packages/client-core/src/reading-collection';
import {CollectionPicker} from '../selection';
import {CoverMosaic} from '../artwork';
import type {NativeShellRuntime} from '../../shell/runtime';
import {useReadingCollectionPicker} from '../../pages/shared/read/useReadingCollectionPicker';

/** The reading title's Add to collection: the kit collection picker, each collection with its square cover mosaic. */
export function ReadingCollectionPicker({controller, runtime, onClose, onView}: {controller: ReadingCollection; runtime: NativeShellRuntime; onClose: () => void; onView: () => void}) {
  const {rows, props} = useReadingCollectionPicker({controller, runtime});
  return <CollectionPicker open onOpenChange={open => {if (!open) onClose();}} {...props}
    entries={rows.map(({covers, ...row}) => ({...row, artwork: <CoverMosaic shape="square" covers={covers} width={48} height={48}/>}))}
    onViewDestination={onView} onResultDismiss={onClose} testID="reading-collection-picker"/>;
}
