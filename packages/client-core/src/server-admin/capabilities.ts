/**
 * Owner diagnostics: why a server capability is or isn't available
 * (`GET /v1/admin/diagnostics/capabilities`, be/playback 0a39ee9). Parsed defensively; the
 * reason code is turned into plain words here and the server's detail stays behind
 * "Technical details".
 */
export type ServerCapability = Readonly<{capability: string; available: boolean; code?: string; detail?: string; checkedAt: string}>;

/** The known rows, in display order. Extra items the server sends render after these. */
export const CAPABILITY_ROWS = ['live_tv', 'recording', 'prepared_media', 'hardware_encoding', 'decoder_sandbox', 'dolby_vision_conversion'] as const;

/** Rows that stay hidden until the server sends them (new capabilities, behind presence). */
export const CAPABILITY_BEHIND_PRESENCE = ['decoder_sandbox', 'dolby_vision_conversion'] as const;

export function parseCapabilities(raw: unknown): readonly ServerCapability[] {
  const items = (raw as {items?: unknown} | null)?.items;
  if (!Array.isArray(items)) throw new Error('Invalid capabilities response.');
  const out: ServerCapability[] = [];
  for (const v of items.slice(0, 64)) {
    const o = v as Record<string, unknown> | null;
    if (!o || typeof o.capability !== 'string' || typeof o.available !== 'boolean' || typeof o.checkedAt !== 'string') continue;
    out.push(Object.freeze({
      capability: o.capability.slice(0, 64), available: o.available, checkedAt: o.checkedAt,
      ...(typeof o.code === 'string' && o.code ? {code: o.code.slice(0, 128)} : {}),
      ...(typeof o.detail === 'string' && o.detail ? {detail: o.detail.slice(0, 1000)} : {}),
    }));
  }
  return out;
}

/** Catalogue ids for the plain reason behind a code. */
export function capabilityReasonId(code: string | undefined): string {
  switch (code) {
    case 'decoder_confinement_unavailable': return 'web.capabilities.reason.platform';
    case 'decoder_sandbox_unavailable': return 'web.capabilities.reason.decoderSandboxUnavailable';
    case 'decoder_sandbox_off': return 'web.capabilities.reason.decoderSandboxOff';
    case 'dolby_vision_approximate': return 'web.capabilities.reason.dolbyVisionApproximate';
    case 'ffmpeg_not_configured': case 'ffprobe_not_configured': return 'web.capabilities.reason.tools';
    case 'decoder_dependencies_unavailable': return 'web.capabilities.reason.dependencies';
    case 'channel_runtime_unavailable': case 'delivery_unavailable': case 'delivery-unavailable': return 'web.capabilities.reason.delivery';
    case 'hardware_unavailable': return 'web.capabilities.reason.hardware';
    default: return 'web.capabilities.reason.other';
  }
}
