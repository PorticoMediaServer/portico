import type {AppServerSession} from '../../../../packages/client-core/src/app-sessions';

/** Settings › Switch server's rows: one per server saved on this device. What tells two same-named servers apart
 * is where this device reaches them (host and port of the stored route); a sign-in past its end on this device's
 * clock offers only Forget (rulings, 13 Nov). */
export function savedServerRows<Label>(servers: readonly AppServerSession[], nameOf: (serverId: string) => string, labelOf: (signIn: AppServerSession['signIn']) => Label, now: number) {
  const count = new Map<string, number>();
  for (const value of servers) {const name = nameOf(value.connection.pin.serverId); count.set(name, (count.get(name) ?? 0) + 1);}
  return Object.freeze(servers.map(value => {
    const name = nameOf(value.connection.pin.serverId);
    return Object.freeze({
      id: value.connection.pin.serverId, name, signIn: value.signIn,
      familyId: value.connection.session.sessionFamilyId, fingerprint: value.connection.pin.fingerprint,
      labelKey: labelOf(value.signIn),
      address: hostOf(value.connection.routes.lastGood ?? value.connection.logicalOrigin),
      sharesName: (count.get(name) ?? 0) > 1,
      signedIn: now < Date.parse(value.connection.session.authorizationHorizon),
    });
  }));
}

function hostOf(origin: string): string {try {return new URL(origin).host;} catch {return origin;}}
