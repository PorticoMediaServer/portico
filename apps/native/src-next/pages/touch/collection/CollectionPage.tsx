import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt} from '../../../kit/artwork';
import {Chip} from '../../../kit/controls';
import {PanelBlock, PanelHeading, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {Menu} from '../../../kit/overlays';
import type {NativeCollectionScreenProps} from '../../../screens/collections/CollectionScreen';
import {PlayOnButton} from '../../../shell/PlayOnButton';
import {useCollection} from '../../shared/collection/useCollection';

const t = defaultI18n.t;

/** A collection's page on a phone and a tablet (phone-detail d3 frame 8): `TitleFrame` with Play all, Shuffle and ⋯, the
 * note when Portico made it, the count with the sort, and its films windowed: rows on a phone, posters on a tablet. */
export function CollectionPage(props: NativeCollectionScreenProps) {
  const view = useCollection(props);
  return <>
    <TitleFrame testID="native-collection" back={{label: t('action.back'), onPress: props.onBack, testID: 'collection-back'}}
      topControl={props.onPlayOn ? <PlayOnButton playOn={props.playOn} variant="round" testID="collection-play-on" onPress={anchor => props.onPlayOn?.(anchor)}/> : undefined}
      art={<AmbientArt treatment="title" source={view.ambient} foreground={view.backdrop}/>}
      kicker={{label: view.kicker}} title={view.title} metadata={<HeroMetadata facts={view.facts}/>}
      primary={{icon: 'i-play', label: t('next.cards.playAll'), onPress: view.play, testID: 'collection-play-all'}}
      actions={[{icon: 'i-shuffle', label: t('title.shuffle'), onPress: view.shuffle, testID: 'collection-shuffle'},
        {icon: 'i-more', label: t('card.more', {title: view.title}), onPress: () => view.pageMenu.onOpenChange(true), testID: 'collection-menu'}]}
      note={view.note} scroller={view.scroller}>
      <PanelBlock>
        <PanelHeading quiet title={view.heading} trailing={(view.sort || view.select) && <>
          {view.select && <Chip icon="i-check" onPress={view.select.onPress}>{view.select.label}</Chip>}
          {view.sort && <Chip testID="collection-sort" variant="pill" trailingIcon="i-chdown" onPress={view.sort.open}>{view.sort.label}</Chip>}</>}/>
        {view.state && <StateRow {...view.state}/>}
      </PanelBlock>
    </TitleFrame>
    {props.selection?.render()}
    <Menu testID={`${props.focusScope}.sort-menu`} open={view.sortMenu.open} onOpenChange={view.sortMenu.onOpenChange} title={view.sortMenu.title} items={view.sortMenu.items}/>
    <Menu testID="collection-page-menu" open={view.pageMenu.open} onOpenChange={view.pageMenu.onOpenChange} title={view.pageMenu.title} items={view.pageMenu.items}/>
  </>;
}
