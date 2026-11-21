import React from 'react';
import {Menu} from '../../../kit/overlays';
import {ListeningQueuePaint} from '../../../kit/player/ListeningPanePaint';
import {TVQueueRows} from '../../../kit/player/TVQueueRows';
import {useTheme} from '../../../kit/theme';
import {useNowPlayingQueue, type NowPlayingQueueProps} from './useNowPlayingQueue';

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
