export function selectWebhookEventGroup(selected: readonly string[], group: readonly string[], enabled: boolean): readonly string[] {
  return enabled ? [...new Set([...selected, ...group])] : selected.filter(event => !group.includes(event));
}
export function webhookSecretChange(secret: string): {secret: string} | {rotateSecret: true} {
  // An explicitly supplied secret is opaque; whitespace is never silently changed.
  return secret.length ? {secret} : {rotateSecret: true};
}
