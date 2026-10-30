import React from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ReadingListMembership, ReadingManifest, ReadingReadMark} from '../../../../../packages/client-core/src';
import {Button, ResumeButton} from '../../kit/controls';
import {useTheme} from '../../kit/theme';

const {t} = defaultI18n;

/** Literal phone-read frame 3: EPUB Read/Resume, the original offered My List
 * membership, Download and the existing page More menu. Finished stays in that
 * real menu; the audiobook 41a action row is a different destination. Other
 * reading formats retain their existing optional read-mark row. */
export function BookDetailActions({format, saved, listed, onList, onMore, readMark, canDownload, onRead, onReadMark, onDownload, downloadRef, horizontalPadding = 20, roundSize = 46}: {
  format: ReadingManifest['format'];
  saved?: number;
  listed?: ReadingListMembership;
  onList: (membership: ReadingListMembership) => void;
  onMore?: () => void;
  readMark?: ReadingReadMark;
  canDownload: boolean;
  onRead: () => void;
  onReadMark: (mark: ReadingReadMark) => void;
  onDownload: () => void;
  downloadRef: React.RefObject<View | null>;
  horizontalPadding?: number;
  roundSize?: number;
}) {
  const theme = useTheme();
  return <View style={{paddingHorizontal: horizontalPadding, paddingTop: 18}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      {saved === undefined
        ? <Button icon="i-book" onPress={onRead} style={{flex: 1}}>{t('next.reader.read')}</Button>
        : <ResumeButton progress={saved} onPress={onRead} style={{flex: 1}}>{t('next.cards.read')}</ResumeButton>}
      {format === 'epub' && listed ? <Button variant="icon" icon={listed.listed ? 'i-check' : 'i-plus'} iconOnly
        accessibilityState={{selected: listed.listed}}
        accessibilityLabel={t(listed.listed ? 'next.home.removeMyList' : 'next.home.addMyList')}
        onPress={() => onList(listed)} style={{height: roundSize, width: roundSize}}/> : null}
      {format !== 'epub' && readMark ? <Button variant="icon" icon="i-check" iconOnly
        accessibilityState={{selected: readMark.read}}
        accessibilityLabel={t(readMark.read ? 'next.reader.markUnread' : 'next.reader.markRead')}
        iconColor={readMark.read ? theme.colors.accentStrong : undefined}
        onPress={() => onReadMark(readMark)} style={{height: roundSize, width: roundSize}}/> : null}
      <Button ref={downloadRef} variant="icon" icon="i-dl" iconOnly disabled={!canDownload}
        accessibilityLabel={t('action.download')} onPress={onDownload} style={{height: roundSize, width: roundSize}}/>
      {format === 'epub' && onMore ? <Button variant="icon" icon="i-more" iconOnly
        accessibilityLabel={t('next.controls.more')} onPress={onMore} style={{height: roundSize, width: roundSize}}/> : null}
    </View>
  </View>;
}
