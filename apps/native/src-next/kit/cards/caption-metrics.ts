/** One line of a card's title: a phone poster's title takes two of them before it is cut (the owner's cut-text rule,
 * W3 F1/F6/F20), so every screen that sizes a poster card adds this line under its art. */
export function cardTitleLine(t: {type?: {cardTitle?: {fontSize?: number}}}): number {return Math.ceil((t.type?.cardTitle?.fontSize ?? 13) * 1.35);}

/** The caption a poster or square card draws under its art: two title lines and its second line (the year, an artist).
 * A row or grid of cards reserves it under the art, so no caption is clipped by the row's height (moved from screens/library). */
export function cardCaptionHeight(t: {spec: {card: {titleMargin: Readonly<Record<string, number>>; subtitleMargin: Readonly<Record<string, number>>}}; platform: string; textScale: number; type: {cardTitle: {lineHeight?: number}; caption: {lineHeight?: number}}}): number {
  return t.spec.card.titleMargin[t.platform]! + 2 * (t.type.cardTitle.lineHeight ?? 28 * t.textScale) + t.spec.card.subtitleMargin[t.platform]! + (t.type.caption.lineHeight ?? 24 * t.textScale);
}
