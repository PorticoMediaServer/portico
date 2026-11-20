import {homeRowsSpec} from '@portico/design';
import type {HomeRow} from '../../../../../packages/client-core/src/home';
import {wideDetailHeight} from '../cards';
import {cardTitleLine} from '../cards/caption-metrics';
import type {Theme} from '../theme';
import {booksCardSize} from './HomeRows';

export type HomeRowMetrics = Readonly<{
  /** One card's drawn box and the caption under its art. */
  itemWidth: number; itemHeight: number; captionHeight: number;
  /** The row's height before the frame measures it: heading, cards and the shelf's padding. */
  estimate: number;
  /** The frame's gap above each row (phone-app.html `.phone .row{margin-top:28px}`). */
  gap: number;
}>;

/**
 * A Home row's sizes on a phone (phone-app.html 1, 2): Continue your books' resume card, a ranked card (its poster beside
 * the number), a wide, square or poster card, a live card, or a collection banner. `cardScale` is the viewer's confirmed
 * Appearance card size (1 is the drawn size).
 */
export function homeRowMetrics(theme: Theme, row: Pick<HomeRow, 'id' | 'artworkShape' | 'live' | 'collection'>, {ranked, cardScale}: Readonly<{ranked: boolean; cardScale: number}>): HomeRowMetrics {
  const spec = homeRowsSpec, wide = row.artworkShape === 'landscape', square = row.artworkShape === 'square', books = row.id === 'continue_books';
  const rankedSpec = spec.ranked[theme.isTV ? 'tv' : 'phone'];
  const medium = theme.form === 'medium', mediumSpec = spec.medium.uprightTablet;
  const drawn = books ? booksCardSize(theme).width : ranked ? rankedSpec.width
    : wide ? medium ? mediumSpec.wide : theme.spec.card.width.wide[theme.column]
    : square ? theme.spec.card.width.square[theme.column] : medium ? mediumSpec.poster : theme.spec.card.width.poster[theme.column];
  const itemWidth = cardScale === 1 ? drawn : Math.round(drawn * cardScale);
  const posterWidth = ranked ? Math.round(rankedSpec.posterWidth * cardScale) : itemWidth;
  const form = theme.isTV ? 'tv' : 'touch';
  const captionHeight = books ? 0 : wide ? wideDetailHeight(theme) : square ? spec.caption.square[form]
    : spec.caption.poster[form] + (theme.isTV ? 0 : cardTitleLine(theme));
  const itemHeight = books ? Math.round(booksCardSize(theme).height * cardScale) : wide ? itemWidth / theme.spec.card.aspect.wide + captionHeight
    : square ? itemWidth + captionHeight : posterWidth / theme.spec.card.aspect.poster + captionHeight;
  const cardHeight = row.live ? theme.isTV ? theme.spec.hero.tvHome.channelHeight : theme.spec.card.live.height.phone : itemHeight;
  const heading = theme.type.rowHeading.lineHeight ?? Math.ceil((theme.type.rowHeading.fontSize ?? 0) * 1.4);
  const estimate = row.collection ? spec.collection[form]
    : heading + theme.spec.shelf.headingMargin[theme.column] + cardHeight + theme.spec.shelf.phonePaddingTop + theme.spec.shelf.phonePaddingBottom;
  return {itemWidth, itemHeight, captionHeight, estimate, gap: spec.phone.rowGap};
}
