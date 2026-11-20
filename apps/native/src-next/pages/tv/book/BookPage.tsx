import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork, missingArt} from '../../../kit/artwork';
import {StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {TrackWindow} from '../../../kit/music/TrackWindow';
import {useBook, type BookPageProps} from '../../shared/book/useBook';

const t = defaultI18n.t;

/** An audiobook's page on a TV (collections frame 43): `TitleFrame` with its cover at the left, the series as a link over the
 * title, the author as a link under it, who reads it, how long it is and how many chapters, the book's progress and the time
 * left, Resume chapter N, Start over (once begun) and ⋯; its chapters beside the cover under the words (the finished ones checked, the one
 * you are in marked with its progress), the next page of them read when focus reaches the last. Focus starts on Resume. */
export function BookPage(props: BookPageProps) {
  const view = useBook(props), chapters = view.chapters, last = chapters?.rows.at(-1)?.id;
  return <TitleFrame testID="native-tv-book" back={{label: t('action.back'), onPress: () => props.onBack?.(), testID: 'book-back'}}
    art={<AmbientArt treatment="cover" source={view.glow}/>}
    cover={{shape: 'square', node: size => <Artwork source={view.cover} width={size} height={size} priority
      fallback={view.cover ? undefined : missingArt({title: view.title, author: view.author, shape: 'square', width: size, height: size})}/>}}
    kicker={view.kicker} title={view.title} byline={view.byline} metadata={<HeroMetadata facts={view.facts}/>}
    primary={{icon: 'i-play', label: view.primary.label, progress: view.primary.progress, remaining: view.primary.remaining, onPress: view.primary.onPress, testID: 'book-resume'}}
    // Start over only once there is a place to start over from (a book in progress); an unstarted book has Play alone.
    actions={[...(view.primary.progress !== undefined ? [{icon: 'i-restart' as const, label: t('action.startOver'), labelled: true, onPress: view.startOver, testID: 'book-start-over'}] : []),
      {icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'book-menu'}]}
    beside={<>
      {view.state && <StateRow {...view.state}/>}
      {chapters && <TrackWindow inset={false} heading={{title: chapters.title, count: chapters.count}} rows={chapters.rows} variant="chapters" onPlay={chapters.onPlay}
        onFocusRow={id => {if (id === last) view.onNearEnd();}}/>}
      {view.continuation && <StateRow {...view.continuation}/>}
    </>}/>;
}
