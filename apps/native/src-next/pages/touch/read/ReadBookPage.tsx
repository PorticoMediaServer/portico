import React, {type ReactNode} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork, missingArt} from '../../../kit/artwork';
import {AboutTable, musicScroller, PanelBlock, PanelHeading, StateRow, TitleFrame, TitleSection} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {Menu} from '../../../kit/overlays';
import {useTheme} from '../../../kit/theme';
import {useReadBook, type ReadBookPageProps} from '../../shared/read/useReadBook';

const t = defaultI18n.t;

/** A book's page (phone-read 7–9 and tablet-read 2): `TitleFrame` with its cover over the page wash from it, the series and
 * its place over the title, the author with their initials, the book's facts, Read or Continue reading with its progress
 * in the button, My List (or Mark read) and Download, the version and where the reader is, the description, the first
 * lines, Contents (every chapter, the one being read marked) and Details under tabs that pin under the bar, and the rest
 * of the series. `selectionBar` is the host's bar for selecting the series' volumes. */
export function ReadBookPage({selectionBar, ...props}: ReadBookPageProps & Readonly<{selectionBar?: ReactNode}>) {
  const view = useReadBook(props), theme = useTheme(), tv = theme.isTV;
  const wide = theme.form === 'expanded' || tv;
  const contents = <TrackWindow rows={view.contents.rows} variant="chapters" opens onPlay={view.contents.onPlay}
    heading={wide ? {title: t('next.reader.contents')} : undefined}/>;
  const details = view.details.length > 0 ? <PanelBlock inset={!wide || !view.tabs}>
    {wide && <PanelHeading title={t('next.reader.details')}/>}
    <AboutTable rows={view.details}/>
  </PanelBlock> : undefined;
  const moreAction = {icon: 'i-more' as const, label: t('next.controls.more'), onPress: view.more, testID: 'read-book-more'};
  const tabs = !wide && view.tabs ? {...view.tabs, items: [{value: 'contents', label: t('next.reader.contents')},
    {value: 'details', label: t('next.reader.details')}]} : undefined;
  return <>
    <TitleFrame testID={`native-read-book-${view.id}`} back={{label: t('action.back'), onPress: props.onBack, testID: 'read-book-back'}}
      topAction={tv ? undefined : moreAction}
      art={<AmbientArt treatment="page" source={view.glow}/>}
      cover={{shape: 'poster', node: size => <Artwork source={view.cover} width={size} height={size * 1.5} priority
        // The generated cover always lies under the picture, so a cover that does not load still shows one.
        fallback={missingArt({title: view.title, author: view.author, shape: 'poster', width: size, height: size * 1.5})}/>}}
      kicker={view.kicker} title={view.title} byline={view.byline} metadata={<HeroMetadata facts={view.facts}/>}
      primary={{icon: 'i-book', label: view.primary.label, progress: view.primary.progress, disabled: view.primary.disabled, onPress: view.primary.onPress, testID: 'read-book-read'}}
      actions={tv ? [...view.actions, moreAction] : view.actions} place={view.place} synopsis={view.synopsis} readMore={view.readMore}
      quote={view.firstLines === undefined ? undefined : {label: t('next.reader.firstLines'), text: view.firstLines}} selectionBar={selectionBar}
      tabs={tabs} aside={wide && view.tabs ? details : undefined} scroller={musicScroller({testID: 'read-book-scroll'})}>
      {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
      {wide ? view.tabs ? <TitleSection tab="contents">{contents}</TitleSection> : details : view.tab === 'contents' ? contents : details}
      {view.shelf && <PosterRow {...view.shelf}/>}
      {view.shelfState && <PanelBlock><StateRow {...view.shelfState}/></PanelBlock>}
    </TitleFrame>
    <Menu presentation={tv ? 'sideSheet' : 'sheet'} open={view.menu.open} onOpenChange={view.menu.onOpenChange} title={view.title} items={view.menu.items}/>
    <Menu presentation={tv ? 'sideSheet' : 'sheet'} open={view.versions.open} onOpenChange={view.versions.onOpenChange} title={t('next.reader.version')} items={view.versions.items}/>
  </>;
}
