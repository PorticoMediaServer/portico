/** Whether Continue is available in the profile dialog: a PIN profile needs all four digits and nothing may be in flight. */
export const profileContinueEnabled = (pinRequired: boolean | undefined, pin: string, busy: boolean): boolean =>
  !busy && (!pinRequired || /^\d{4}$/.test(pin));
/** Enter does what Continue does, and only when Continue is enabled. */
export const enterContinuesProfile = (event: {key: string; isComposing?: boolean}, pinRequired: boolean | undefined, pin: string, busy: boolean): boolean =>
  event.key === 'Enter' && !event.isComposing && profileContinueEnabled(pinRequired, pin, busy);
