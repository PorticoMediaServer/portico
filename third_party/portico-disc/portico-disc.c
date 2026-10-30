/* SPDX-License-Identifier: GPL-3.0-or-later
 * Bounded disc structure reader. It does not decrypt or assemble loose clips.
 * The helper protocol has generated host fixtures; target bundle and complete
 * disc playback qualification remain separate from this structure reader.
 */
#define _POSIX_C_SOURCE 200809L
#include <errno.h>
#include <inttypes.h>
#include <limits.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#ifdef _WIN32
#include <io.h>
#include <fcntl.h>
#define dup _dup
#define dup2 _dup2
#define close _close
#define fileno _fileno
#define fdopen _fdopen
#else
#include <unistd.h>
#endif
#include <libbluray/bluray.h>
#include <dvdread/dvd_reader.h>
#include <dvdread/ifo_read.h>
#include <dvdread/ifo_types.h>
#include <dvdnav/dvdnav.h>
#include "mpegts_dovi.h"

#define OUTPUT_MAX (1024 * 1024)
#define TITLE_MAX 4096
static char output[OUTPUT_MAX + 1];
static FILE *result_output;
static size_t used;
static int output_error;
static void emit(const char *fmt, ...) {
    va_list ap;
    int n;
    if (output_error) return;
    va_start(ap, fmt);
    n = vsnprintf(output + used, sizeof(output) - used, fmt, ap);
    va_end(ap);
    if (n < 0 || (size_t)n >= sizeof(output) - used) { output_error = 1; return; }
    used += (size_t)n;
}
static int publish(void) {
    if (output_error) return 4;
    if (fwrite(output, 1, used, result_output) != used || fflush(result_output)) return 5;
    return 0;
}
static void language(const uint8_t *lang, size_t len) {
    size_t n = 0;
    while (n < len && lang[n]) {
        if (!((lang[n] >= 'a' && lang[n] <= 'z') || (lang[n] >= 'A' && lang[n] <= 'Z'))) { emit("\"\""); return; }
        n++;
    }
    if (n != 2 && n != 3) { emit("\"\""); return; }
    emit("\"");
    for (size_t i = 0; i < n; i++) emit("%c", lang[i] >= 'A' && lang[i] <= 'Z' ? lang[i] + ('a'-'A') : lang[i]);
    emit("\"");
}
static void probe_start(const char *kind, const char *packaging, const char *encryption) {
    emit("{\"schema\":1,\"kind\":\"%s\",\"packaging\":\"%s\",\"encryption\":\"%s\",\"titles\":[", kind, packaging, encryption);
}
static int bd_dv_pid(const BLURAY_CLIP_INFO *clip, unsigned pid) {
    if (clip->dv_stream_count && !clip->dv_streams) { output_error = 1; return 0; }
    for (unsigned i = 0; i < clip->dv_stream_count; i++) if (clip->dv_streams[i].pid == pid) return 1;
    return 0;
}
static int bd_pid_facts(BLURAY *bd, unsigned clip, uint32_t packets, PD_DoviFacts *facts) {
    uint64_t limit = (uint64_t)packets * 192;
    size_t size = 0;
    if (limit > 1024 * 1024) limit = 1024 * 1024;
    limit -= limit % 192;
    memset(facts, 0, sizeof(*facts));
    if (!limit) return 0;
    uint8_t *buffer = malloc((size_t)limit);
    if (!buffer) return -1;
    if (bd_seek_playitem(bd, clip) < 0) { free(buffer); return -1; }
    while (size < limit) {
        int want = (int)(limit - size);
        if (want > 6144) want = 6144;
        int got = bd_read(bd, buffer + size, want);
        if (got < 0 || got > want) { free(buffer); return -1; }
        if (!got) break;
        size += (unsigned)got;
    }
    pd_dovi_probe(buffer, size, 192, facts);
    free(buffer);
    return 0;
}
static void bd_streams(const BLURAY_STREAM_INFO *streams, unsigned n, const char *kind,
                       const BLURAY_CLIP_INFO *clip_info, unsigned clip,
                       const PD_DoviFacts *facts, const char *role, uint8_t *seen, int *first) {
    if ((n && !streams) || n > 256) { output_error = 1; return; }
    for (unsigned i = 0; i < n; i++) {
        const BLURAY_STREAM_INFO *s = &streams[i];
        if (s->pid >= PD_PID_MAX) { output_error = 1; return; }
        if (seen[s->pid]) continue;
        seen[s->pid] = 1;
        unsigned base = facts->base[s->pid];
        int enhancement = base || bd_dv_pid(clip_info, s->pid);
        emit("%s{\"clipSequence\":%u,\"pid\":%u,\"kind\":\"%s\",\"coding\":%u,\"language\":", *first ? "" : ",", clip, s->pid, kind, s->coding_type);
        language(s->lang, 3);
        /* The pinned libbluray explicitly exposes its Dolby Vision EL STN
         * entries. PMT supplies the separate, validated base PID relation. */
        emit(",\"dolbyVisionEnhancementLayer\":%s,\"dolbyVisionEnhancementKnown\":%s",
             enhancement ? "true" : "false", enhancement ? "true" : "false");
        if (role && clip_info->dv_stream_count) emit(",\"videoRole\":\"%s\"", role);
        if (base) emit(",\"dolbyVisionBasePid\":%u", base);
        emit("}");
        *first = 0;
    }
}
static int probe_bd(BLURAY *bd, const char *packaging) {
    const BLURAY_DISC_INFO *info = bd_get_disc_info(bd);
    uint32_t n, order = 0;
    const char *encryption;
    if (!info || !info->bluray_detected) return 2;
    encryption = info->bdplus_detected ? "bdplus" : info->aacs_detected ? "aacs" : "none";
    probe_start("bluray", packaging, encryption);
    if (strcmp(encryption, "none")) { emit("]}\n"); return publish(); }
    n = bd_get_titles(bd, TITLES_RELEVANT, 60);
    if (n > TITLE_MAX) return 4;
    for (uint32_t i = 0; i < n; i++) {
        BLURAY_TITLE_INFO *t = bd_get_title_info(bd, i, 0);
        int first = 1;
        if (!t) return 3;
        if (!t->angle_count || t->clip_count > 4096 || t->chapter_count > 65535 || (t->clip_count && !t->clips) || (t->chapter_count && !t->chapters)) { bd_free_title_info(t); return 4; }
        if (t->duration < 60 * UINT64_C(90000)) { bd_free_title_info(t); continue; }
        if (!bd_select_playlist(bd, t->playlist) || !bd_select_angle(bd, 0)) { bd_free_title_info(t); return 3; }
        emit("%s{\"discOrder\":%u,\"playlist\":%u,\"playlistFile\":\"%05u.mpls\",\"durationMs\":%" PRIu64 ",\"angleCount\":%u,\"clips\":[", order ? "," : "", order, t->playlist, t->playlist, t->duration/90, t->angle_count);
        for (uint32_t j = 0; j < t->clip_count; j++) {
            BLURAY_CLIP_INFO *c = &t->clips[j];
            /* clip_id is the library-validated five-digit identifier. */
            for (unsigned k = 0; k < 5; k++) if (c->clip_id[k] < '0' || c->clip_id[k] > '9') { bd_free_title_info(t); return 3; }
            emit("%s{\"sequence\":%u,\"file\":\"%.5s.m2ts\",\"inTime90k\":%" PRIu64 ",\"outTime90k\":%" PRIu64 "}", j ? "," : "", j, c->clip_id, c->in_time, c->out_time);
        }
        emit("],\"streams\":[");
        for (uint32_t j = 0; j < t->clip_count; j++) {
            BLURAY_CLIP_INFO *c = &t->clips[j];
            PD_DoviFacts facts;
            uint8_t seen[PD_PID_MAX] = {0};
            if (bd_pid_facts(bd, j, c->pkt_count, &facts) < 0) { bd_free_title_info(t); return 3; }
            bd_streams(c->video_streams, c->video_stream_count, "video", c, j, &facts, "primary", seen, &first);
            bd_streams(c->audio_streams, c->audio_stream_count, "audio", c, j, &facts, NULL, seen, &first);
            bd_streams(c->pg_streams, c->pg_stream_count, "subtitle", c, j, &facts, NULL, seen, &first);
            bd_streams(c->ig_streams, c->ig_stream_count, "interactive", c, j, &facts, NULL, seen, &first);
            bd_streams(c->sec_video_streams, c->sec_video_stream_count, "video", c, j, &facts, "secondary", seen, &first);
            bd_streams(c->sec_audio_streams, c->sec_audio_stream_count, "audio", c, j, &facts, NULL, seen, &first);
            bd_streams(c->dv_streams, c->dv_stream_count, "video", c, j, &facts, "enhancement", seen, &first);
            for (unsigned pid = 32; pid < PD_PID_MAX - 1; pid++) if (facts.base[pid]) {
                BLURAY_STREAM_INFO pmt_stream;
                memset(&pmt_stream, 0, sizeof(pmt_stream));
                pmt_stream.coding_type = 0x24; /* observed PMT HEVC stream_type */
                pmt_stream.pid = facts.base[pid];
                bd_streams(&pmt_stream, 1, "video", c, j, &facts, NULL, seen, &first);
                pmt_stream.pid = pid;
                bd_streams(&pmt_stream, 1, "video", c, j, &facts, NULL, seen, &first);
            }
        }
        emit("],\"chapters\":[");
        for (uint32_t j = 0; j < t->chapter_count; j++) emit("%s{\"startMs\":%" PRIu64 "}", j ? "," : "", t->chapters[j].start/90);
        emit("]}"); order++;
        bd_free_title_info(t);
        if (output_error) return 4;
    }
    emit("]}\n"); return publish();
}
static int position_bd(BLURAY *bd, unsigned playlist, uint64_t ms, int64_t *offset, uint64_t *time) {
    const BLURAY_DISC_INFO *info = bd_get_disc_info(bd);
    uint32_t count;
    int found = 0;
    if (!info || !info->bluray_detected || info->aacs_detected || info->bdplus_detected) return 6;
    if (ms > UINT64_MAX/90) return 2;
    count = bd_get_titles(bd, TITLES_RELEVANT, 60);
    if (count > TITLE_MAX) return 4;
    for (uint32_t i = 0; i < count; i++) {
        BLURAY_TITLE_INFO *title = bd_get_title_info(bd, i, 0);
        if (!title) return 3;
        if (title->playlist == playlist) {
            found = title->duration >= 60 * UINT64_C(90000) && ms*90 < title->duration;
            bd_free_title_info(title);
            break;
        }
        bd_free_title_info(title);
    }
    if (!found) return 2;
    if (!bd_select_playlist(bd, playlist) || !bd_select_angle(bd, 0)) return 3;
    *offset = bd_seek_time(bd, ms*90);
    if (*offset < 0) return 3;
    *time = bd_tell_time(bd)/90;
    if (*time > ms) return 3;
    return 0;
}
static int seek_bd(BLURAY *bd, unsigned playlist, uint64_t ms) {
    int64_t offset;
    uint64_t time;
    int status = position_bd(bd, playlist, ms, &offset, &time);
    if (status) return status;
    emit("{\"schema\":1,\"playlist\":%u,\"requestedMs\":%" PRIu64 ",\"byteOffset\":%" PRId64 ",\"titleTimeMs\":%" PRIu64 "}\n", playlist, ms, offset, time);
    return publish();
}
/* No libdvdcss is linked. Detect scrambled PES headers in the bounded title
 * prefix; unreadable sectors fail the probe. Later media reads are still
 * required to reject scrambled packets. This is not a decryption operation. */
static int dvd_scrambled(dvd_reader_t *dvd, int vts) {
    uint8_t block[2048];
    dvd_file_t *vob = DVDOpenFile(dvd, vts, DVD_READ_TITLE_VOBS);
    ssize_t count;
    if (!vob) return -1;
    for (int i = 0; i < 1024; i++) {
        count = DVDReadBlocks(vob, i, 1, block);
        if (count < 0) { DVDCloseFile(vob); return -1; }
        if (count == 0) break;
        for (size_t j = 0; j + 9 < sizeof(block); j++) {
            if (block[j] || block[j+1] || block[j+2] != 1) continue;
            unsigned id = block[j+3];
            if ((id == 0xbd || (id >= 0xc0 && id <= 0xef)) && (block[j+6] & 0xc0) == 0x80 && (block[j+6] & 0x30)) { DVDCloseFile(vob); return 1; }
        }
    }
    DVDCloseFile(vob); return 0;
}
static void dvd_language(uint16_t code) {
    uint8_t l[3] = {(uint8_t)(code >> 8), (uint8_t)code, 0}; language(l, 2);
}
static void dvd_stream(unsigned pid, const char *kind, unsigned coding, uint16_t lang, int *first) {
    emit("%s{\"clipSequence\":0,\"pid\":%u,\"kind\":\"%s\",\"coding\":%u,\"language\":", *first ? "" : ",", pid, kind, coding);
    dvd_language(lang); emit(",\"dolbyVisionEnhancementLayer\":false,\"dolbyVisionEnhancementKnown\":true}"); *first = 0;
}
static int probe_dvd_readers(dvd_reader_t *dvd, const char *path, void *stream, dvd_reader_stream_cb *callbacks, const char *packaging) {
    ifo_handle_t *vmg = NULL, *vts = NULL;
    dvdnav_t *nav = NULL;
    int ret = 3, first_title = 1;
    unsigned order = 0;
    if (!dvd) return 2;
    vmg = ifoOpen(dvd, 0);
    if (!vmg || !vmg->tt_srpt || vmg->tt_srpt->nr_of_srpts > 99) goto done;
    if ((path ? dvdnav_open(&nav, path) : dvdnav_open_stream(&nav, stream, callbacks)) != DVDNAV_STATUS_OK ||
        dvdnav_set_readahead_flag(nav, 0) != DVDNAV_STATUS_OK) goto done;
    /* Check every referenced VTS before publishing any playable title. */
    for (unsigned i = 0; i < vmg->tt_srpt->nr_of_srpts; i++) {
        int status = dvd_scrambled(dvd, vmg->tt_srpt->title[i].title_set_nr);
        if (status < 0) goto done;
        if (status) { probe_start("dvd", packaging, "css"); emit("]}\n"); ret = publish(); goto done; }
    }
    probe_start("dvd", packaging, "none");
    for (unsigned i = 0; i < vmg->tt_srpt->nr_of_srpts; i++) {
        title_info_t ti = vmg->tt_srpt->title[i];
        uint64_t *ends = NULL, duration = 0;
        uint32_t count;
        unsigned pgcn;
        pgc_t *pgc;
        int first = 1;
        count = dvdnav_describe_title_chapters(nav, i+1, &ends, &duration);
        if (!count || !ends || count > 65535) { free(ends); goto done; }
        if (duration < 60*UINT64_C(90000)) { free(ends); continue; }
        vts = ifoOpen(dvd, ti.title_set_nr);
        if (!vts || !vts->vts_ptt_srpt || !vts->vts_pgcit || !vts->vtsi_mat || !ti.nr_of_ptts || !ti.nr_of_angles || !ti.vts_ttn || ti.vts_ttn > vts->vts_ptt_srpt->nr_of_srpts) { free(ends); goto done; }
        if (!vts->vts_ptt_srpt->title[ti.vts_ttn-1].nr_of_ptts || !vts->vts_ptt_srpt->title[ti.vts_ttn-1].ptt) { free(ends); goto done; }
        pgcn = vts->vts_ptt_srpt->title[ti.vts_ttn-1].ptt[0].pgcn;
        if (!pgcn || pgcn > vts->vts_pgcit->nr_of_pgci_srp) { free(ends); goto done; }
        pgc = vts->vts_pgcit->pgci_srp[pgcn-1].pgc;
        if (!pgc || vts->vtsi_mat->nr_of_vts_audio_streams > 8 || vts->vtsi_mat->nr_of_vts_subp_streams > 32) { free(ends); goto done; }
        emit("%s{\"discOrder\":%u,\"title\":%u,\"vts\":%u,\"pgc\":%u,\"durationMs\":%" PRIu64 ",\"angleCount\":%u,\"clips\":[],\"streams\":[", first_title ? "" : ",", order++, i+1, ti.title_set_nr, pgcn, duration/90, ti.nr_of_angles);
        first_title = 0;
        dvd_stream(0x1e0, "video", vts->vtsi_mat->vts_video_attr.mpeg_version ? 2 : 1, 0, &first);
        for (unsigned j = 0; j < vts->vtsi_mat->nr_of_vts_audio_streams; j++) {
            audio_attr_t a = vts->vtsi_mat->vts_audio_attr[j];
            if (!(pgc->audio_control[j] & 0x8000)) continue;
            unsigned pos = (pgc->audio_control[j] >> 8) & 0x7f;
            unsigned pid = a.audio_format == 0 ? 0x80+pos : a.audio_format == 4 ? 0xa0+pos : a.audio_format == 6 ? 0x88+pos : 0x1c0+pos;
            dvd_stream(pid, "audio", a.audio_format, a.lang_code, &first);
        }
        for (unsigned j = 0; j < vts->vtsi_mat->nr_of_vts_subp_streams; j++) {
            uint32_t control = pgc->subp_control[j];
            if (!(control & UINT32_C(0x80000000))) continue;
            /* Preserve every enabled aspect-ratio presentation; source IDs
             * are substream IDs, not a guessed track ordinal. */
            for (unsigned k = 0; k < 4; k++) dvd_stream(0x20 + ((control >> (k*8)) & 0x1f), "subtitle", 0, vts->vtsi_mat->vts_subp_attr[j].lang_code, &first);
        }
        emit("],\"chapters\":[");
        for (uint32_t j = 0; j < count; j++) emit("%s{\"startMs\":%" PRIu64 "}", j ? "," : "", j ? ends[j-1]/90 : UINT64_C(0));
        emit("]}"); free(ends); ifoClose(vts); vts = NULL;
        if (output_error) { ret = 4; goto done; }
    }
    emit("]}\n"); ret = publish();
done:
    if (vts) ifoClose(vts);
    if (nav) dvdnav_close(nav);
    if (vmg) ifoClose(vmg);
    DVDClose(dvd); return ret;
}
static int probe_dvd(const char *path, const char *packaging) {
    return probe_dvd_readers(DVDOpen(path), path, NULL, NULL, packaging);
}

#ifndef _WIN32
/* The parent brokers one captured immutable ISO over inherited pipes. There
 * are no paths, URLs, credentials, HTTP parser or network calls in this mode.
 * The same parent's RemoteObject owns all conditional range/source checks. */
typedef struct { uint64_t size, position; int failed; } remote_image;
#define REMOTE_RANGE_MAX (2U * 1024U * 1024U)
static int remote_transfer(int fd, void *buffer, size_t length, int writing) {
    unsigned char *p = buffer;
    while (length) {
        ssize_t n = writing ? write(fd, p, length) : read(fd, p, length);
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) return 0;
        p += n; length -= (size_t)n;
    }
    return 1;
}
static int remote_read_at(remote_image *image, uint64_t offset, void *buffer, size_t length) {
    unsigned char *target = buffer;
    if (image->failed || offset > image->size || length > image->size - offset) return 0;
    while (length) {
        unsigned char request[16] = {'P','D','R','1'}, reply[8];
        uint32_t part = length > REMOTE_RANGE_MAX ? REMOTE_RANGE_MAX : (uint32_t)length;
        for (int i = 0; i < 8; i++) request[4+i] = (unsigned char)(offset >> (56-8*i));
        for (int i = 0; i < 4; i++) request[12+i] = (unsigned char)(part >> (24-8*i));
        if (!remote_transfer(3, request, sizeof(request), 1) ||
            !remote_transfer(4, reply, sizeof(reply), 0) || memcmp(reply, "PDA1", 4) ||
            (((uint32_t)reply[4]<<24)|((uint32_t)reply[5]<<16)|((uint32_t)reply[6]<<8)|reply[7]) != part ||
            !remote_transfer(4, target, part, 0)) {
            image->failed = 1; return 0;
        }
        offset += part; target += part; length -= part;
    }
    return 1;
}
static int remote_bd_blocks(void *handle, void *buffer, int lba, int blocks) {
    remote_image *image = handle;
    if (lba < 0 || blocks <= 0 || blocks > INT_MAX / 2048) return -1;
    return remote_read_at(image, (uint64_t)lba * 2048, buffer, (size_t)blocks * 2048) ? blocks : -1;
}
static int remote_dvd_seek(void *handle, uint64_t position) {
    remote_image *image = handle;
    if (image->failed || position > image->size) return -1;
    image->position = position; return 0;
}
static int remote_dvd_read(void *handle, void *buffer, int length) {
    remote_image *image = handle;
    if (length < 0 || !remote_read_at(image, image->position, buffer, (size_t)length)) return -1;
    image->position += (uint64_t)length; return length;
}
static int probe_remote_image(uint64_t size) {
    remote_image image = {size, 0, 0}, navigation = {size, 0, 0};
    BLURAY *bd = bd_init();
    dvd_reader_t *dvd;
    dvd_reader_stream_cb callbacks = {remote_dvd_seek, remote_dvd_read, NULL};
    int result;
    if (!size || size > INT64_MAX || size % 2048) return 2;
    if (!bd) return 3;
    if (bd_open_stream(bd, &image, remote_bd_blocks) && bd_get_disc_info(bd) && bd_get_disc_info(bd)->bluray_detected) {
        result = probe_bd(bd, "iso"); bd_close(bd); return image.failed ? 3 : result;
    }
    bd_close(bd);
    if (image.failed) return 3;
    image.position = 0;
    dvd = DVDOpenStream(&image, &callbacks);
    result = probe_dvd_readers(dvd, NULL, &navigation, &callbacks, "iso");
    return image.failed || navigation.failed ? 3 : result;
}
static int seek_remote_image(uint64_t size, unsigned playlist, uint64_t ms) {
    remote_image image = {size, 0, 0};
    BLURAY *bd = bd_init();
    int result;
    if (!size || size > INT64_MAX || size % 2048 || !bd) { if (bd) bd_close(bd); return 2; }
    if (!bd_open_stream(bd, &image, remote_bd_blocks)) { bd_close(bd); return 3; }
    result = seek_bd(bd, playlist, ms);
    bd_close(bd); return image.failed ? 3 : result;
}
static int stream_remote_bluray(uint64_t size, unsigned playlist, uint64_t ms, uint64_t expected_offset, uint64_t expected_time) {
    remote_image image = {size, 0, 0};
    BLURAY *bd = bd_init();
    unsigned char buffer[6144 * 16];
    int64_t offset;
    uint64_t time;
    uint64_t written = 0;
    int result, count;
    if (!size || size > INT64_MAX || size % 2048 || expected_offset > INT64_MAX || !bd) { if (bd) bd_close(bd); return 2; }
    if (!bd_open_stream(bd, &image, remote_bd_blocks)) { bd_close(bd); return 3; }
    result = position_bd(bd, playlist, ms, &offset, &time);
    if (result || (uint64_t)offset != expected_offset || time != expected_time) {
        bd_close(bd); return result ? result : 3;
    }
    /* Only libbluray reads the selected title in its branching/clip order.
     * A source/broker error is a nonzero exit, never successful end-of-title. */
    while ((count = bd_read(bd, buffer, sizeof(buffer))) > 0) {
        if (image.failed || (size_t)count > sizeof(buffer) || (uint64_t)count > UINT64_MAX-written ||
            fwrite(buffer, 1, (size_t)count, result_output) != (size_t)count) { bd_close(bd); return 3; }
        written += (uint64_t)count;
    }
    bd_close(bd);
    return count < 0 || !written || image.failed || fflush(result_output) ? 3 : 0;
}
#endif
static int number(const char *s, uint64_t *out) {
    char *end;
    if (!s || !*s) return 0;
    for (const char *p = s; *p; p++) if (*p < '0' || *p > '9') return 0;
    errno = 0; *out = strtoull(s, &end, 10); return !errno && !*end;
}
static int run(int argc, char **argv) {
    struct stat st;
    BLURAY *bd;
    int result;
#ifndef _WIN32
    if (argc == 3 && !strcmp(argv[1], "probe-image-stream")) {
        uint64_t size;
        if (!number(argv[2], &size)) return 2;
        return probe_remote_image(size);
    }
    if (argc == 5 && !strcmp(argv[1], "seek-image-stream")) {
        uint64_t size, playlist, ms;
        if (!number(argv[2], &size) || !number(argv[3], &playlist) || playlist > 99999 || !number(argv[4], &ms)) return 2;
        return seek_remote_image(size, (unsigned)playlist, ms);
    }
    if (argc == 7 && !strcmp(argv[1], "stream-bluray-image")) {
        uint64_t size, playlist, ms, offset, time;
        if (!number(argv[2], &size) || !number(argv[3], &playlist) || playlist > 99999 || !number(argv[4], &ms) ||
            !number(argv[5], &offset) || !number(argv[6], &time)) return 2;
        return stream_remote_bluray(size, (unsigned)playlist, ms, offset, time);
    }
#endif
    if (argc < 3 || stat(argv[2], &st) || (!S_ISDIR(st.st_mode) && !S_ISREG(st.st_mode))) return 2;
    if (!strcmp(argv[1], "probe") && argc == 3) {
        bd = bd_open(argv[2], NULL);
        if (bd && bd_get_disc_info(bd) && bd_get_disc_info(bd)->bluray_detected) {
            result = probe_bd(bd, S_ISDIR(st.st_mode) ? "folder" : "iso"); bd_close(bd); return result;
        }
        if (bd) bd_close(bd);
        return probe_dvd(argv[2], S_ISDIR(st.st_mode) ? "folder" : "iso");
    }
    if (!strcmp(argv[1], "seek") && argc == 5) {
        uint64_t playlist, ms;
        if (!number(argv[3], &playlist) || playlist > 99999 || !number(argv[4], &ms)) return 2;
        bd = bd_open(argv[2], NULL); if (!bd) return 3;
        result = seek_bd(bd, (unsigned)playlist, ms); bd_close(bd); return result;
    }
    return 2;
}

int main(int argc, char **argv) {
    /* Some library builds print diagnostics directly to stdout, bypassing
     * their logger callbacks. Keep the bounded JSON channel on its own fd
     * before any library is opened; all library stdout becomes stderr. */
    int fd = dup(fileno(stdout));
    int status;
    if (fd < 0) return 5;
#ifdef _WIN32
    if (_setmode(fd, _O_BINARY) < 0) { close(fd); return 5; }
#endif
    if (dup2(fileno(stderr), fileno(stdout)) < 0) { close(fd); return 5; }
    result_output = fdopen(fd, "wb");
    if (!result_output) { close(fd); return 5; }
    status = run(argc, argv);
    if (fclose(result_output) && status == 0) status = 5;
    return status;
}
