/** One line of a card's title: a phone poster's title takes two of them before it is cut (the owner's cut-text rule,
 * W3 F1/F6/F20), so every screen that sizes a poster card adds this line under its art. */
export function cardTitleLine(t: {type?: {cardTitle?: {fontSize?: number}}}): number {return Math.ceil((t.type?.cardTitle?.fontSize ?? 13) * 1.35);}
