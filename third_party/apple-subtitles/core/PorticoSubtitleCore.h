#ifndef PORTICO_SUBTITLE_CORE_H
#define PORTICO_SUBTITLE_CORE_H
#include <stddef.h>
#include <stdint.h>

typedef struct PSRenderer PSRenderer;
typedef struct { const char *name; const uint8_t *bytes; size_t size; } PSFont;
/* Bytes are copied. No network, filesystem, or external resource access is allowed. */
PSRenderer *ps_open(const char *kind, const uint8_t *bytes, size_t size,
                    const uint8_t *companion, size_t companion_size,
                    const PSFont *fonts, size_t font_count, char *error, size_t error_size);
void ps_close(PSRenderer *renderer);
/* Source-relative microseconds; output is premultiplied sRGB RGBA, valid until next call.
 * ASS animates at the caller's video-frame clock. Bitmap dimensions are the decoder's
 * composition/display dimensions, never a fabricated 1920x1080 canvas. */
const uint8_t *ps_render(PSRenderer *renderer, int64_t source_us, int width, int height,
                         int *output_width, int *output_height, int *changed);
typedef struct { const char *font; double size; uint32_t color, background;
                 int border_style, alignment; double outline, shadow, margin_percent; } PSStyle;
/* NULL preserves authored ASS styles; otherwise use the validated app/OS caption style. */
void ps_style(PSRenderer *renderer, const PSStyle *style);
/* Shared compositing primitive, also used by the pixel conformance harness. */
void ps_blend(uint8_t *rgba, int width, int height, int x, int y, int w, int h,
              const uint8_t *mask, int stride, uint32_t rrggbbaa);
/* Ogg Opus uses the same pinned FFmpeg, with raw decoder trims exposed explicitly. */
typedef struct PSAudio PSAudio;
typedef int (*PSAudioRead)(void *, uint8_t *, int);
typedef int64_t (*PSAudioSeek)(void *, int64_t, int);
PSAudio *ps_audio_open(void *opaque, PSAudioRead read, PSAudioSeek seek, int *rate, int *channels);
int ps_audio_read(PSAudio *audio, float **planes, int frames);
int ps_audio_rewind(PSAudio *audio);
int ps_audio_channel_at(PSAudio *audio, int channel);
void ps_audio_close(PSAudio *audio);
#endif
