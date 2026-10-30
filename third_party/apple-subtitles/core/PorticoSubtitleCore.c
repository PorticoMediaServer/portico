#include "PorticoSubtitleCore.h"
#include <ass/ass.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/mem.h>
#include <libavutil/intreadwrite.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <limits.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>

enum { PS_BYTES = 64 * 1024 * 1024, PS_PIXELS = 4096 * 2304 };
typedef struct { uint8_t *data; size_t size; int64_t at; } PSBytes;
struct PSRenderer {
    ASS_Library *library; ASS_Renderer *ass; ASS_Track *track;
    AVFormatContext *format; AVCodecContext *decoder;
    const char *demuxer;enum AVCodecID codec_id; AVIOContext *io;
    PSBytes bytes, pair; int stream; AVPacket *packet;
    AVSubtitle displayed, pending; int has_displayed, has_pending, eof;
    int64_t display_start, display_end, pending_start, pending_end, last_us;
    uint8_t *canvas; size_t capacity; int width, height;
};
static void ps_error(char *out,size_t size,const char *message) { if(out && size)snprintf(out,size,"%s",message); }
static int ps_read(void *opaque,uint8_t *out,int size) {
    PSBytes *b=opaque; if(b->at >= (int64_t)b->size)return AVERROR_EOF;
    size_t n=b->size-(size_t)b->at;if(n>(size_t)size)n=(size_t)size;
    memcpy(out,b->data+b->at,n);b->at+=(int64_t)n;return (int)n;
}
static int64_t ps_seek(void *opaque,int64_t offset,int whence) {
    PSBytes *b=opaque;if(whence & AVSEEK_SIZE)return (int64_t)b->size;
    whence &= ~AVSEEK_FORCE;int64_t base=whence==SEEK_SET?0:whence==SEEK_CUR?b->at:whence==SEEK_END?(int64_t)b->size:-1;
    if(base<0 || offset < -base || offset > (int64_t)b->size-base)return AVERROR(EINVAL);
    return b->at=base+offset;
}
static AVIOContext *ps_io(PSBytes *b) {
    uint8_t *buffer=av_malloc(32768);if(!buffer)return NULL;
    AVIOContext *io=avio_alloc_context(buffer,32768,0,b,ps_read,NULL,ps_seek);
    if(!io)av_free(buffer);
    return io;
}
static int ps_io_open(AVFormatContext *format,AVIOContext **io,const char *url,int flags,AVDictionary **options) {
    (void)format;(void)io;(void)url;(void)flags;(void)options;
    return AVERROR(EACCES);
}
static int ps_io_close(AVFormatContext *format,AVIOContext *io) {
    (void)format;
    av_freep(&io->buffer);avio_context_free(&io);return 0;
}
/* VobSub opens a private nested MPEG demuxer without inheriting io_open. Its SUB must
 * be seekable. Only the already-authorized bounded companion is spooled; no IDX path
 * is ever opened. The generated 0600 path is unlinked as soon as FFmpeg owns its FD. */
static int ps_companion_file(PSRenderer *r,char *path,size_t size) {
    const char *tmp=getenv("TMPDIR");
    if(!tmp||tmp[0]!='/'||snprintf(path,size,"%s/portico-vobsub-XXXXXX",tmp)>=(int)size)return -1;
    int fd=mkstemp(path);if(fd<0)return -1;
    if(fcntl(fd,F_SETFD,FD_CLOEXEC)<0){unlink(path);close(fd);return -1;}
    size_t at=0;while(at<r->pair.size){
        ssize_t n=write(fd,r->pair.data+at,r->pair.size-at);
        if(n<0&&errno==EINTR)continue;
        if(n<=0){unlink(path);close(fd);return -1;}at+=(size_t)n;
    }
    return fd;
}
void ps_blend(uint8_t *rgba,int width,int height,int x,int y,int w,int h,const uint8_t *mask,int stride,uint32_t color) {
    unsigned opacity=255-(color&255),red=color>>24,green=(color>>16)&255,blue=(color>>8)&255;
    for(int j=0;j<h;j++)for(int i=0;i<w;i++) {
        int px=x+i,py=y+j;if(px<0||py<0||px>=width||py>=height)continue;
        unsigned a=(mask[j*stride+i]*opacity+127)/255,inv=255-a;
        uint8_t *p=rgba+((size_t)py*width+px)*4;
        p[0]=(uint8_t)((red*a+p[0]*inv+127)/255);p[1]=(uint8_t)((green*a+p[1]*inv+127)/255);
        p[2]=(uint8_t)((blue*a+p[2]*inv+127)/255);p[3]=(uint8_t)(a+(p[3]*inv+127)/255);
    }
}
static int ps_canvas(PSRenderer *r,int width,int height) {
    if(width<1||height<1||width>4096||height>2304||(size_t)width*height>PS_PIXELS)return 0;
    size_t bytes=(size_t)width*height*4;
    if(bytes>r->capacity){uint8_t *next=realloc(r->canvas,bytes);if(!next)return 0;r->canvas=next;r->capacity=bytes;}
    r->width=width;r->height=height;memset(r->canvas,0,bytes);return 1;
}
static int ps_bitmap_open(PSRenderer *r) {
    r->io=ps_io(&r->bytes);r->format=avformat_alloc_context();if(!r->io||!r->format)return 0;
    r->format->pb=r->io;r->format->flags|=AVFMT_FLAG_CUSTOM_IO;r->format->opaque=r;r->format->io_open=ps_io_open;r->format->io_close2=ps_io_close;
    r->format->probesize=8*1024*1024;r->format->max_analyze_duration=5*AV_TIME_BASE;
    AVDictionary *options=NULL;char path[PATH_MAX]={0};int fd=-1;
    if(r->pair.size){
        fd=ps_companion_file(r,path,sizeof(path));if(fd<0)return 0;
        if(av_dict_set(&options,"sub_name",path,0)<0||av_dict_set(&options,"protocol_whitelist","file",0)<0){av_dict_free(&options);unlink(path);close(fd);return 0;}
    }
    int opened=avformat_open_input(&r->format,"memory.idx",av_find_input_format(r->demuxer),&options);
    av_dict_free(&options);if(fd>=0){unlink(path);close(fd);}
    if(opened<0)return 0;
    r->stream=-1;for(unsigned i=0;i<r->format->nb_streams;i++)if(r->format->streams[i]->codecpar->codec_id==r->codec_id){r->stream=(int)i;break;}
    if(r->stream<0)return 0;
    r->decoder=avcodec_alloc_context3(avcodec_find_decoder(r->codec_id));if(!r->decoder)return 0;
    return avcodec_parameters_to_context(r->decoder,r->format->streams[r->stream]->codecpar)>=0&&avcodec_open2(r->decoder,NULL,NULL)>=0;
}
PSRenderer *ps_open(const char *kind,const uint8_t *bytes,size_t size,const uint8_t *companion,size_t companion_size,const PSFont *fonts,size_t font_count,char *error,size_t error_size) {
    if(!kind||!bytes||!size||size>PS_BYTES||companion_size>PS_BYTES-size||font_count>16){ps_error(error,error_size,"Invalid or oversized subtitle payload.");return NULL;}
    PSRenderer *r=calloc(1,sizeof(*r));if(!r)return NULL;r->last_us=-1;
    if(!strcmp(kind,"ass")) {
        r->library=ass_library_init();if(!r->library)goto fail;
        size_t total=0;for(size_t i=0;i<font_count;i++){
            if(!fonts[i].name||!fonts[i].bytes||fonts[i].size>8*1024*1024||total+fonts[i].size>32*1024*1024)goto fail;
            total+=fonts[i].size;ass_add_font(r->library,(char *)fonts[i].name,(char *)fonts[i].bytes,(int)fonts[i].size);
        }
        r->ass=ass_renderer_init(r->library);if(!r->ass)goto fail;
        ass_set_cache_limits(r->ass,1000,32);
        ass_set_fonts(r->ass,NULL,"sans-serif",ASS_FONTPROVIDER_AUTODETECT,NULL,1);
        r->track=ass_read_memory(r->library,(char *)bytes,size,NULL);if(!r->track)goto fail;
        return r;
    }
    const char *demux=!strcmp(kind,"pgs")?"sup":!strcmp(kind,"vobsub")?"vobsub":!strcmp(kind,"dvb")?"matroska":NULL;
    enum AVCodecID codec=!strcmp(kind,"pgs")?AV_CODEC_ID_HDMV_PGS_SUBTITLE:!strcmp(kind,"vobsub")?AV_CODEC_ID_DVD_SUBTITLE:AV_CODEC_ID_DVB_SUBTITLE;
    if(!demux || (!strcmp(kind,"vobsub")&&!companion_size))goto fail;
    r->bytes.data=av_memdup(bytes,size);r->bytes.size=size;
    if(companion_size){r->pair.data=av_memdup(companion,companion_size);r->pair.size=companion_size;if(!r->pair.data)goto fail;}
    r->demuxer=demux;r->codec_id=codec;
    if(!r->bytes.data||!ps_bitmap_open(r))goto fail;
    r->packet=av_packet_alloc();if(!r->packet)goto fail;return r;
fail:
    ps_error(error,error_size,"The subtitle payload could not be decoded.");ps_close(r);return NULL;
}
void ps_close(PSRenderer *r) {
    if(!r)return;
    if(r->track)ass_free_track(r->track);
    if(r->ass)ass_renderer_done(r->ass);
    if(r->library)ass_library_done(r->library);
    if(r->has_displayed)avsubtitle_free(&r->displayed);
    if(r->has_pending)avsubtitle_free(&r->pending);
    av_packet_free(&r->packet);avcodec_free_context(&r->decoder);avformat_close_input(&r->format);
    if(r->io){av_freep(&r->io->buffer);avio_context_free(&r->io);}
    av_free(r->bytes.data);av_free(r->pair.data);free(r->canvas);free(r);
}
static int ps_decode(PSRenderer *r) {
    while(av_read_frame(r->format,r->packet)>=0){
        AVPacket *p=r->packet;if(p->stream_index!=r->stream){av_packet_unref(p);continue;}
        AVSubtitle sub={0};int got=0;int result=avcodec_decode_subtitle2(r->decoder,&sub,&got,p);
        int64_t at=p->pts!=AV_NOPTS_VALUE?av_rescale_q(p->pts,r->format->streams[r->stream]->time_base,AV_TIME_BASE_Q):0;
        av_packet_unref(p);if(result<0){avsubtitle_free(&sub);continue;}if(!got)continue;
        if(sub.pts!=AV_NOPTS_VALUE)at=sub.pts;
        r->pending=sub;r->has_pending=1;r->pending_start=at+(int64_t)sub.start_display_time*1000;
        r->pending_end=sub.end_display_time==UINT_MAX?INT64_MAX:at+(int64_t)sub.end_display_time*1000;
        if(r->pending_end<=r->pending_start)r->pending_end=INT64_MAX;
        return 1;
    }
    r->eof=1;return 0;
}
void ps_style(PSRenderer *r,const PSStyle *style) {
    if(!r||!r->track)return;
    if(!style){ass_set_selective_style_override_enabled(r->ass,ASS_OVERRIDE_DEFAULT);return;}
    ASS_Style override={0};double scale=(r->track->PlayResY>0?r->track->PlayResY:360)/360.0;
    override.FontName=(char *)style->font;override.FontSize=style->size*scale;override.ScaleX=override.ScaleY=1;
    override.PrimaryColour=override.SecondaryColour=style->color;override.OutlineColour=0x00000000;override.BackColour=style->background;
    override.BorderStyle=style->border_style;override.Outline=style->outline*scale;override.Shadow=style->shadow*scale;
    override.Alignment=style->alignment;override.MarginL=override.MarginR=12*scale;override.MarginV=style->margin_percent*(r->track->PlayResY>0?r->track->PlayResY:360);
    ass_set_selective_style_override(r->ass,&override);ass_set_selective_style_override_enabled(r->ass,ASS_OVERRIDE_FULL_STYLE);
}
const uint8_t *ps_render(PSRenderer *r,int64_t us,int width,int height,int *ow,int *oh,int *changed) {
    if(!r||!ow||!oh||!changed||us<0)return NULL;
    *changed=1;
    if(r->track){
        int resize=width!=r->width||height!=r->height;
        ass_set_frame_size(r->ass,width,height);ass_set_storage_size(r->ass,0,0);
        ASS_Image *images=ass_render_frame(r->ass,r->track,us/1000,changed);
        if(resize)*changed=1;
        if(!*changed&&r->canvas){*ow=r->width;*oh=r->height;return r->canvas;}
        if(!ps_canvas(r,width,height))return NULL;
        for(ASS_Image *i=images;i;i=i->next)ps_blend(r->canvas,width,height,i->dst_x,i->dst_y,i->w,i->h,i->bitmap,i->stride,i->color);
    }else{
        if(r->last_us>=0&&us<r->last_us){
            /* Raw PGS has no seek index. Reopen the bounded memory input so its display
             * state and palette are rebuilt from an actual composition epoch. */
            if(r->has_pending)avsubtitle_free(&r->pending);
            if(r->has_displayed)avsubtitle_free(&r->displayed);
            r->has_pending=r->has_displayed=r->eof=0;
            avcodec_free_context(&r->decoder);avformat_close_input(&r->format);
            if(r->io){av_freep(&r->io->buffer);avio_context_free(&r->io);}
            r->bytes.at=r->pair.at=0;if(!ps_bitmap_open(r))return NULL;
        }
        int replaced=0;while(r->has_pending||(!r->eof&&ps_decode(r))){
            if(r->pending_start>us)break;
            if(r->has_displayed)avsubtitle_free(&r->displayed);
            r->displayed=r->pending;memset(&r->pending,0,sizeof(r->pending));
            r->display_start=r->pending_start;r->display_end=r->pending_end;r->has_displayed=1;r->has_pending=0;replaced=1;
        }
        int expired=r->has_displayed&&us>=r->display_end;
        *changed=replaced||r->last_us<0||(expired&&r->last_us<r->display_end);
        if(!*changed&&r->canvas){*ow=r->width;*oh=r->height;r->last_us=us;return r->canvas;}
        if(!ps_canvas(r,r->decoder->width,r->decoder->height))return NULL;
        if(r->has_displayed&&!expired)for(unsigned n=0;n<r->displayed.num_rects;n++){
            AVSubtitleRect *rect=r->displayed.rects[n];if(rect->type!=SUBTITLE_BITMAP||!rect->data[0]||!rect->data[1])continue;
            for(int y=0;y<rect->h;y++)for(int x=0;x<rect->w;x++){
                unsigned index=rect->data[0][y*rect->linesize[0]+x];if(index>=(unsigned)rect->nb_colors)continue;
                uint32_t argb=((uint32_t *)rect->data[1])[index];uint8_t mask=255;
                ps_blend(r->canvas,r->width,r->height,rect->x+x,rect->y+y,1,1,&mask,1,(argb<<8)|(255-(argb>>24)));
            }
        }
    }
    r->last_us=us;*ow=r->width;*oh=r->height;return r->canvas;
}

struct PSAudio {
    AVFormatContext *format; AVIOContext *io; AVCodecContext *codec; AVPacket *packet;
    AVFrame *frame; int stream, offset, eof, flushing;
};
static int ps_audio_deny(AVFormatContext *f,AVIOContext **io,const char *url,int flags,AVDictionary **options) {
    (void)f;(void)io;(void)url;(void)flags;(void)options;return AVERROR(EACCES);
}
void ps_audio_close(PSAudio *a) {
    if(!a)return;
    av_frame_free(&a->frame);av_packet_free(&a->packet);avcodec_free_context(&a->codec);
    avformat_close_input(&a->format);if(a->io){av_freep(&a->io->buffer);avio_context_free(&a->io);}free(a);
}
PSAudio *ps_audio_open(void *opaque,PSAudioRead read,PSAudioSeek seek,int *rate,int *channels) {
    PSAudio *a=calloc(1,sizeof(*a));if(!a)return NULL;
    uint8_t *buffer=av_malloc(32768);if(!buffer)goto fail;
    a->io=avio_alloc_context(buffer,32768,0,opaque,read,NULL,seek);if(!a->io){av_free(buffer);goto fail;}
    a->format=avformat_alloc_context();if(!a->format)goto fail;a->format->pb=a->io;a->format->flags|=AVFMT_FLAG_CUSTOM_IO;a->format->io_open=ps_audio_deny;
    if(avformat_open_input(&a->format,NULL,av_find_input_format("ogg"),NULL)<0)goto fail;
    a->stream=-1;for(unsigned i=0;i<a->format->nb_streams;i++)if(a->format->streams[i]->codecpar->codec_id==AV_CODEC_ID_OPUS){a->stream=(int)i;break;}
    if(a->stream<0)goto fail;
    a->codec=avcodec_alloc_context3(avcodec_find_decoder(AV_CODEC_ID_OPUS));if(!a->codec)goto fail;
    if(avcodec_parameters_to_context(a->codec,a->format->streams[a->stream]->codecpar)<0)goto fail;
    a->codec->flags2|=AV_CODEC_FLAG2_SKIP_MANUAL;
    if(avcodec_open2(a->codec,NULL,NULL)<0||a->codec->sample_rate<=0||a->codec->ch_layout.nb_channels<1||a->codec->ch_layout.nb_channels>8)goto fail;
    *rate=a->codec->sample_rate;*channels=a->codec->ch_layout.nb_channels;
    a->packet=av_packet_alloc();a->frame=av_frame_alloc();if(!a->packet||!a->frame)goto fail;return a;
fail:ps_audio_close(a);return NULL;
}
int ps_audio_rewind(PSAudio *a) {
    if(!a||av_seek_frame(a->format,a->stream,0,AVSEEK_FLAG_BACKWARD)<0)return 0;
    avcodec_flush_buffers(a->codec);av_frame_unref(a->frame);a->offset=a->eof=a->flushing=0;return 1;
}
int ps_audio_channel_at(PSAudio *a,int channel) {
    if(!a||channel<0||channel>=a->codec->ch_layout.nb_channels)return -1;
    return av_channel_layout_channel_from_index(&a->codec->ch_layout,channel);
}
int ps_audio_read(PSAudio *a,float **planes,int frames) {
    if(!a||frames<1)return 0;
    int written=0;
    while(written<frames){
        if(a->offset<a->frame->nb_samples){
            int n=a->frame->nb_samples-a->offset;if(n>frames-written)n=frames-written;
            /* Opus emits planar float. Reject any unexpected native decoder format. */
            if(a->frame->format!=AV_SAMPLE_FMT_FLTP)return AVERROR_INVALIDDATA;
            for(int ch=0;ch<a->codec->ch_layout.nb_channels;ch++)memcpy(planes[ch]+written,((float *)a->frame->extended_data[ch])+a->offset,(size_t)n*sizeof(float));
            a->offset+=n;written+=n;continue;
        }
        av_frame_unref(a->frame);a->offset=0;int result=avcodec_receive_frame(a->codec,a->frame);
        if(result==0)continue;
        if(result==AVERROR_EOF)break;
        if(result!=AVERROR(EAGAIN))return result;
        if(a->eof){if(a->flushing)break;avcodec_send_packet(a->codec,NULL);a->flushing=1;continue;}
        for(;;){result=av_read_frame(a->format,a->packet);if(result<0){a->eof=1;break;}if(a->packet->stream_index==a->stream)break;av_packet_unref(a->packet);}
        if(a->eof)continue;
        result=avcodec_send_packet(a->codec,a->packet);av_packet_unref(a->packet);if(result<0)return result;
    }
    return written;
}
