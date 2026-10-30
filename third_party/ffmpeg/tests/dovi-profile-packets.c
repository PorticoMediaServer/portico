/* SPDX-License-Identifier: GPL-3.0-only
 * Actual libavcodec/libdovi BSF qualification. Synthetic AUD/EL wrapper + RPU
 * packets prove metadata, filtering and timestamp semantics, not decoded BL/EL
 * imagery. Full video joins and visual FEL loss remain separate fixtures.
 */
#include <libdovi/rpu_parser.h>
#include <libavcodec/bsf.h>
#include <libavcodec/codec_par.h>
#include <libavutil/dovi_meta.h>
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(value) do { if (!(value)) { fprintf(stderr, "check failed at line %d: %s\n", __LINE__, #value); exit(1); } } while (0)

static AVBSFContext *create_filter(int configuration, int profile, int compatibility, int el)
{
    const AVBitStreamFilter *filter = av_bsf_get_by_name("dovi_profile");
    AVBSFContext *context = NULL;
    CHECK(filter && av_bsf_alloc(filter, &context) >= 0);
    context->par_in->codec_type = AVMEDIA_TYPE_VIDEO;
    context->par_in->codec_id = AV_CODEC_ID_HEVC;
    context->time_base_in = (AVRational){1, 90000};
    if (configuration) {
        size_t size;
        AVDOVIDecoderConfigurationRecord *config = av_dovi_alloc(&size);
        CHECK(config);
        config->dv_version_major = 1;
        config->dv_profile = profile;
        config->dv_level = 6;
        config->rpu_present_flag = config->bl_present_flag = 1;
        config->el_present_flag = el;
        config->dv_bl_signal_compatibility_id = compatibility;
        CHECK(av_packet_side_data_add(&context->par_in->coded_side_data,
            &context->par_in->nb_coded_side_data, AV_PKT_DATA_DOVI_CONF,
            (uint8_t *)config, size, 0));
    }
    return context;
}

static size_t start(const uint8_t *data, size_t size, size_t from, size_t *prefix)
{
    for (size_t i = from; i + 2 < size; i++) {
        if (data[i] || data[i+1])
            continue;
        if (data[i+2] == 1) {
            *prefix = 3;
            return i;
        }
        if (!data[i+2] && i + 3 < size && data[i+3] == 1) {
            *prefix = 4;
            return i;
        }
    }
    *prefix = 0;
    return size;
}

static void verify_packet(AVPacket *packet, int frame)
{
    int rpus = 0, auds = 0, els = 0;
    CHECK(packet->pts == 12345 + (int64_t)frame * 3750);
    CHECK(packet->dts == 10000 + (int64_t)frame * 3750);
    CHECK(packet->duration == 3750);
    CHECK(packet->flags & AV_PKT_FLAG_KEY);
    size_t prefix;
    for (size_t at = start(packet->data, packet->size, 0, &prefix); at < (size_t)packet->size;) {
        size_t next_prefix;
        size_t next = start(packet->data, packet->size, at + prefix, &next_prefix);
        const uint8_t *nal = packet->data + at + prefix;
        size_t length = next - at - prefix;
        CHECK(length >= 2);
        int type = (nal[0] >> 1) & 63;
        if (type == 35) {
            CHECK(length == 3 && !memcmp(nal, "\x46\x01\x50", 3));
            auds++;
        } else if (type == 62) {
            DoviRpuOpaque *rpu = dovi_parse_unspec62_nalu(nal, length);
            CHECK(rpu && !dovi_rpu_get_error(rpu));
            const DoviRpuDataHeader *header = dovi_rpu_get_header(rpu);
            CHECK(header && header->guessed_profile == 8 && header->disable_residual_flag);
            dovi_rpu_free_header(header);
            dovi_rpu_free(rpu);
            rpus++;
        } else if (type == 63) {
            els++;
        }
        at = next;
        prefix = next_prefix;
    }
    CHECK(rpus == 1 && auds == 1 && els == 0);
}

static int packet(AVBSFContext *context, const uint8_t *rpu, size_t size, int frame)
{
    static const uint8_t prefix[] = {
        0,0,0,1,0x46,1,0x50,         /* valid AUD, retained byte for byte */
        0,0,0,1,0x7e,1,0x46,1,0x50, /* synthetic EL wrapper, removed */
        0,0,0,1
    };
    AVPacket *input = av_packet_alloc(), *output = av_packet_alloc();
    CHECK(input && output && av_new_packet(input, sizeof(prefix) + size) >= 0);
    memcpy(input->data, prefix, sizeof(prefix));
    memcpy(input->data + sizeof(prefix), rpu, size);
    input->pts = 12345 + (int64_t)frame * 3750;
    input->dts = 10000 + (int64_t)frame * 3750;
    input->duration = 3750;
    input->flags = AV_PKT_FLAG_KEY;
    CHECK(av_bsf_send_packet(context, input) >= 0);
    int result = av_bsf_receive_packet(context, output);
    if (result >= 0)
        verify_packet(output, frame);
    av_packet_free(&input);
    av_packet_free(&output);
    return result;
}

static void fixture(const char *directory, const char *name, int expectation)
{
	int success = expectation > 0;
    char path[4096];
    CHECK(snprintf(path, sizeof(path), "%s/%s", directory, name) < (int)sizeof(path));
    FILE *file = fopen(path, "rb");
    CHECK(file);
    uint8_t data[65536];
    size_t size = fread(data, 1, sizeof(data), file);
    CHECK(size > 0 && size < sizeof(data) && feof(file));
    fclose(file);
    if (expectation < 0) {
        size_t prefix;
        size_t next = start(data, size, 4, &prefix);
        CHECK(next > 8);
        data[next - 3] ^= 1; /* corrupt first RPU CRC, keep NAL boundaries */
    }
    AVBSFContext *context = create_filter(1, 7, 6, 1);
    CHECK(av_bsf_init(context) >= 0);
    const AVPacketSideData *side = av_packet_side_data_get(context->par_out->coded_side_data,
        context->par_out->nb_coded_side_data, AV_PKT_DATA_DOVI_CONF);
    CHECK(side);
    const AVDOVIDecoderConfigurationRecord *config = (const void *)side->data;
    CHECK(config->dv_profile == 8 && config->dv_bl_signal_compatibility_id == 1 &&
          config->rpu_present_flag && config->bl_present_flag && !config->el_present_flag);
    int frames = 0;
    size_t prefix;
    for (size_t at = start(data, size, 0, &prefix); at < size;) {
        size_t next_prefix;
        size_t next = start(data, size, at + prefix, &next_prefix);
        int result = packet(context, data + at + prefix, next - at - prefix, frames++);
        CHECK(success ? result >= 0 : result < 0);
        if (!success)
            break;
        at = next;
        prefix = next_prefix;
    }
    CHECK(success ? frames == 24 : frames == 1);
    if (success) {
        CHECK(av_bsf_send_packet(context, NULL) >= 0);
        AVPacket *out = av_packet_alloc();
        CHECK(out && av_bsf_receive_packet(context, out) == AVERROR_EOF);
        av_packet_free(&out);
    }
    av_bsf_free(&context);
}

int main(int argc, char **argv)
{
    CHECK(argc == 2);
    const int invalid[][4] = {{0,7,6,1}, {1,5,0,0}, {1,7,1,1}, {1,7,6,0}};
    for (size_t i = 0; i < sizeof(invalid)/sizeof(invalid[0]); i++) {
        AVBSFContext *context = create_filter(invalid[i][0], invalid[i][1], invalid[i][2], invalid[i][3]);
        CHECK(av_bsf_init(context) < 0);
        av_bsf_free(&context);
    }
    AVBSFContext *context = create_filter(1,7,6,1);
    const AVOption *mode = av_opt_find(context->priv_data, "mode", NULL, 0, 0);
    CHECK(mode && mode->min == 2 && mode->max == 2 && mode->default_val.i64 == 2);
    CHECK(av_opt_set_int(context->priv_data, "mode", 1, 0) < 0);
    CHECK(av_opt_set_int(context->priv_data, "mode", 2, 0) >= 0);
    av_bsf_free(&context);
    context = create_filter(1,7,6,1);
    CHECK(av_bsf_init(context) >= 0 && av_bsf_send_packet(context, NULL) >= 0);
    AVPacket *empty = av_packet_alloc();
    CHECK(empty && av_bsf_receive_packet(context, empty) == AVERROR_INVALIDDATA);
    av_packet_free(&empty);
    av_bsf_free(&context);
    fixture(argv[1], "dv-7-MEL-V29.rpu", 1);
    fixture(argv[1], "dv-7-MEL-V40.rpu", 1);
    fixture(argv[1], "dv-7-FEL-V29.rpu", 1);
    fixture(argv[1], "dv-7-FEL-V40.rpu", 1);
    fixture(argv[1], "dv-5-V40.rpu", 0);
    fixture(argv[1], "dv-8.1-V40.rpu", 0);
    fixture(argv[1], "dv-8.4-V40.rpu", 0);
    fixture(argv[1], "dv-7-FEL-V40.rpu", -1);
    puts("{\"passed\":true,\"convertedPackets\":96,\"wrongRpuProfilesRejected\":3,\"invalidConfigurationsRejected\":4,\"corruptRpuRejected\":1,\"noRpuRejected\":true,\"modeIntrospection\":true,\"scope\":\"synthetic metadata packets; no decoded BL/EL video proof\"}");
    return 0;
}
