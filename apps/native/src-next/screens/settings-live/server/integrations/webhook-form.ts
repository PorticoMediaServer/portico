export function selectWebhookEventGroup(selected: readonly string[], group: readonly string[], enabled: boolean): readonly string[] {
  return enabled ? [...new Set([...selected, ...group])] : selected.filter(event => !group.includes(event));
}
