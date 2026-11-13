/** The Portico Services routes the account pages call, as the Services contract lists them
 * (`hosted-services/hosted-next/API.md` in the private repository). Kept by hand until Portico
 * Services publishes a generated contract; a route is added here only when a client calls it. */
export const servicesRoutes = {
  system: ['GET', '/v1/system'],
  signUp: ['POST', '/v1/accounts'],
  confirm: ['POST', '/v1/accounts/confirm'],
  resendConfirmation: ['POST', '/v1/accounts/confirm/resend'],
  passwordReset: ['POST', '/v1/password-reset'],
  passwordResetConfirm: ['POST', '/v1/password-reset/confirm'],
  signIn: ['POST', '/v1/sessions'],
  twoStepSignIn: ['POST', '/v1/sessions/two-step'],
  providerStart: ['POST', '/v1/sessions/provider'],
  providerComplete: ['POST', '/v1/sessions/provider/complete'],
  providerApple: ['POST', '/v1/sessions/apple'],
  refresh: ['POST', '/v1/sessions/refresh'],
  signOut: ['DELETE', '/v1/sessions/current'],
  account: ['GET', '/v1/account'],
  password: ['POST', '/v1/account/password'],
  email: ['POST', '/v1/account/email'],
  accountExport: ['POST', '/v1/account/export'],
  deleteAccount: ['POST', '/v1/account/delete'],
  sessions: ['GET', '/v1/account/sessions'],
  endSession: ['DELETE', '/v1/account/sessions/{id}'],
  endOtherSessions: ['DELETE', '/v1/account/sessions'],
  disconnectProvider: ['DELETE', '/v1/account/providers/{provider}'],
  twoStep: ['GET', '/v1/account/two-step'],
  twoStepBegin: ['POST', '/v1/account/two-step'],
  twoStepConfirm: ['POST', '/v1/account/two-step/confirm'],
  twoStepOff: ['DELETE', '/v1/account/two-step'],
  recoveryCodes: ['POST', '/v1/account/two-step/recovery-codes'],
  approval: ['GET', '/v1/approvals/{id}'],
  approvalLookup: ['POST', '/v1/approvals/lookup'],
  approvalDecision: ['POST', '/v1/approvals/{id}/decision'],
  servers: ['GET', '/v1/servers'],
  serverIdentity: ['POST', '/v1/servers/{id}/identity'],
  leaveServer: ['DELETE', '/v1/servers/{id}/membership'],
  removeServer: ['DELETE', '/v1/servers/{id}'],
  tvLookup: ['POST', '/v1/tv-codes/lookup'],
  tvDecision: ['POST', '/v1/tv-codes/{id}/decision'],
} as const;

export type ServicesRoute = keyof typeof servicesRoutes;

/** A route's method and its path with `{name}` filled in (each value encoded). */
export function servicesRoute(route: ServicesRoute, values: Readonly<Record<string, string>> = {}): {method: string; path: string} {
  const [method, template] = servicesRoutes[route];
  const path = template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = values[name];
    if (value === undefined) throw new Error(`Portico Services route ${route} needs ${name}.`);
    return encodeURIComponent(value);
  });
  return {method, path};
}
