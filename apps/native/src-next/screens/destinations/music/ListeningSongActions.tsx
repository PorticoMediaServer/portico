import React from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import type {NativeListeningSong} from '../../../shell/listening-song-detail';

const t = defaultI18n.t;

/** The current song's own heart. A token is the only source of its state: while it is absent
 * nothing is drawn, and a command never assumes the answer. More waits for a guarded
 * playlist-open seam, so this surface does not borrow the TV picker's press-time opening. */
export function ListeningSongActions({song, pending, favorite, testID = 'now-playing-song'}: {
  song?: NativeListeningSong;
  pending: boolean;
  favorite: (token: NativeListeningSong, value: boolean) => void;
  testID?: string;
}) {
  const view = listeningSongFavorite(song, pending);
  return view && song ? <ListeningFavoritePaint {...view} testID={testID} onPress={() => favorite(song, !view.favorite)}/> : null;
}

/** A display-only projection: no issued token or command function enters paint. */
export function listeningSongFavorite(song: NativeListeningSong | undefined, pending: boolean): Readonly<{favorite: boolean; pending: boolean}> | undefined {
  return song?.actions.some(action => action.id === 'favorite' && action.enabled) ? {favorite: song.favorite, pending} : undefined;
}
export function ListeningFavoritePaint({favorite, pending, onPress, testID = 'now-playing-song'}: {favorite: boolean; pending: boolean; onPress?: () => void; testID?: string}) {
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginTop: 12}}>
    <Button testID={`${testID}-favorite`} variant={favorite ? 'primary' : 'glass'} iconOnly
      icon={favorite ? 'i-heart-f' : 'i-heart'} accessibilityLabel={t(favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites')}
      accessibilityState={{checked: favorite, disabled: pending}} disabled={pending} onPress={onPress}/>
  </View>;
}
