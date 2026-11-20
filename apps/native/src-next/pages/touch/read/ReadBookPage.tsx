import React, {type ReactNode} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork, missingArt} from '../../../kit/artwork';
import {AboutTable, musicScroller, PanelBlock, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {Menu} from '../../../kit/overlays';
import {useReadBook, type ReadBookPageProps} from '../../shared/read/useReadBook';

const t = defaultI18n.t;

/** A book's page on a phone (phone-read 7, 8, 9): `TitleFrame` with its cover over the page wash from it, the series and
 * its place over the title, the author with their initials, the book's facts, Read or Continue reading with its progress
 * in the button, My List (or Mark read) and Download, the version and where the reader is, the description, the first
 * lines, Contents (every chapter, the one being read marked) and Details under tabs that pin under the bar, and the rest
 * of the series. `selectionBar` is the host's bar for selecting the series' volumes. */
export function ReadBookPage({selectionBar, ...props}: ReadBookPageProps & Readonly<{selectionBar?: ReactNode}>) {
  const view = useReadBook(props);
  return <>
    <TitleFrame testID={`native-read-book-${view.id}`} back={{label: t('action.back'), onPress: props.onBack, testID: 'read-book-back'}}
      topAction={{icon: 'i-more', label: t('next.controls.more'), onPress: view.more, testID: 'read-book-more'}}
      art={<AmbientArt treatment="page" source={view.glow}/>}
      cover={{shape: 'poster', node: size => <Artwork source={view.cover} width={size} height={size * 1.5} priority
        // The generated cover always lies under the picture, so a cover that does not load still shows one.
        fallback={missingArt({title: view.title, author: view.author, shape: 'poster', width: size, height: size * 1.5})}/>}}
      kicker={view.kicker} title={view.title} byline={view.byline} metadata={<HeroMetadata facts={view.facts}/>}
      primary={{icon: 'i-book', label: view.primary.label, progress: view.primary.progress, disabled: view.primary.disabled, onPress: view.primary.onPress, testID: 'read-book-read'}}
      actions={view.actions} place={view.place} synopsis={view.synopsis} readMore={view.readMore}
      quote={view.firstLines === undefined ? undefined : {label: t('next.reader.firstLines'), text: view.firstLines}} selectionBar={selectionBar}
      tabs={view.tabs && {...view.tabs, items: [{value: 'contents', label: t('next.reader.contents')}, {value: 'details', label: t('next.reader.details')}]}}
      scroller={musicScroller({testID: 'read-book-scroll'})}>
      {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
      {/* A phone draws the chosen tab's panel only. */}
      {view.tab === 'contents' ? <TrackWindow rows={view.contents.rows} variant="chapters" opens onPlay={view.contents.onPlay}/>
        : view.details.length > 0 && <PanelBlock><AboutTable rows={view.details}/></PanelBlock>}
      {view.shelf && <PosterRow {...view.shelf}/>}
      {view.shelfState && <PanelBlock><StateRow {...view.shelfState}/></PanelBlock>}
    </TitleFrame>
    <Menu open={view.menu.open} onOpenChange={view.menu.onOpenChange} title={view.title} items={view.menu.items}/>
    <Menu open={view.versions.open} onOpenChange={view.versions.onOpenChange} title={t('next.reader.version')} items={view.versions.items}/>
  </>;
}
