import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {ProfileFace} from '../artwork';
import type {ArtworkSource} from '../artwork/types';
import type {ProfileArt} from '../../../../../packages/client-core/src/profile-management';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

type Face = {name: string; art: ProfileArt; locked: boolean; picture: (size: number) => ArtworkSource | undefined};

export function MemberProfileRow({name, art, locked, picture, facts, children, testID}: Face & {
  facts: readonly string[]; children?: ReactNode; testID?: string;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), size = theme.targets.touch;
  return <View testID={testID} style={{flexDirection: 'row', alignItems: 'center', padding: frame.cardPadding,
    gap: theme.space.controlGap, borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
    <ProfileFace initial={name.slice(0, 1).toUpperCase()} color={art} locked={locked} size={size} source={picture(size)}/>
    <View style={{flex: 1, minWidth: 0, gap: theme.space.xs}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{name}</Text>
      {facts.length > 0 && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{facts.join(' · ')}</Text>}
    </View>
    {children}
  </View>;
}

export function MemberProfilePortrait({name, art, locked, picture}: Face) {
  const frame = useFrameMetrics(), size = frame.rowTwoLine;
  return <View style={{alignItems: 'center', paddingVertical: frame.cardPadding}}>
    <ProfileFace initial={name.slice(0, 1).toUpperCase()} color={art} locked={locked} size={size} source={picture(size)}/>
  </View>;
}
