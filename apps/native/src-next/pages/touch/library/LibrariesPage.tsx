import React from 'react';
import {BrowseFrame} from '../../../kit/frames';
import {LibraryTiles} from '../../../kit/browse/LibraryTiles';
import {Button} from '../../../kit/controls';
import {useLibraryOverview, type LibraryOverviewProps} from '../../shared/library/useLibrary';
import {useBrowseScroll} from '../../../kit/frames/useBrowseScroll';

export function LibrariesPage(props: LibraryOverviewProps) {
  const view = useLibraryOverview(props), {t} = view;
  const scroll = useBrowseScroll(props.runtime, {kind: 'libraries'});
  return <BrowseFrame testID="native-libraries-overview" scroll={scroll}
    header={{title: t('pins.libraries'), trailing: <Button testID="libraries-edit-pins" variant="text" onPress={props.editPins}>{t('pins.edit')}</Button>}}
    state={view.failure ?? (view.loading ? {kind: 'loading'} : undefined)}>
    <LibraryTiles libraries={view.libraries} note={t('next.browse.native.libraryOrderHint')}/>
  </BrowseFrame>;
}
