/** Only facts suitable for sharing leave the owner form. Private notes, account
 * names and email are deliberately not part of this public presentation input. */
export type InvitationCardFacts = Readonly<{serverName: string; expiresAt: string; libraryNames: readonly string[]; libraryCount: number; allLibraries: boolean}>;
export function invitationCardFacts(serverName: string, invitation: Readonly<{expiresAt: string; allowedLibraries: readonly string[]}>, libraries: readonly {id: string; name: string}[]): InvitationCardFacts {
  const names = invitation.allowedLibraries.map(id => libraries.find(library => library.id === id)?.name).filter((name): name is string => name !== undefined);
  return Object.freeze({serverName, expiresAt: invitation.expiresAt, libraryNames: Object.freeze(names), libraryCount: invitation.allowedLibraries.length, allLibraries: invitation.allowedLibraries.length === 0});
}
