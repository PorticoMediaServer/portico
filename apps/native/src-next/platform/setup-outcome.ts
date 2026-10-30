/** A verified original setup ended without granting an owner. Cancellation is
 * deliberately distinct from an authenticated Hosted denial. */
export class NativeSetupEnded extends Error {
  readonly reason: 'declined' | 'cancelled';
  constructor(reason: 'declined' | 'cancelled') {super('native_setup_ended'); this.reason = reason;}
}
