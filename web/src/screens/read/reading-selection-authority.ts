/** Reading's Select is personal state for every signed-in viewer (phone-read 2,
 * desktop-read 2b); only the owner's metadata editing is role-bound. Creating a
 * personal collection is the same authority the Home selection picker already
 * offers members. */
export function readingSelectionAuthority(viewer: Readonly<{authority: string; role: string}> | undefined) {
  const personal = viewer?.authority === 'local';
  return Object.freeze({select: personal, create: personal, edit: personal && viewer?.role === 'owner'});
}
