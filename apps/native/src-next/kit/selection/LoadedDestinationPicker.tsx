import React from 'react';
import {View} from 'react-native';
import {CollectionPicker, PlaylistPicker} from './Picker';
import type {PickerEntry} from './types';
import {useTheme} from '../theme';
import type {NativeLoadedDestinations} from '../../screens/search/loaded-destinations';
import {useLoadedDestinationPicker} from '../../pages/shared/selection/useLoadedDestinationPicker';

export type NativeDestinationPickerProps = Readonly<{
  controller: NativeLoadedDestinations;
  kind: 'playlist' | 'collection';
  open: boolean;
  selectionLabel: string;
  count: number;
  artwork: (path?: string) => React.ReactNode;
  onOpenChange: (open: boolean) => void;
}>;

/** The drawn playlist/collection picker (select.html frames 20–22) for a page's selection, a title's Add to collection
 * and Search: each destination row with its own 2 by 2 cover mosaic when the server published more than one cover. */
export function NativeDestinationPicker({artwork, ...input}: NativeDestinationPickerProps) {
  const theme = useTheme(), {rows, props} = useLoadedDestinationPicker(input);
  // The drawn 2 by 2 cells are the phone/tablet picker's 48 px frame; TV keeps its own 76 px single artwork until its
  // cell geometry has its own proof. No artwork at all (a caller that draws none) keeps today's empty cell.
  const mosaic = (paths: readonly string[]) => {
    const sources = paths.slice(0, 4), first = artwork(sources[0]);
    if (sources.length < 2 || theme.isTV || first == null) return first;
    return <View style={{flex: 1, flexDirection: 'row', flexWrap: 'wrap'}}>
      {Array.from({length: 4}, (_, index) => <View key={index} style={{width: '50%', height: '50%', alignItems: 'center', justifyContent: 'center', overflow: 'hidden'}}>
        {sources[index] ? <View style={{transform: [{scale: 0.5}]}}>{artwork(sources[index])}</View> : null}
      </View>)}
    </View>;
  };
  const entries: PickerEntry[] = rows.map(({paths, ...row}) => ({...row, artwork: mosaic(paths)}));
  return input.kind === 'playlist' ? <PlaylistPicker {...props} entries={entries}/> : <CollectionPicker {...props} entries={entries}/>;
}
