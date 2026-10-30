/** Lyric-only bounded JSON transport; fetch stays on HttpLocalApi's selected server. */
export async function readLyricsResponse(response: Response, signal: AbortSignal): Promise<unknown> {
    const limit = response.ok ? 4 * 1024 * 1024 : 16384;
    const declared = response.headers.get('Content-Length');
    if (declared !== null && (!/^\d+$/.test(declared) || Number(declared) > limit)) {
        void response.body?.cancel();
        throw new Error('Lyrics response exceeded its limit.');
    }
    if (response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'application/json')
        throw new Error('Invalid lyrics response.');
    let text: string;
    if (response.body && typeof response.body.getReader === 'function') {
        const reader = response.body.getReader(), chunks: Uint8Array[] = [];
        let length = 0;
        const cancel = () => { void reader.cancel().catch(() => { }); };
        signal.addEventListener('abort', cancel, { once: true });
        try {
            for (;;) {
                if (signal.aborted)
                    throw new Error('Lyrics request cancelled.');
                const next = await reader.read();
                if (next.done)
                    break;
                length += next.value.byteLength;
                if (length > limit)
                    throw new Error('Lyrics response exceeded its limit.');
                chunks.push(next.value);
            }
            const bytes = new Uint8Array(length);
            let at = 0;
            for (const chunk of chunks) {
                bytes.set(chunk, at);
                at += chunk.byteLength;
            }
            text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
        }
        finally {
            signal.removeEventListener('abort', cancel);
            cancel();
        }
    }
    else {
        // React Native's fetch may not expose streaming bodies. Native transport
        // cancellation still applies; enforce the payload bound before JSON parsing.
        text = await response.text();
        if (text.length > limit)
            throw new Error('Lyrics response exceeded its limit.');
    }
    if (signal.aborted)
        throw new Error('Lyrics request cancelled.');
    const raw: unknown = JSON.parse(text);
    if (!response.ok) {
        const error = new Error('Lyrics request failed.') as Error & {
            status: number;
            code: string;
        };
        error.status = response.status;
        const value = raw as {
            error?: {
                code?: unknown;
            };
        };
        error.code = typeof value?.error?.code === 'string' ? value.error.code : 'request_failed';
        throw error;
    }
    return raw;
}
