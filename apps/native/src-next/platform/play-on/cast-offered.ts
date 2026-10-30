/**
 * Whether Cast is offered: this build can cast, and the server names a receiver application
 * (its `cast.applicationId` setting; configuration, never a default). A build knows the one
 * application it was made for; when the server names another, this build cannot start it, so
 * nothing is offered. A build that cannot say which application it has (an older binary) trusts
 * the server's setting alone. No React Native imports: tested under Node.
 */
export function castOffered(buildCanCast: boolean, serverApplication: unknown, buildApplication: unknown): boolean {
  if (!buildCanCast) return false;
  const configured = typeof serverApplication === 'string' ? serverApplication.trim() : '';
  if (!configured) return false;
  const built = typeof buildApplication === 'string' ? buildApplication.trim() : '';
  return !built || built.toUpperCase() === configured.toUpperCase();
}
