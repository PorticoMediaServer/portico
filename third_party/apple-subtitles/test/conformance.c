#include "../core/PorticoSubtitleCore.h"
#include <ass/ass.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dirent.h>
#include <libavutil/intreadwrite.h>
#include <libavformat/avformat.h>
#if defined(__linux__)
#include <dlfcn.h>
#endif

static void *read_file(const char *path,size_t *size){FILE *f=fopen(path,"rb");if(!f)return NULL;fseek(f,0,SEEK_END);long n=ftell(f);rewind(f);if(n<1){fclose(f);return NULL;}void *data=malloc((size_t)n);*size=fread(data,1,(size_t)n,f);fclose(f);return data;}
static int companion_files(void){const char *path=getenv("TMPDIR");DIR *dir=path?opendir(path):NULL;if(!dir)return -1;int count=0;struct dirent *entry;while((entry=readdir(dir)))if(!strncmp(entry->d_name,"portico-vobsub-",15))count++;closedir(dir);return count;}
/* Independent scalar source-over reference. It deliberately doesn't call ps_blend. */
static void reference(uint8_t *canvas,int width,int height,ASS_Image *image){
 for(ASS_Image *p=image;p;p=p->next)for(int y=0;y<p->h;y++)for(int x=0;x<p->w;x++){
  int dx=p->dst_x+x,dy=p->dst_y+y;if(dx<0||dy<0||dx>=width||dy>=height)continue;
  unsigned alpha=(p->bitmap[y*p->stride+x]*(255-(p->color&255))+127)/255;
  uint8_t *out=canvas+((size_t)dy*width+dx)*4;
  for(int ch=0;ch<3;ch++){unsigned component=(p->color>>(24-ch*8))&255;out[ch]=(uint8_t)((component*alpha+out[ch]*(255-alpha)+127)/255);}
  out[3]=(uint8_t)(alpha+(out[3]*(255-alpha)+127)/255);
 }
}
static const char script[]=
"[Script Info]\nScriptType: v4.00+\nPlayResX: 640\nPlayResY: 360\nScaledBorderAndShadow: yes\n"
"[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n"
"Style: Default,Manrope,26,&H00FFFFFF,&H0000FFFF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,2,1,2,12,12,12,1\n"
"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"
"Dialogue: 0,0:00:00.00,0:00:04.00,Default,,0,0,0,,{\\move(100,100,500,200)\\fad(300,500)\\t(0,3000,\\frz20)}Animated ASS\n"
"Dialogue: 1,0:00:00.50,0:00:03.50,Default,,0,0,0,,{\\an7\\pos(40,20)\\clip(50,10,600,280)\\blur1}Clipped {\\k40}karaoke {\\k70}line\n"
"Dialogue: 2,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\an7\\pos(280,240)\\p1\\1c&H0000FF&}m 0 0 l 40 0 40 30 0 30\n";

static int ass_test(const char *font_path){
 size_t font_size=0;uint8_t *font=read_file(font_path,&font_size);if(!font)return 0;PSFont attached={"Manrope.ttf",font,font_size};char error[256]={0};
 PSRenderer *actual=ps_open("ass",(const uint8_t *)script,strlen(script),NULL,0,&attached,1,error,sizeof(error));
 ASS_Library *library=ass_library_init();ass_add_font(library,"Manrope.ttf",(char *)font,(int)font_size);ASS_Renderer *renderer=ass_renderer_init(library);ass_set_fonts(renderer,NULL,"sans-serif",ASS_FONTPROVIDER_AUTODETECT,NULL,1);
 ASS_Track *track=ass_read_memory(library,(char *)script,strlen(script),NULL);int ok=actual&&track;
 const long times[]={0,250,500,1000,1250,2000,2750,3500,3999,4100,1000};
 for(int geometry=0;ok&&geometry<2;geometry++){int w=geometry?320:640,h=geometry?180:360;ass_set_frame_size(renderer,w,h);ass_set_storage_size(renderer,0,0);
  for(size_t i=0;i<sizeof(times)/sizeof(times[0]);i++){
   int changed=0;ASS_Image *images=ass_render_frame(renderer,track,times[i],&changed);uint8_t *expected=calloc((size_t)w*h,4);reference(expected,w,h,images);
   int aw=0,ah=0;const uint8_t *pixels=ps_render(actual,times[i]*1000,w,h,&aw,&ah,&changed);
   if(!pixels||aw!=w||ah!=h||memcmp(pixels,expected,(size_t)w*h*4)){fprintf(stderr,"ASS pixel mismatch: %dx%d at %ldms\n",w,h,times[i]);ok=0;}free(expected);
  }
 }
 ps_close(actual);ass_free_track(track);ass_renderer_done(renderer);ass_library_done(library);free(font);printf("ASS attached-font animated/clipped/seek/resize reference pixels: %s\n",ok?"PASS":"FAIL");return ok;
}
static int pgs_test(const char *path){
 size_t size=0;uint8_t *bytes=read_file(path,&size);char error[256]={0};PSRenderer *r=ps_open("pgs",bytes,size,NULL,0,NULL,0,error,sizeof(error));free(bytes);if(!r){fprintf(stderr,"PGS open: %s\n",error);return 0;}
 int w=0,h=0,changed=0;const uint8_t *p=ps_render(r,1500000,640,360,&w,&h,&changed);int ok=p&&w==320&&h==180;
 if(ok){size_t at=((size_t)20*w+10)*4;ok=p[at]>200&&p[at+1]<40&&p[at+2]<40&&p[at+3]==255&&p[0]==0;}
 p=ps_render(r,2500000,640,360,&w,&h,&changed);if(!p)ok=0;else for(int i=0;i<w*h*4;i++)if(p[i])ok=0;
 p=ps_render(r,1500000,640,360,&w,&h,&changed);if(!p||p[((size_t)20*w+10)*4+3]!=255)ok=0;
 ps_close(r);printf("PGS intrinsic canvas/palette/timing/clear/backward-seek: %s\n",ok?"PASS":"FAIL");return ok;
}
static int bitmap_test(const char *kind,const char *path,const char *pair_path){
 int files=companion_files();size_t size=0,pair_size=0;uint8_t *bytes=read_file(path,&size),*pair=pair_path?read_file(pair_path,&pair_size):NULL;char error[256]={0};PSRenderer *r=ps_open(kind,bytes,size,pair,pair_size,NULL,0,error,sizeof(error));free(bytes);free(pair);if(!r){fprintf(stderr,"%s open: %s\n",kind,error);return 0;}
 int w=0,h=0,changed=0;const uint8_t *p=ps_render(r,1500000,640,360,&w,&h,&changed);int ok=p&&w>0&&h>0;unsigned visible=0;if(p)for(int i=0;i<w*h;i++)if(p[i*4+3])visible++;
 ok=ok&&visible==4&&files>=0&&companion_files()==files;
 p=ps_render(r,2500000,640,360,&w,&h,&changed);if(!p)ok=0;else for(int i=0;i<w*h*4;i++)if(p[i])ok=0;
 p=ps_render(r,1500000,640,360,&w,&h,&changed);unsigned again=0;if(p)for(int i=0;i<w*h;i++)if(p[i*4+3])again++;ok=ok&&again==4;
 ps_close(r);ok=ok&&companion_files()==files;
 printf("%s intrinsic canvas %dx%d, authored pixels=%u (expected4), clear/seek/private-file cleanup: %s\n",kind,w,h,visible,ok?"PASS":"FAIL");return ok;
}
typedef struct {const uint8_t *data;size_t size;int64_t at;} Input;
static int input_read(void *opaque,uint8_t *p,int size){Input *i=opaque;if(i->at>=(int64_t)i->size)return AVERROR_EOF;size_t n=i->size-(size_t)i->at;if(n>(size_t)size)n=(size_t)size;memcpy(p,i->data+i->at,n);i->at+=(int64_t)n;return (int)n;}
static int64_t input_seek(void *opaque,int64_t offset,int whence){Input *i=opaque;if(whence&AVSEEK_SIZE)return (int64_t)i->size;whence&=~AVSEEK_FORCE;int64_t base=whence==SEEK_SET?0:whence==SEEK_CUR?i->at:whence==SEEK_END?(int64_t)i->size:-1;if(base<0||offset < -base||offset>(int64_t)i->size-base)return -1;return i->at=base+offset;}
static int opus_test(const char *path){
 Input input={0};input.data=read_file(path,&input.size);int rate=0,channels=0;PSAudio *audio=ps_audio_open(&input,input_read,input_seek,&rate,&channels);int ok=audio&&rate==48000&&channels==2;
 float *planes[2]={calloc(4096,sizeof(float)),calloc(4096,sizeof(float))};float first[2][512];long frames=0;
 if(ok){int got=ps_audio_read(audio,planes,512);ok=got==512;for(int c=0;c<2;c++)memcpy(first[c],planes[c],sizeof(first[c]));frames=got;int n;while((n=ps_audio_read(audio,planes,4096))>0)frames+=n;ok=ok&&n==0&&frames>=48000&&frames<=50000;
  ok=ok&&ps_audio_rewind(audio);got=ps_audio_read(audio,planes,512);for(int c=0;c<2;c++)ok=ok&&got==512&&!memcmp(first[c],planes[c],sizeof(first[c]));}
 printf("Opus raw frames and deterministic rewind (%ld frames): %s\n",frames,ok?"PASS":"FAIL");ps_audio_close(audio);free((void *)input.data);free(planes[0]);free(planes[1]);return ok;
}
int main(int argc,char **argv){if(argc!=7)return 2;setvbuf(stdout,NULL,_IONBF,0);int ok=ass_test(argv[1]);ok=pgs_test(argv[2])&&ok;ok=opus_test(argv[3])&&ok;ok=bitmap_test("vobsub",argv[4],argv[5])&&ok;ok=bitmap_test("dvb",argv[6],NULL)&&ok;
 char error[64];uint8_t bad[]={0,1,2};PSRenderer *invalid=ps_open("vobsub",bad,sizeof(bad),NULL,0,NULL,0,error,sizeof(error));if(invalid){ok=0;ps_close(invalid);}printf("VobSub missing-companion rejection: %s\n",invalid?"FAIL":"PASS");
#if defined(__linux__)
 /* The harness is the last Fontconfig client. Release its process-global cache for LSan. */
 void (*fontconfig_done)(void)=(void (*)(void))dlsym(RTLD_DEFAULT,"FcFini");
 if(fontconfig_done)fontconfig_done();else ok=0;
#endif
 return ok?0:1;}
