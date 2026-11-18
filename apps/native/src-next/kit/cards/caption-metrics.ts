/** One line of a card's title: a phone poster's title takes two of them before it is cut (the owner's cut-text rule,
 * W3 F1/F6/F20), so every screen that sizes a poster card adds this line under its art. */
export function cardTitleLine(t: {type?: {cardTitle?: {fontSize?: number}}}): number {return Math.ceil((t.type?.cardTitle?.fontSize ?? 13) * 1.35);}

/** R5: the caption under a card's art is a reserved slot (title, then year or subtitle), so every card in a row is the
 * same height whether or not it has a second line, and a row's height includes its captions. Its height for this form,
 * from `formScale.card.caption`; `titleLines` is 2 where a phone poster's title may wrap. A row or grid adds this to
 * the art's height: it never types a caption height of its own. */
type CaptionScale = {scale: {card: {caption: {gap: number; titleLine: number; subtitleGap: number; subtitleLine: number}}}; fontScale?: number};
export function cardCaptionHeight(t: CaptionScale, titleLines = 1): number {
  const caption = t.scale.card.caption, text = Math.max(1, t.fontScale ?? 1);
  return caption.gap + Math.ceil((caption.titleLine * titleLines + caption.subtitleLine) * text) + caption.subtitleGap;
}
