import React, {useRef, useState} from 'react';
import {useTheme} from '../../kit/theme';
import {ReadBookPage} from '../../pages/touch/read/ReadBookPage';
import type {ReadBookPageProps, ReadBookSheet} from '../../pages/shared/read/useReadBook';
import {ReadBookScreen} from './ReadBookScreen';
import {ReadCollectionSheet} from './ReadCollectionSheet';
import {ReadingItemMenu, ReadingSelectionToolbar} from '../../kit/reading/ReadingActions';
import {useReadingActions} from '../../pages/shared/read/useReadingActions';
import {ReadOwnerEditSheet} from './ReadOwnerEditSheet';

type HostProps = Omit<ReadBookPageProps, 'readingActions' | 'onSheet'>;

/** A phone draws a book on the rebuilt page (phone-read 7, 8, 9); a tablet and TV keep the old book screen until their
 * pages are rebuilt. */
export function ReadBookHost(props: HostProps) {
  return useTheme().platform === 'phone' ? <PhoneReadBook {...props}/> : <ReadBookScreen {...props}/>;
}

/** The page, with what the reading screens own drawn over it: the series' volumes' menu and selection bar, and the
 * owner's Edit details and Add to collection sheets. A sheet keeps the ticket it was opened with, so a sheet that closes
 * late never closes a newer one. */
function PhoneReadBook(props: HostProps) {
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
