import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {LibraryTabId} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {LibrarySwitcher} from '../../../kit/browse';
import {BrowseFrame} from '../../../kit/frames';
import {Button} from '../../../kit/controls';
import {Menu} from '../../../kit/overlays';
import {ReadingLibraryBody, ReadingLibraryLoading, ReadingDiscovery, ReadingFallbackGrid} from '../../../kit/reading/ReadingLibraryParts';
import {NativeReadingBooks} from '../../../screens/read/ReadingBooks';
import {ReadingItemMenu, ReadingSelectionToolbar} from '../../../screens/read/ReadingActions';
import {useBooksLibrary, type BooksPageProps} from '../../shared/reading/useBooksLibrary';

const t = defaultI18n.t;

/** The server's reading pivots and Discover shelves share one library header on touch. */
export function BooksPage(props: BooksPageProps) {
  const {runtime, libraryId, onBack} = props, model = useBooksLibrary(props);
  const {name, tab, tabs, selectTab, readingLibraries, openLibrary, counts, downloadMenu, grid, readingActions} = model;
  const body = model.waiting ? <ReadingLibraryLoading metrics={model.metrics}/>
    : tab === 'discover' ? <ReadingDiscovery model={model} runtime={runtime}/>
    : grid ? <NativeReadingBooks {...grid} key={grid.key}/> : <ReadingFallbackGrid model={model} runtime={runtime}/>;
  return <ReadingLibraryBody id={libraryId} onWidth={model.reportWidth}>
    <BrowseFrame testID="phone-reading-library" header={{title: name, counts: counts.map((text, index) => ({id: String(index), text, role: 'context'})),
      back: {label: t('next.browse.native.libraries'), onPress: onBack},
      menu: model.metrics.tablet ? <Button variant="text" icon="i-left" onPress={onBack}>{t('next.browse.native.libraries')}</Button> : undefined,
      tabs: {items: tabs.map(value => ({value: value.id, label: t(value.label)})), value: tab, onValueChange: id => selectTab(id as LibraryTabId)},
      switcher: <LibrarySwitcher libraries={readingLibraries} value={libraryId} onValueChange={openLibrary}/>}}
      toolbar={model.showSelection ? <ReadingSelectionToolbar port={readingActions} runtime={runtime}/> : undefined} grid={body}/>
    <ReadingItemMenu port={readingActions} runtime={runtime}/>
    <Menu open={model.downloadOpen} onOpenChange={open => {if (!open) model.dismissDownload();}} title={downloadMenu?.item.title}
      items={[{id: 'download', label: t('action.download'), onSelect: model.startDownload}]}/>
  </ReadingLibraryBody>;
}
