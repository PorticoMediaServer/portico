/* SPDX-License-Identifier: GPL-3.0-only
 * Real encoded-video qualification, not a runtime demuxer or FEL renderer.
 * The input is the 24-frame procedural joined fixture. A declared test clock
 * uses the HEVC parser's actual POC, with one VFR interval. The authored source
 * MP4 then supplies the authoritative PTS/DTS/configuration for conversion.
 */
#include <libavcodec/avcodec.h>
#include <libavcodec/bsf.h>
#include <libavformat/avformat.h>
#include <libavutil/dovi_meta.h>
#include <libavutil/error.h>
#include <libavutil/intreadwrite.h>
#include <stdio.h>
#include <stdlib.h>

#define CHECK(x) do { if (!(x)) { fprintf(stderr, "check failed line %d: %s\n", __LINE__, #x); exit(1); } } while (0)
#define OK(x) do { int err_ = (x); if (err_ < 0) { char text_[128]; av_strerror(err_, text_, sizeof(text_)); fprintf(stderr, "line %d: %s: %s\n", __LINE__, #x, text_); exit(1); } } while (0)

static const AVRational clock90k = {1, 90000};

static AVFormatContext *output(const char *path, const AVCodecParameters *par, int converted)
{
    AVFormatContext *out = NULL;
    OK(avformat_alloc_output_context2(&out, NULL, "mp4", path));
    CHECK(out);
    AVStream *st = avformat_new_stream(out, NULL);
    CHECK(st);
    OK(avcodec_parameters_copy(st->codecpar, par));
    st->time_base = clock90k;
    st->codecpar->codec_tag = converted ? MKTAG('d','v','h','1') : MKTAG('d','v','h','e');
    out->strict_std_compliance = FF_COMPLIANCE_UNOFFICIAL;
    OK(avio_open(&out->pb, path, AVIO_FLAG_WRITE));
    OK(avformat_write_header(out, NULL));
    return out;
}

static void finish(AVFormatContext **out)
{
    OK(av_write_trailer(*out));
    OK(avio_closep(&(*out)->pb));
    avformat_free_context(*out);
    *out = NULL;
}

static const AVDOVIDecoderConfigurationRecord *config(const AVCodecParameters *par)
{
    const AVPacketSideData *sd = av_packet_side_data_get(par->coded_side_data,
                                  par->nb_coded_side_data, AV_PKT_DATA_DOVI_CONF);
    CHECK(sd && sd->size >= sizeof(AVDOVIDecoderConfigurationRecord));
    return (const void *)sd->data;
}

static void author_source(const char *raw, const char *path)
{
    AVFormatContext *in = NULL;
    OK(avformat_open_input(&in, raw, NULL, NULL));
    OK(avformat_find_stream_info(in, NULL));
    CHECK(in->nb_streams == 1 && in->streams[0]->codecpar->codec_id == AV_CODEC_ID_HEVC);
    AVCodecParameters *par = avcodec_parameters_alloc();
    CHECK(par);
    OK(avcodec_parameters_copy(par, in->streams[0]->codecpar));
    size_t size;
    AVDOVIDecoderConfigurationRecord *dv = av_dovi_alloc(&size);
    CHECK(dv);
    /* Explicit authored fixture configuration, not a production probe guess. */
    dv->dv_version_major = 1;
    dv->dv_profile = 7;
    dv->dv_level = 1;
    dv->rpu_present_flag = dv->el_present_flag = dv->bl_present_flag = 1;
    dv->dv_bl_signal_compatibility_id = 6;
    CHECK(av_packet_side_data_add(&par->coded_side_data, &par->nb_coded_side_data,
                                 AV_PKT_DATA_DOVI_CONF, (uint8_t *)dv, size, 0));
    AVFormatContext *out = output(path, par, 0);
    AVCodecParserContext *parser = av_parser_init(AV_CODEC_ID_HEVC);
    AVCodecContext *codec = avcodec_alloc_context3(NULL);
    CHECK(parser && codec);
    OK(avcodec_parameters_to_context(codec, par));
    parser->flags |= PARSER_FLAG_COMPLETE_FRAMES;
    AVPacket *pkt = av_packet_alloc();
    CHECK(pkt);
    unsigned seen = 0;
    int count = 0, ret, reordered = 0, b_frames = 0;
    while ((ret = av_read_frame(in, pkt)) >= 0) {
        CHECK(pkt->stream_index == 0 && pkt->size > 0 && pkt->size <= 1048576 && count < 24);
        uint8_t *parsed;
        int parsed_size;
        OK(av_parser_parse2(parser, codec, &parsed, &parsed_size, pkt->data, pkt->size,
                            AV_NOPTS_VALUE, AV_NOPTS_VALUE, pkt->pos));
        CHECK(parsed_size == pkt->size && parser->output_picture_number >= 0 && parser->output_picture_number < 24);
        int poc = parser->output_picture_number;
        reordered |= poc != count;
        b_frames += parser->pict_type == AV_PICTURE_TYPE_B;
        CHECK(!(seen & (1U << poc)));
        seen |= 1U << poc;
        pkt->pts = poc * 3750 + (poc >= 12 ? 750 : 0);
        pkt->dts = count * 3750 + (count >= 12 ? 750 : 0) - 11250;
        pkt->duration = poc == 11 ? 4500 : 3750;
        CHECK(pkt->dts <= pkt->pts);
        av_packet_rescale_ts(pkt, clock90k, out->streams[0]->time_base);
        OK(av_interleaved_write_frame(out, pkt));
        av_packet_unref(pkt);
        count++;
    }
    CHECK(ret == AVERROR_EOF && count == 24 && seen == 0xffffffU && reordered && b_frames > 0);
    av_packet_free(&pkt);
    av_parser_close(parser);
    avcodec_free_context(&codec);
    finish(&out);
    avcodec_parameters_free(&par);
    avformat_close_input(&in);
}

static void convert(const char *source, const char *destination)
{
    AVFormatContext *in = NULL;
    OK(avformat_open_input(&in, source, NULL, NULL));
    OK(avformat_find_stream_info(in, NULL));
    CHECK(in->nb_streams == 1);
    const AVDOVIDecoderConfigurationRecord *dv = config(in->streams[0]->codecpar);
    CHECK(dv->dv_profile == 7 && dv->rpu_present_flag && dv->el_present_flag && dv->bl_present_flag &&
          dv->dv_bl_signal_compatibility_id == 6);
    AVBSFContext *bsf = NULL;
    const AVBitStreamFilter *filter = av_bsf_get_by_name("dovi_profile");
    CHECK(filter);
    OK(av_bsf_alloc(filter, &bsf));
    OK(avcodec_parameters_copy(bsf->par_in, in->streams[0]->codecpar));
    bsf->time_base_in = in->streams[0]->time_base;
    OK(av_bsf_init(bsf));
    dv = config(bsf->par_out);
    CHECK(dv->dv_profile == 8 && dv->dv_bl_signal_compatibility_id == 1 && !dv->el_present_flag &&
          dv->rpu_present_flag && dv->bl_present_flag);
    AVFormatContext *out = output(destination, bsf->par_out, 1);
    AVPacket *src = av_packet_alloc(), *dst = av_packet_alloc();
    CHECK(src && dst);
    int count = 0, ret;
    while ((ret = av_read_frame(in, src)) >= 0) {
        int64_t pts = src->pts, dts = src->dts, duration = src->duration;
        int flags = src->flags;
        CHECK(pts != AV_NOPTS_VALUE && dts != AV_NOPTS_VALUE && duration > 0);
        OK(av_bsf_send_packet(bsf, src));
        OK(av_bsf_receive_packet(bsf, dst));
        CHECK(dst->pts == pts && dst->dts == dts && dst->duration == duration && dst->flags == flags);
        dst->stream_index = 0;
        av_packet_rescale_ts(dst, bsf->time_base_out, out->streams[0]->time_base);
        OK(av_interleaved_write_frame(out, dst));
        av_packet_unref(dst);
        count++;
    }
    CHECK(ret == AVERROR_EOF && count == 24);
    OK(av_bsf_send_packet(bsf, NULL));
    CHECK(av_bsf_receive_packet(bsf, dst) == AVERROR_EOF);
    finish(&out);
    av_packet_free(&src);
    av_packet_free(&dst);
    av_bsf_free(&bsf);
    avformat_close_input(&in);
    OK(avformat_open_input(&in, destination, NULL, NULL));
    OK(avformat_find_stream_info(in, NULL));
    dv = config(in->streams[0]->codecpar);
    CHECK(dv->dv_profile == 8 && dv->dv_bl_signal_compatibility_id == 1 && !dv->el_present_flag &&
          dv->rpu_present_flag && dv->bl_present_flag);
    CHECK(in->streams[0]->codecpar->codec_tag == MKTAG('d','v','h','1'));
    avformat_close_input(&in);
}

int main(int argc, char **argv)
{
    CHECK(argc == 4);
    author_source(argv[1], argv[2]);
    convert(argv[2], argv[3]);
    puts("{\"passed\":true,\"packets\":24,\"bFrames\":true,\"testClock\":\"POC-authored with one VFR interval\",\"timestampsPreserved\":true,\"mp4Config\":\"dvh1 profile8 compatibility1, no EL\",\"scope\":\"real encoded fixture conversion, not production separate-PID join or FEL reconstruction\"}");
    return 0;
}
