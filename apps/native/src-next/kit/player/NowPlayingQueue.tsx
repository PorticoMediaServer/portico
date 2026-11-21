import React from 'react';
import {Menu} from '../overlays';
import {ListeningQueuePaint} from './ListeningPanePaint';
import {TVQueueRows} from './TVQueueRows';
import {useTheme} from '../theme';
import {useNowPlayingQueue, type NowPlayingQueueProps} from '../../pages/shared/player/useNowPlayingQueue';

export function NowPlayingQueue(props: NowPlayingQueueProps) {
  const {paintOwner, testID = 'now-playing-queue', interactive = true, regionId, edges, artwork} = props;
  const tv = useTheme().isTV, model = useNowPlayingQueue(props, tv), {rowMenu} = model;
  if (tv) return <TVQueueRows model={model} testID={testID} interactive={interactive} regionId={regionId} edges={edges} artwork={artwork}/>;
  return <>
    {!paintOwner && <ListeningQueuePaint data={model.display} onPlay={model.paintActions.play} onMenu={model.paintActions.menu} testID={testID}/>}
    {rowMenu && <Menu key={`${rowMenu.queueId}:${rowMenu.revision}:${rowMenu.row.entryId}`} testID={`${testID}-row-menu`} open presentation="sheet"
      title={rowMenu.row.title} onOpenChange={open => {if (!open) model.retire(rowMenu);}} items={model.menuItems(rowMenu)}/>}
  </>;
}
