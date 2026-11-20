import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork, missingArt} from '../../../kit/artwork';
import {musicScroller, PanelBlock, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {useBook, type BookPageProps} from '../../shared/book/useBook';

const t = defaultI18n.t;

/** An audiobook's page on a phone (collections 41a): `TitleFrame` with its cover over the page wash from it, the series
 * over the title, the author as a link under it, who reads it and how long, Resume with Finished and Download, the book's
 * progress and the time left, the description with Read more, and its chapters a window at a time (the finished ones
 * checked, the one you are in marked with its progress), paging the server near the end. */
export function BookPage(props: BookPageProps) {
  const view = useBook(props), chapters = view.chapters;
  return <TitleFrame testID="native-phone-book" back={{label: t('action.back'), onPress: () => props.onBack?.(), testID: 'book-back'}}
    topAction={{icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'book-more'}}
    art={<AmbientArt treatment="page" source={view.glow}/>}
    cover={{shape: 'square', node: size => <Artwork source={view.cover} width={size} height={size} priority
      fallback={view.cover ? undefined : missingArt({title: view.title, author: view.author, shape: 'square', width: size, height: size})}/>}}
    kicker={view.kicker} title={view.title} byline={view.byline} metadata={<HeroMetadata facts={view.facts}/>}
    primary={{icon: 'i-play', label: view.primary.label, progress: view.primary.progress, remaining: view.primary.remaining, onPress: view.primary.onPress, testID: 'book-resume'}}
    actions={[{icon: 'i-check', label: t('title.markFinished'), active: view.finished.active, onPress: view.finished.onPress, testID: 'book-finished'},
      ...(view.download ? [{icon: 'i-dl' as const, label: t('action.download'), onPress: view.download, testID: 'book-download'}] : [])]}
    synopsis={view.description?.text} readMore={view.description?.readMore}
    scroller={musicScroller({onNearEnd: view.onNearEnd, testID: 'book-scroll'})}>
    {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
    {chapters && <TrackWindow heading={{title: chapters.title, trailing: chapters.count}} rows={chapters.rows} variant="chapters" onPlay={chapters.onPlay}/>}
    {view.continuation && <PanelBlock><StateRow {...view.continuation}/></PanelBlock>}
  </TitleFrame>;
}
