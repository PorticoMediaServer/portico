import React, {useRef, useState} from 'react';
import {ReadBookPage} from './ReadBookPage';
import type {ReadBookPageProps, ReadBookSheet} from '../../shared/read/useReadBook';
import {ReadCollectionSheet} from '../../../kit/reading/ReadCollectionSheet';
import {ReadingItemMenu, ReadingSelectionToolbar} from '../../../kit/reading/ReadingActions';
import {useReadingActions} from '../../shared/read/useReadingActions';
import {ReadOwnerEditSheet} from '../../../kit/reading/ReadOwnerEditSheet';

type HostProps = Omit<ReadBookPageProps, 'readingActions' | 'onSheet'>;

/** The page, with what the reading screens own drawn over it: the series' volumes' menu and selection bar, and the
 * owner's Edit details and Add to collection sheets. A sheet keeps the ticket it was opened with, so a sheet that closes
 * late never closes a newer one. */
export function ReadBookHost(props: HostProps) {
  const {controller, context, route, runtime} = props;
  const readingActions = useReadingActions(context, runtime, controller);
  const [sheet, setSheet] = useState<ReadBookSheet & Readonly<{owner: object; key: number}>>(), seq = useRef(0);
  const close = (ticket: object) => setSheet(current => current?.owner === ticket ? undefined : current);
  return <>
    <ReadBookPage {...props} readingActions={readingActions} onSheet={value => setSheet({...value, owner: {}, key: ++seq.current})}
      selectionBar={<ReadingSelectionToolbar port={readingActions} runtime={runtime}/>}/>
    <ReadingItemMenu port={readingActions} runtime={runtime}/>
    {sheet?.kind === 'edit' ? <ReadOwnerEditSheet key={sheet.key} context={context} lease={sheet.lease} owner={sheet.owner} itemId={controller.item.id} libraryId={route.libraryId}
      manifest={sheet.manifest} onClose={close}/> : null}
    {sheet?.kind === 'collection' ? <ReadCollectionSheet key={sheet.key} context={context} lease={sheet.lease} owner={sheet.owner} itemId={controller.item.id} libraryId={route.libraryId}
      kind={sheet.collection} title={sheet.title} onClose={close}/> : null}
  </>;
}
