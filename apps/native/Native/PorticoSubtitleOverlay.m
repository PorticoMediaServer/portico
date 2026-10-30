#import "PorticoSubtitleOverlay.h"
#import "PorticoServerTrust.h"
#import "PorticoSubtitleSelection.h"
#import "PorticoSubtitlePayload.h"
#import "PorticoSubtitleClock.h"
#import "PorticoLocalSubtitle.h"
#import <CoreText/CoreText.h>
#import <Libporticosubtitle/PorticoSubtitleCore.h>

typedef NS_ENUM(NSInteger,PSFetchFailure){PSFetchOK,PSFetchTransient,PSFetchDenied};

/* One bounded scoped fetch. No ambient credentials, redirect, cookies or URL cache. */
@interface PSFetch : NSObject<NSURLSessionDataDelegate>
@property(nonatomic,strong) NSURLSession *session;
@property(nonatomic,strong) NSURLSessionDataTask *task;
@property(nonatomic,strong) NSMutableData *bytes;
@property(nonatomic) NSUInteger limit;
@property(nonatomic) PSFetchFailure failure;
@property(nonatomic,copy) void (^done)(NSData *,PSFetchFailure);
- (void)start:(NSURL *)url limit:(NSUInteger)limit done:(void (^)(NSData *,PSFetchFailure))done;
- (void)cancel;
- (void)complete:(PSFetchFailure)failure;
@end
@implementation PSFetch
- (void)start:(NSURL *)url limit:(NSUInteger)limit done:(void (^)(NSData *,PSFetchFailure))done {
 self.limit=limit;self.done=done;self.bytes=[NSMutableData new];
 NSURLSessionConfiguration *c=NSURLSessionConfiguration.ephemeralSessionConfiguration;c.URLCache=nil;c.HTTPCookieStorage=nil;c.URLCredentialStorage=nil;c.HTTPShouldSetCookies=NO;c.timeoutIntervalForRequest=15;c.timeoutIntervalForResource=25;
 self.session=[NSURLSession sessionWithConfiguration:c delegate:self delegateQueue:NSOperationQueue.mainQueue];
 self.task=[self.session dataTaskWithURL:url];[self.task resume];
}
- (void)cancel {self.done=nil;[self.task cancel];[self.session invalidateAndCancel];self.session=nil;self.task=nil;}
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {[PorticoServerTrust handleChallenge:challenge completion:completion];}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completion {self.failure=PSFetchDenied;completion(nil);[self complete:self.failure];}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveResponse:(NSURLResponse *)response completionHandler:(void (^)(NSURLSessionResponseDisposition))completion {
 NSHTTPURLResponse *http=[response isKindOfClass:NSHTTPURLResponse.class]?(id)response:nil;
 if(self.failure!=PSFetchDenied&&http.statusCode>=500&&http.statusCode<=599)self.failure=PSFetchTransient;
 else if(http.statusCode!=200||response.expectedContentLength>(int64_t)self.limit)self.failure=PSFetchDenied;
 completion(self.failure==PSFetchOK?NSURLSessionResponseAllow:NSURLSessionResponseCancel);
 if(self.failure!=PSFetchOK)[self complete:self.failure];
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {
 if(data.length>self.limit-self.bytes.length){self.failure=PSFetchDenied;[task cancel];[self complete:self.failure];return;}[self.bytes appendData:data];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
 PSFetchFailure failure=self.failure;
 if(failure==PSFetchOK&&error){
  // Certificate, trust and transport-policy failures are definitive, not an outage grace period.
  BOOL trust=[error.domain isEqual:NSURLErrorDomain]&&(error.code==NSURLErrorSecureConnectionFailed||error.code==NSURLErrorServerCertificateHasBadDate||error.code==NSURLErrorServerCertificateUntrusted||error.code==NSURLErrorServerCertificateHasUnknownRoot||error.code==NSURLErrorServerCertificateNotYetValid||error.code==NSURLErrorClientCertificateRejected||error.code==NSURLErrorClientCertificateRequired||error.code==NSURLErrorAppTransportSecurityRequiresSecureConnection);
  failure=trust?PSFetchDenied:PSFetchTransient;
 }
 if(failure==PSFetchOK&&!self.bytes.length)failure=PSFetchDenied;
 [self complete:failure];
}
- (void)complete:(PSFetchFailure)failure {
 void (^done)(NSData *,PSFetchFailure)=self.done;NSData *bytes=self.bytes;self.done=nil;
 if(failure==PSFetchOK)[self.session finishTasksAndInvalidate];else [self.session invalidateAndCancel];self.session=nil;self.task=nil;
 if(done)done(failure==PSFetchOK?bytes:nil,failure);
}
- (void)dealloc {[self cancel];}
@end

@implementation PorticoSubtitleOverlay {
 dispatch_queue_t _queue;PSRenderer *_renderer;NSDictionary *_command;NSString *_source;NSUInteger _epoch;
 NSMutableArray<PSFetch *> *_fetches;BOOL _active,_busy,_loading;NSTimeInterval _authorized,_lastFetch;NSUInteger _imageRevision;NSTimer *_retryTimer,*_authorizationTimer;
}
- (instancetype)init {if((self=[super init])){_queue=dispatch_queue_create("tv.getportico.subtitles.render",DISPATCH_QUEUE_SERIAL);_fetches=[NSMutableArray new];}return self;}
- (BOOL)active {return _active;}
- (void)clearRenderer {
 _imageRevision++;_authorized=0;[_authorizationTimer invalidate];_authorizationTimer=nil;
 dispatch_async(_queue,^{ps_close(self->_renderer);self->_renderer=NULL;});if(self.onImage)self.onImage(nil);
}
- (void)invalidate {
 _epoch++;_active=NO;_command=nil;_source=nil;_loading=NO;
 [_retryTimer invalidate];_retryTimer=nil;
 for(PSFetch *f in _fetches)[f cancel];[_fetches removeAllObjects];
 [self clearRenderer];
}
- (void)failed:(NSString *)error transient:(BOOL)transient {
 _loading=NO;[_retryTimer invalidate];_retryTimer=nil;
 NSTimeInterval now=NSProcessInfo.processInfo.systemUptime;
 if(!transient){
  // Fence both pending loads and queued images before clearing a revoked/invalid renderer.
  _epoch++;for(PSFetch *f in _fetches)[f cancel];[_fetches removeAllObjects];[self clearRenderer];_lastFetch=now;
  // A saved VTT reading can take over after a local original/font/companion fails.
  if([NSURL URLWithString:_source].isFileURL)_active=NO;
 }else{
  if(now>=_authorized)[self clearRenderer];
  _lastFetch=now-20;NSUInteger epoch=_epoch;__weak PorticoSubtitleOverlay *weakSelf=self;
  _retryTimer=[NSTimer timerWithTimeInterval:5 repeats:NO block:^(NSTimer *timer){PorticoSubtitleOverlay *s=weakSelf;if(!s||!s->_active||s->_epoch!=epoch)return;s->_retryTimer=nil;[s load];}];[NSRunLoop.mainRunLoop addTimer:_retryTimer forMode:NSRunLoopCommonModes];
 }
 if(self.onStatus)self.onStatus(error);
}
/* The URL must belong to this media grant's resource, including each font and IDX companion. */
- (NSURL *)scoped:(id)value source:(NSString *)source resource:(NSString *)resource {
 if([NSURL URLWithString:source].isFileURL)return PorticoLocalSubtitleURL(value,source,self.allowsLocalFile);
 return PorticoScopedSubtitleURL(value,source,resource);
}
- (NSURL *)companion:(NSDictionary *)render source:(NSString *)source index:(NSURL *)index {
 if(![NSURL URLWithString:source].isFileURL)return PorticoVobSubCompanionURL(render,source,index);
 NSURL *pair=[self scoped:render[@"companionUrl"] source:source resource:nil];
 return [index.pathExtension.lowercaseString isEqual:@"idx"]&&[pair.pathExtension.lowercaseString isEqual:@"sub"]&&[[pair.path stringByDeletingPathExtension]isEqual:[index.path stringByDeletingPathExtension]]?pair:nil;
}
- (void)fetch:(NSURL *)url limit:(NSUInteger)limit epoch:(NSUInteger)epoch done:(void (^)(NSData *))done {
 if(url.isFileURL){
  NSString *source=[_source copy];BOOL (^allowed)(NSURL *)=[self.allowsLocalFile copy];__weak PorticoSubtitleOverlay *weakSelf=self;
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{NSData *data=PorticoReadLocalSubtitle(url,source,limit,allowed);
   dispatch_async(dispatch_get_main_queue(),^{PorticoSubtitleOverlay *s=weakSelf;if(!s||s->_epoch!=epoch)return;if(!data){[s failed:@"subtitle_download_unavailable" transient:NO];return;}done(data);});
  });return;
 }
 PSFetch *f=[PSFetch new];[_fetches addObject:f];__weak PorticoSubtitleOverlay *weakSelf=self;
 [f start:url limit:limit done:^(NSData *data,PSFetchFailure failure){PorticoSubtitleOverlay *s=weakSelf;if(!s||s->_epoch!=epoch)return;
  [s->_fetches removeObject:f];if(!data){[s failed:failure==PSFetchTransient?@"subtitle_connection_failed":@"subtitle_unavailable" transient:failure==PSFetchTransient];return;}done(data);
 }];
}
- (void)loadFonts:(NSArray *)named index:(NSUInteger)index total:(NSUInteger)total fonts:(NSMutableArray *)fonts resource:(NSString *)resource epoch:(NSUInteger)epoch done:(void (^)(NSArray *))done {
 if(_epoch!=epoch||!_active)return;
 if(index==named.count){done(fonts);return;}
 NSDictionary *font=[named[index] isKindOfClass:NSDictionary.class]?named[index]:nil;
 NSString *name=font[@"name"];NSURL *address=[self scoped:font[@"url"] source:_source resource:resource];
 if(!address||![name isKindOfClass:NSString.class]||name.length>160||!name.length||total>=32*1024*1024){[self failed:@"subtitle_font_invalid" transient:NO];return;}
 __weak PorticoSubtitleOverlay *weakSelf=self;
 [self fetch:address limit:MIN(8*1024*1024,32*1024*1024-total) epoch:epoch done:^(NSData *bytes){
  PorticoSubtitleOverlay *view=weakSelf;if(!view||view->_epoch!=epoch)return;
  [fonts addObject:@{@"name":name,@"data":bytes}];
  [view loadFonts:named index:index+1 total:total+bytes.length fonts:fonts resource:resource epoch:epoch done:done];
 }];
}
- (void)apply:(NSDictionary *)command source:(NSString *)source {
 NSDictionary *render=[command[@"render"] isKindOfClass:NSDictionary.class]?command[@"render"]:nil;
 NSString *kind=render[@"kind"];
 if(![@[@"ass",@"pgs",@"vobsub",@"dvb"] containsObject:kind]){if(_active)[self invalidate];return;}
 if(!PorticoSubtitleOverlayOffsetUS(command)){[self invalidate];if(self.onStatus)self.onStatus(@"subtitle_source_invalid");return;}
 NSURL *url=[self scoped:render[@"url"] source:source resource:nil];
 if(!url||([kind isEqual:@"vobsub"]&&![self companion:render source:source index:url])){[self invalidate];if(self.onStatus)self.onStatus(@"subtitle_source_invalid");return;}
 if([_command isEqual:command]&&[_source isEqual:source])return;
 [self invalidate];_active=YES;_command=[command copy];_source=[source copy];[self load];
}
- (void)load {
 if(_loading||!_active)return;[_retryTimer invalidate];_retryTimer=nil;_loading=YES;_lastFetch=NSProcessInfo.processInfo.systemUptime;
 NSUInteger epoch=_epoch;NSDictionary *render=_command[@"render"];NSString *kind=render[@"kind"];
 NSDictionary *style=[_command[@"style"] isKindOfClass:NSDictionary.class]?_command[@"style"]:@{};
 BOOL overrideStyle=[style[@"keepStyling"] isEqual:@NO]||[style[@"useSystemCaptionStyle"] isEqual:@YES];
 UILabel *appearance=[UILabel new];[PorticoSubtitleSelection applyCaptionAppearanceToLabel:appearance baseSize:26 style:style italic:NO];
 CGFloat red=1,green=1,blue=1,alpha=1;[appearance.textColor getRed:&red green:&green blue:&blue alpha:&alpha];
 uint32_t foreground=((uint32_t)lround(red*255)<<24)|((uint32_t)lround(green*255)<<16)|((uint32_t)lround(blue*255)<<8)|(uint32_t)lround((1-alpha)*255);
 red=green=blue=0;alpha=0;[appearance.backgroundColor getRed:&red green:&green blue:&blue alpha:&alpha];
 uint32_t background=((uint32_t)lround(red*255)<<24)|((uint32_t)lround(green*255)<<16)|((uint32_t)lround(blue*255)<<8)|(uint32_t)lround((1-alpha)*255);
 NSString *fontName=appearance.font.familyName;double fontSize=appearance.font.pointSize;
 NSURL *url=[self scoped:render[@"url"] source:_source resource:nil];
 NSString *resource=[url.path stringByDeletingLastPathComponent];
 __weak PorticoSubtitleOverlay *weakSelf=self;
 [self fetch:url limit:64*1024*1024 epoch:epoch done:^(NSData *data){
  PorticoSubtitleOverlay *s=weakSelf;if(!s)return;
  void (^finish)(NSData *,NSArray *)=^(NSData *pair,NSArray *fonts){
   PorticoSubtitleOverlay *owner=weakSelf;if(!owner||owner->_epoch!=epoch)return;
   if(data.length+pair.length>64*1024*1024){[owner failed:@"subtitle_size_exceeded" transient:NO];return;}
   dispatch_async(owner->_queue,^{
    BOOL fontsValid=YES;PSFont attached[16]={0};for(NSUInteger i=0;i<fonts.count;i++){
     CFArrayRef descriptors=CTFontManagerCreateFontDescriptorsFromData((__bridge CFDataRef)fonts[i][@"data"]);BOOL valid=descriptors&&CFArrayGetCount(descriptors)>0;if(descriptors)CFRelease(descriptors);
     if(!valid){fontsValid=NO;break;}attached[i].name=[fonts[i][@"name"] UTF8String];attached[i].bytes=[fonts[i][@"data"] bytes];attached[i].size=[fonts[i][@"data"] length];}
    char error[256]={0};PSRenderer *next=fontsValid?ps_open(kind.UTF8String,data.bytes,data.length,pair.bytes,pair.length,attached,fonts.count,error,sizeof(error)):NULL;
    if(next&&overrideStyle){BOOL box=[@[@"translucent",@"opaque"] containsObject:style[@"background"]];PSStyle override={fontName.UTF8String,fontSize,foreground,background,box?3:1,[style[@"position"] isEqual:@"top"]?8:2,[style[@"background"] isEqual:@"none"]?0:2,[style[@"background"] isEqual:@"shadow"]?2:0,[style[@"position"] isEqual:@"raised"]?.20:.08};ps_style(next,&override);}
    NSString *failure=next?@"":fontsValid?@"subtitle_decode_failed":@"subtitle_font_invalid";
    dispatch_async(dispatch_get_main_queue(),^{PorticoSubtitleOverlay *view=weakSelf;if(!view||view->_epoch!=epoch){dispatch_async(owner->_queue,^{ps_close(next);});return;}
     if(!next){[view failed:failure transient:NO];return;}
     view->_loading=NO;view->_imageRevision++;view->_authorized=[NSURL URLWithString:view->_source].isFileURL?DBL_MAX:NSProcessInfo.processInfo.systemUptime+40;
     [view->_authorizationTimer invalidate];
     if(![NSURL URLWithString:view->_source].isFileURL){view->_authorizationTimer=[NSTimer timerWithTimeInterval:40 repeats:NO block:^(NSTimer *timer){PorticoSubtitleOverlay *s=weakSelf;if(s&&s->_epoch==epoch&&NSProcessInfo.processInfo.systemUptime>=s->_authorized)[s clearRenderer];}];[NSRunLoop.mainRunLoop addTimer:view->_authorizationTimer forMode:NSRunLoopCommonModes];}
     dispatch_async(view->_queue,^{ps_close(view->_renderer);view->_renderer=next;});
     if(view.onStatus)view.onStatus(failure);
    });
   });
  };
  if([kind isEqual:@"vobsub"]){
   NSURL *pair=[s companion:render source:s->_source index:url];if(!pair){[s failed:@"subtitle_source_invalid" transient:NO];return;}
   [s fetch:pair limit:64*1024*1024-data.length epoch:epoch done:^(NSData *bytes){finish(bytes,@[]);}];return;
  }
  if(render[@"fonts"]&&![render[@"fonts"] isKindOfClass:NSArray.class]){[s failed:@"subtitle_font_invalid" transient:NO];return;}
  NSArray *named=[render[@"fonts"] isKindOfClass:NSArray.class]?render[@"fonts"]:@[];
  if(named.count>16){[s failed:@"subtitle_font_invalid" transient:NO];return;}
  [s loadFonts:named index:0 total:0 fonts:[NSMutableArray new] resource:resource epoch:epoch done:^(NSArray *fonts){finish(nil,fonts);}];
 }];
}
- (void)renderAtSourceTime:(double)seconds size:(CGSize)pixels {
 if(!_active||!isfinite(seconds)||seconds<0)return;
 if(![NSURL URLWithString:_source].isFileURL&&NSProcessInfo.processInfo.systemUptime-_lastFetch>=25)[self load];
 if(NSProcessInfo.processInfo.systemUptime>=_authorized){if(_authorized>0)[self clearRenderer];else if(self.onImage)self.onImage(nil);return;}
 if(_busy)return;int width=(int)llround(pixels.width),height=(int)llround(pixels.height);
 if(width<1||height<1)return;
 double scale=MIN(1,MIN(4096.0/width,2304.0/height));width=(int)llround(width*scale);height=(int)llround(height*scale);
 NSNumber *offset=PorticoSubtitleOverlayOffsetUS(_command);
 if(!offset){[self invalidate];if(self.onStatus)self.onStatus(@"subtitle_source_invalid");return;}
 int64_t us=(int64_t)llround(seconds*1000000-offset.doubleValue);
 if(us<0){if(self.onImage)self.onImage(nil);return;}
 NSUInteger epoch=_epoch,imageRevision=_imageRevision;_busy=YES;
 __weak PorticoSubtitleOverlay *weakSelf=self;
 dispatch_async(_queue,^{
  int w=0,h=0,changed=0;const uint8_t *rgba=ps_render(self->_renderer,us,width,height,&w,&h,&changed);
  UIImage *image=nil;if(rgba&&changed&&w>0&&h>0){
   NSData *copy=[NSData dataWithBytes:rgba length:(NSUInteger)w*h*4];CGDataProviderRef provider=CGDataProviderCreateWithCFData((__bridge CFDataRef)copy);
   CGColorSpaceRef color=CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
   CGImageRef cg=CGImageCreate(w,h,8,32,w*4,color,kCGImageAlphaPremultipliedLast|kCGBitmapByteOrder32Big,provider,NULL,NO,kCGRenderingIntentDefault);
   if(cg){image=[UIImage imageWithCGImage:cg];CGImageRelease(cg);}CGColorSpaceRelease(color);CGDataProviderRelease(provider);
  }
  dispatch_async(dispatch_get_main_queue(),^{PorticoSubtitleOverlay *s=weakSelf;if(!s)return;s->_busy=NO;if(s->_active&&s->_epoch==epoch&&s->_imageRevision==imageRevision&&NSProcessInfo.processInfo.systemUptime<s->_authorized&&changed&&s.onImage)s.onImage(image);});
 });
}
- (void)dealloc {[_retryTimer invalidate];[_authorizationTimer invalidate];for(PSFetch *f in _fetches)[f cancel];PSRenderer *r=_renderer;dispatch_async(_queue,^{ps_close(r);});}
@end
