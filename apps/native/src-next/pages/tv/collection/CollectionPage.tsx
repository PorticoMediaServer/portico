import React, {useRef} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt} from '../../../kit/artwork';
import {Chip} from '../../../kit/controls';
import {PanelBlock, PosterWindowRow, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {Menu} from '../../../kit/overlays';
import {useCollection, type CollectionPageProps} from '../../shared/collection/useCollection';

const t = defaultI18n.t;

/** A collection's page on a TV (tv-detail frame 11): `TitleFrame` over its first film's art, with the count, years and
 * genres, the lede, Play all, Shuffle and More; then its films as one windowed row of posters under the count, how many are
 * watched and the sort. The head is the frame's short one, so the row shows whole on arrival; the frame folds the hero as
 * focus goes down to the row. */
export function CollectionPage(props: CollectionPageProps) {
  const view = useCollection(props), sortRef = useRef<View>(null);
  return <>
    <TitleFrame testID="native-collection" shortHead back={{label: t('action.back'), onPress: props.onBack, testID: 'collection-back'}}
      art={<AmbientArt treatment="title" source={view.ambient} foreground={view.backdrop}/>}
      kicker={{label: view.kicker}} title={view.title} metadata={<HeroMetadata facts={view.facts}/>} synopsis={view.lede}
      primary={{icon: 'i-play', label: t('next.cards.playAll'), onPress: view.play, testID: 'collection-play-all'}}
      actions={[{icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, onPress: view.shuffle, testID: 'collection-shuffle'},
        {icon: 'i-more', label: t('card.more', {title: view.title}), onPress: () => view.pageMenu.onOpenChange(true), testID: 'collection-menu'}]}
      note={view.note}>
      {/* The films once there are any; a failed later page keeps them and says so under the row. */}
      {view.row && <PosterWindowRow {...view.row} title={view.heading} count={view.watched}
        tools={view.sort && <Chip ref={sortRef} focusId="collection-sort" testID="collection-sort" variant="pill" trailingIcon="i-chdown" onPress={view.sort.open}>{view.sort.label}</Chip>}/>}
      {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
    </TitleFrame>
    {props.selection?.render()}
    <Menu testID={`${props.focusScope}.sort-menu`} open={view.sortMenu.open} onOpenChange={view.sortMenu.onOpenChange} title={view.sortMenu.title} items={view.sortMenu.items} returnFocusRef={sortRef}/>
    <Menu testID="collection-page-menu" open={view.pageMenu.open} onOpenChange={view.pageMenu.onOpenChange} title={view.pageMenu.title} items={view.pageMenu.items}/>
  </>;
}
