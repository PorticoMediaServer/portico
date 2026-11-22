/** The side menu's pending return to Profile and server (TVRail): the profile menu was opened from that item, so focus
 * coming back to the side menu (the menu closed, or a page it opened went Back) is a return, not an entry (tv-focus
 * "Choices made"). Kept outside the side menu, which a full-screen page (Switch server) unmounts, and bound to the
 * destination, server and profile it was made on. */
export const profileReturn = {pending: false, destination: '', scope: '', chose: false};

/** The profile menu acted: what it opens (a page or a dialog) takes focus from the side menu, and its Back is still the
 * return, even though focus may pass through the side menu on the way (#5060). */
export function profileMenuChose(): void {profileReturn.chose = true;}
