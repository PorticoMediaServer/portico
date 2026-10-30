/* SPDX-License-Identifier: GPL-3.0-only
 * Real dual-PID MPEGTS demux test, including EOF, seek and terminal failures.
 */
#include <libavformat/avformat.h>
#include <libavutil/dovi_meta.h>
#include <libavutil/error.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(x) do { if (!(x)) { fprintf(stderr, "check failed line %d: %s\n", __LINE__, #x); exit(1); } } while (0)

static int read_all(AVFormatContext *in, FILE *out, int joined, int64_t *pts, int64_t *dts, int *count)
{
    AVPacket *packet = av_packet_alloc();
    CHECK(packet);
    int ret;
    *count = 0;
    while ((ret = av_read_frame(in, packet)) >= 0) {
        CHECK(*count < 64 && packet->size > 0 && packet->size <= 8388608);
        if (joined) {
            CHECK(in->streams[packet->stream_index]->id == 0x1011);
            CHECK(packet->pts != AV_NOPTS_VALUE && packet->dts != AV_NOPTS_VALUE);
            pts[*count] = packet->pts;
            dts[*count] = packet->dts;
            if (out)
                CHECK(fwrite(packet->data, 1, packet->size, out) == (size_t)packet->size);
        }
        (*count)++;
        av_packet_unref(packet);
    }
    av_packet_free(&packet);
    return ret;
}

int main(int argc, char **argv)
{
    CHECK(argc == 4);
    int joined = !strcmp(argv[3], "joined24"), refused = !strcmp(argv[3], "refused");
    CHECK(joined || refused || !strcmp(argv[3], "unjoined"));
    AVFormatContext *in = NULL;
    AVDictionary *options = NULL;
    av_dict_set(&options, "merge_dovi", "1", 0);
    av_dict_set(&options, "protocol_whitelist", "file,pipe", 0);
    av_dict_set(&options, "format_whitelist", "mpegts", 0);
    CHECK(avformat_open_input(&in, argv[1], NULL, &options) >= 0);
    CHECK(!av_dict_count(options));
    av_dict_free(&options);
    FILE *out = fopen(argv[2], "wb");
    CHECK(out);
    int64_t pts[64], dts[64];
    int count;
    int ret = read_all(in, out, joined, pts, dts, &count);
    CHECK(!fclose(out));
    if (refused) {
        CHECK(ret == AVERROR_INVALIDDATA);
        AVPacket *packet = av_packet_alloc();
        CHECK(packet && av_read_frame(in, packet) == AVERROR_INVALIDDATA);
        CHECK(avio_seek(in->pb, 0, SEEK_SET) == 0);
        avformat_flush(in);
        CHECK(av_read_frame(in, packet) == AVERROR_INVALIDDATA);
        av_packet_free(&packet);
        printf("{\"passed\":true,\"refused\":true,\"terminalAfterSeek\":true,\"packetsBeforeFailure\":%d}\n", count);
    } else if (joined) {
        CHECK(ret == AVERROR_EOF && count == 24);
        AVStream *base = NULL;
        for (unsigned i = 0; i < in->nb_streams; i++)
            if (in->streams[i]->id == 0x1011)
                base = in->streams[i];
        CHECK(base);
        const AVPacketSideData *sd = av_packet_side_data_get(base->codecpar->coded_side_data,
            base->codecpar->nb_coded_side_data, AV_PKT_DATA_DOVI_CONF);
        CHECK(sd && sd->size >= sizeof(AVDOVIDecoderConfigurationRecord));
        const AVDOVIDecoderConfigurationRecord *cfg = (const void *)sd->data;
        CHECK(cfg->dv_profile == 7 && cfg->rpu_present_flag && cfg->el_present_flag && cfg->bl_present_flag &&
              cfg->dv_bl_signal_compatibility_id == 6);
        CHECK(avio_seek(in->pb, 0, SEEK_SET) == 0);
        avformat_flush(in);
        int64_t after_pts[64], after_dts[64];
        int after_count;
        CHECK(read_all(in, NULL, 1, after_pts, after_dts, &after_count) == AVERROR_EOF && after_count == count);
        CHECK(!memcmp(pts, after_pts, count * sizeof(*pts)) && !memcmp(dts, after_dts, count * sizeof(*dts)));
        printf("{\"passed\":true,\"joinedPackets\":%d,\"seekClockIdentical\":true,\"pts\":[", count);
        for (int i = 0; i < count; i++)
            printf("%s%lld", i ? "," : "", (long long)pts[i]);
        printf("],\"dts\":[");
        for (int i = 0; i < count; i++)
            printf("%s%lld", i ? "," : "", (long long)dts[i]);
        puts("]}");
    } else {
        CHECK(ret == AVERROR_EOF && count == 48);
        puts("{\"passed\":true,\"unjoinedPackets\":48,\"noInventedJoin\":true}");
    }
    avformat_close_input(&in);
    return 0;
}
