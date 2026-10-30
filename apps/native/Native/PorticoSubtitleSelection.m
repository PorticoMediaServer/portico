#import "PorticoSubtitleSelection.h"
#import "PorticoLocalSubtitle.h"
#import "PorticoServerTrust.h"
#import <Foundation/Foundation.h>
#import <MediaAccessibility/MediaAccessibility.h>
#import <CoreText/CoreText.h>

static const NSUInteger SubtitleByteLimit=8*1024*1024;
static BOOL DecimalUS(id value,int64_t maximum,BOOL signedValue,int64_t *out) {
 if(![value isKindOfClass:NSString.class]||![value length]||[value length]>19)return NO;
 NSString *s=value;BOOL negative=[s hasPrefix:@"-"];if(negative&&!signedValue)return NO;
 NSUInteger start=negative?1:0;if(start==s.length||(s.length-start>1&&[s characterAtIndex:start]=='0'))return NO;
 int64_t n=0;for(NSUInteger i=start;i<s.length;i++){unichar c=[s characterAtIndex:i];if(c<'0'||c>'9'||n>(maximum-(c-'0'))/10)return NO;n=n*10+c-'0';}
 if(negative&&!n)return NO;if(out)*out=negative?-n:n;return YES;
}
static BOOL PositiveInteger(id value){if(![value isKindOfClass:NSNumber.class]||CFGetTypeID((__bridge CFTypeRef)value)==CFBooleanGetTypeID())return NO;double v=[value doubleValue];return isfinite(v)&&v>=1&&v<=9007199254740991.0&&floor(v)==v;}
static BOOL PlainText(id value){
 if(![value isKindOfClass:NSString.class]||![value length]||[value lengthOfBytesUsingEncoding:NSUTF8StringEncoding]>8192||[value containsString:@"{\\"])return NO;
 for(NSUInteger i=0;i<[value length];i++){unichar c=[value characterAtIndex:i];if((c<32&&c!=9&&c!=10)||c==127)return NO;}
 return YES;
}
@interface PorticoSubtitleSelection ()<NSURLSessionDataDelegate>
@end
@implementation PorticoSubtitleSelection {
 NSDictionary *_command;__weak AVPlayerItem *_item;NSArray<NSDictionary *> *_cues;NSArray<NSNumber *> *_prefixEnds;
 NSURLSession *_session;NSURLSessionDataTask *_task;NSMutableData *_bytes;NSTimer *_timer;
 NSDictionary *_displayCue;BOOL _italic,_top;NSString *_text;NSURL *_url;int64_t _offset;NSInteger _revision;BOOL _failed;BOOL _transientFailure;BOOL _legibleRequested;BOOL _nativeSelected;NSString *_nativeName;NSString *_nativeLanguage;NSString *_nativeTitle;
 NSTimeInterval _authorizedUntil,_lastRequest;CMTime _time;
 // The validator of the document held in _cues. It is sent by hand on every reload, so the
 // server can answer "still authorised, unchanged" with a header instead of the whole track.
 NSString *_etag,*_responseEtag;BOOL _notModified;
 // A download's subtitle file on this device: read once, never re-checked with a server.
 BOOL _local;NSInteger _localEpoch,_selectionEpoch;
}
+ (void)applyCaptionAppearanceToLabel:(UILabel *)label baseSize:(CGFloat)base style:(NSDictionary *)style italic:(BOOL)italic {
 NSDictionary *scales=@{@"small":@0.8,@"medium":@1,@"large":@1.3,@"extra-large":@1.6};
 CGFloat scale=[style[@"scale"] isKindOfClass:NSNumber.class]?[style[@"scale"] doubleValue]:[scales[style[@"size"]?:@"medium"] doubleValue];if(!isfinite(scale)||scale<0.5||scale>3)scale=1;
 BOOL system=[style[@"useSystemCaptionStyle"] isEqual:@YES];MACaptionAppearanceBehavior behavior=kMACaptionAppearanceBehaviorUseContentIfAvailable;
 CGFloat relative=system?MACaptionAppearanceGetRelativeCharacterSize(kMACaptionAppearanceDomainUser,&behavior):1;
 if(!isfinite(relative)||relative<=0)relative=1;CGFloat size=base*scale*MAX(.5,MIN(3,relative));
 NSString *family=style[@"font"];UIFont *font=italic?[UIFont italicSystemFontOfSize:size]:[UIFont systemFontOfSize:size weight:UIFontWeightMedium];
 if([family isEqual:@"serif"]){UIFontDescriptor *d=[font.fontDescriptor fontDescriptorWithDesign:UIFontDescriptorSystemDesignSerif];if(d)font=[UIFont fontWithDescriptor:d size:size];}
 else if([family isEqual:@"mono"])font=[UIFont monospacedSystemFontOfSize:size weight:UIFontWeightMedium];
 else if([family isEqual:@"rounded"]){UIFontDescriptor *d=[font.fontDescriptor fontDescriptorWithDesign:UIFontDescriptorSystemDesignRounded];if(d)font=[UIFont fontWithDescriptor:d size:size];}
 NSDictionary *colors=@{@"white":UIColor.whiteColor,@"yellow":UIColor.yellowColor,@"cyan":UIColor.cyanColor,@"green":UIColor.greenColor,@"pink":[UIColor colorWithRed:1 green:.5 blue:.75 alpha:1]};
 UIColor *text=colors[style[@"color"]?:@"white"]?:UIColor.whiteColor;
 NSString *background=[style[@"background"] isKindOfClass:NSString.class]?style[@"background"]:@"translucent";
 UIColor *box=([@[@"none",@"outline",@"shadow"] containsObject:background])?UIColor.clearColor:[UIColor colorWithWhite:0 alpha:[background isEqual:@"opaque"]?1:.7];
 if(system){
  CTFontDescriptorRef descriptor=MACaptionAppearanceCopyFontDescriptorForStyle(kMACaptionAppearanceDomainUser,&behavior,kMACaptionAppearanceFontStyleDefault);
  if(descriptor){if(behavior==kMACaptionAppearanceBehaviorUseValue)font=[UIFont fontWithDescriptor:(__bridge UIFontDescriptor *)descriptor size:size];CFRelease(descriptor);}
  behavior=kMACaptionAppearanceBehaviorUseContentIfAvailable;CGColorRef foreground=MACaptionAppearanceCopyForegroundColor(kMACaptionAppearanceDomainUser,&behavior);
  if(foreground){if(behavior==kMACaptionAppearanceBehaviorUseValue)text=[UIColor colorWithCGColor:foreground];CGColorRelease(foreground);}
  behavior=kMACaptionAppearanceBehaviorUseContentIfAvailable;CGColorRef bg=MACaptionAppearanceCopyBackgroundColor(kMACaptionAppearanceDomainUser,&behavior);
  if(bg){if(behavior==kMACaptionAppearanceBehaviorUseValue){MACaptionAppearanceBehavior opacityBehavior;CGFloat opacity=MACaptionAppearanceGetBackgroundOpacity(kMACaptionAppearanceDomainUser,&opacityBehavior);box=[[UIColor colorWithCGColor:bg] colorWithAlphaComponent:isfinite(opacity)?MAX(0,MIN(1,opacity)):.7];}CGColorRelease(bg);}
 }
 label.font=font;label.textColor=text;label.backgroundColor=box;
 BOOL outline=[background isEqual:@"outline"],shadow=[background isEqual:@"shadow"]||CGColorGetAlpha(box.CGColor)<.3;
 label.layer.shadowColor=UIColor.blackColor.CGColor;label.layer.shadowOffset=CGSizeMake(0,outline?0:2);label.layer.shadowRadius=shadow?MAX(2,size*.08):0;label.layer.shadowOpacity=shadow?1:0;
 if(outline&&label.text.length)label.attributedText=[[NSAttributedString alloc]initWithString:label.text attributes:@{NSFontAttributeName:font,NSForegroundColorAttributeName:text,NSStrokeColorAttributeName:UIColor.blackColor,NSStrokeWidthAttributeName:@-3}];
 else if(label.text.length)label.attributedText=[[NSAttributedString alloc]initWithString:label.text attributes:@{NSFontAttributeName:font,NSForegroundColorAttributeName:text}];
}

+ (NSString *)normalizedTitle:(NSString *)title {
 NSArray *parts=[(title?:@"").lowercaseString componentsSeparatedByCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
 NSMutableArray *words=[NSMutableArray new];for(NSString *part in parts)if(part.length)[words addObject:part];return [words componentsJoinedByString:@" "];
}
+ (AVMediaSelectionOption *)option:(NSArray<AVMediaSelectionOption *> *)options language:(NSString *)language title:(NSString *)title {
 NSString *wanted=[[language lowercaseString] componentsSeparatedByCharactersInSet:[NSCharacterSet characterSetWithCharactersInString:@"-_"]].firstObject;
 NSMutableArray<AVMediaSelectionOption *> *matches=[NSMutableArray new];
 for(AVMediaSelectionOption *option in options){
  if([option hasMediaCharacteristic:AVMediaCharacteristicContainsOnlyForcedSubtitles])continue;
  NSString *tag=[option.extendedLanguageTag?:option.locale.languageCode?:@"" lowercaseString];
  NSString *base=[tag componentsSeparatedByCharactersInSet:[NSCharacterSet characterSetWithCharactersInString:@"-_"]].firstObject;
  if(wanted.length&&[base isEqual:wanted])[matches addObject:option];
 }
 NSString *requested=[self normalizedTitle:title];NSMutableArray *exact=[NSMutableArray new],*fuzzy=[NSMutableArray new];
 if(requested.length)for(AVMediaSelectionOption *option in matches){
  NSString *name=[self normalizedTitle:option.displayName];
  if([name isEqual:requested])[exact addObject:option];
  else if(name.length&&([name containsString:requested]||[requested containsString:name]))[fuzzy addObject:option];
 }
 if(exact.count)return exact.count==1?exact.firstObject:nil;
 if(matches.count==1)return matches.firstObject;
 return fuzzy.count==1?fuzzy.firstObject:nil; // ambiguous: keep drawing it ourselves
}
- (BOOL)overlayActive {return _command!=nil&&!_nativeSelected;}
- (BOOL)requestPending {return _task!=nil;}
- (NSTimeInterval)now {return self.clock?self.clock():NSProcessInfo.processInfo.systemUptime;}
/** Once a second: expired cues go, and a due recheck starts. */
- (void)timerTick {NSTimeInterval now=[self now];if(now>_authorizedUntil){[self setText:@""];}if(!_task&&now-_lastRequest>=25)[self fetch];}
- (void)setText:(NSString *)text{NSDictionary *cue=@{@"text":text,@"italic":@(_italic&&text.length>0),@"top":@(_top&&text.length>0)};if([_displayCue isEqual:cue])return;_displayCue=cue;_text=[text copy];if(self.onText)self.onText(cue);}
- (void)status:(NSString *)error{if(self.onStatus)self.onStatus(error?:@"");}
- (void)resetNativeSelection {
 _selectionEpoch++;
 if(_nativeSelected&&_item){AVMediaSelectionGroup *group=[_item.asset mediaSelectionGroupForMediaCharacteristic:AVMediaCharacteristicLegible];if(group)[_item selectMediaOption:nil inMediaSelectionGroup:group];}
 _legibleRequested=NO;_nativeSelected=NO;
}
- (void)invalidate {
 [self resetNativeSelection];_local=NO;_localEpoch++;
 [_task cancel];_task=nil;[_session invalidateAndCancel];_session=nil;[_timer invalidate];_timer=nil;
 _command=nil;_item=nil;_cues=nil;_prefixEnds=nil;_bytes=nil;_url=nil;_etag=nil;_responseEtag=nil;_notModified=NO;_revision=0;_offset=0;_authorizedUntil=0;_lastRequest=0;_legibleRequested=NO;_nativeSelected=NO;_nativeName=nil;_nativeLanguage=nil;_nativeTitle=nil;
 [self setText:@""];
}
- (void)disableEmbedded:(AVPlayerItem *)item {
 if(!item||_legibleRequested)return;_legibleRequested=YES;NSInteger epoch=++_selectionEpoch;__weak PorticoSubtitleSelection *weakSelf=self;
 [item.asset loadValuesAsynchronouslyForKeys:@[@"availableMediaCharacteristicsWithMediaSelectionOptions"] completionHandler:^{dispatch_async(dispatch_get_main_queue(),^{
  PorticoSubtitleSelection *self=weakSelf;if(!self||self->_item!=item||self->_selectionEpoch!=epoch)return;
  AVMediaSelectionGroup *group=[item.asset mediaSelectionGroupForMediaCharacteristic:AVMediaCharacteristicLegible];if(group){AVMediaSelectionOption *selected=nil;for(AVMediaSelectionOption *option in group.options){if(self->_nativeName.length&&[option.displayName isEqual:self->_nativeName]){selected=option;break;}}if(!selected&&self->_nativeLanguage.length)selected=[PorticoSubtitleSelection option:group.options language:self->_nativeLanguage title:self->_nativeTitle];[item selectMediaOption:selected inMediaSelectionGroup:group];self->_nativeSelected=selected!=nil;[self tick:self->_time];}
 });}];
}
- (void)apply:(NSDictionary *)command item:(AVPlayerItem *)item source:(NSString *)source {
 if(![command isKindOfClass:NSDictionary.class]||![command[@"mode"] isEqual:@"track"]){
  if(_command||_cues||_item!=item){[self invalidate];_item=item;}
  [self disableEmbedded:item];[self setText:@""];return;
 }
 int64_t offset=0;
 if(!PositiveInteger(command[@"revision"])||!PositiveInteger(command[@"resourceRevision"])||!DecimalUS(command[@"offsetUs"],600000000,YES,&offset)||![command[@"documentUrl"] isKindOfClass:NSString.class]||![command[@"resourceId"] isKindOfClass:NSString.class]){[self invalidate];[self status:@"subtitle_command_invalid"];return;}
 NSURL *url=[NSURL URLWithString:command[@"documentUrl"]],*media=[NSURL URLWithString:source];
 // A downloaded title: its subtitles are a WebVTT file beside it, inside the app's Downloads
 // folder. Only a download may name one, and only there.
 if(url.isFileURL){
  if(!PorticoLocalSubtitleURL(url.absoluteString,source,self.allowsLocalFile)||![url.pathExtension.lowercaseString isEqual:@"vtt"]){[self invalidate];[self status:@"subtitle_source_invalid"];return;}
  // offsetUs may describe a correction already baked into this saved VTT. Only the
  // explicit current-session delta is applied here; older commands default to zero.
  if(!DecimalUS(command[@"localSessionOffsetUs"]?:@"0",600000000,YES,&offset)){[self invalidate];[self status:@"subtitle_command_invalid"];return;}
  if(_item==item&&_local&&[_command[@"binding"]isEqual:command[@"binding"]]&&[command[@"revision"]integerValue]<_revision)return;
  if(_item==item&&_local&&[_url isEqual:url]&&[_command[@"binding"] isEqual:command[@"binding"]]){_command=[command copy];_revision=[command[@"revision"] integerValue];_offset=offset;[self tick:_time];return;}
  [self invalidate];_item=item;_url=url;_local=YES;_command=[command copy];_revision=[command[@"revision"] integerValue];_offset=offset;
  [self disableEmbedded:item];[self readLocal:url source:source];return;
 }
 NSArray *mediaParts=media.path.pathComponents;
 NSString *grant=mediaParts.count>=4?mediaParts[3]:@"";
 // Subtitle files have a place of their own beside the media: /v1/media/subtitles/<grant>/<resource>/<revision>.
 NSString *expected=[NSString stringWithFormat:@"/v1/media/subtitles/%@/%@/%@",grant,command[@"resourceId"],command[@"resourceRevision"]];
 NSRegularExpression *route=[NSRegularExpression regularExpressionWithPattern:@"^/v1/media/subtitles/[A-Za-z0-9_-]{1,128}/[A-Za-z0-9_-]{1,128}/[1-9][0-9]*$" options:0 error:nil];
 // The document travels the way the media does: the same scheme, host and port, under the media's
 // own grant. A server reached over plain HTTP serves its subtitles over plain HTTP too.
 if(!url||!media||(![url.scheme isEqual:@"https"]&&![url.scheme isEqual:@"http"])||![url.scheme isEqual:media.scheme]||![url.host.lowercaseString isEqual:media.host.lowercaseString]||![(url.port?:@0) isEqual:(media.port?:@0)]||url.user||url.password||url.query||url.fragment||![url.path isEqual:expected]||![route firstMatchInString:url.path options:0 range:NSMakeRange(0,url.path.length)]){
  [self invalidate];[self status:@"subtitle_source_invalid"];return;
 }
 if(_item==item&&[_command[@"binding"] isEqual:command[@"binding"]]&&[command[@"revision"] integerValue]<_revision)return;
 BOOL refreshRequested=![_command[@"refreshRevision"] isEqual:command[@"refreshRevision"]];
 BOOL sameResource=_item==item&&[_url isEqual:url]&&[_command[@"binding"] isEqual:command[@"binding"]];
 if(!sameResource){[self invalidate];_item=item;_url=url;}
 _offset=offset;
 NSString *nativeName=[command[@"manifestName"] isKindOfClass:NSString.class]?command[@"manifestName"]:nil;
 if(_nativeName!=nativeName&&![_nativeName isEqual:nativeName]){[self resetNativeSelection];_nativeName=[nativeName copy];}
 // OS-02: an embedded text track (MP4 tx3g, HLS WebVTT) chosen without a manifest name is
 // matched by language (and title when several share it), so AVPlayer draws it itself and it
 // survives Picture in Picture and AirPlay. Sidecar, uploaded and provider tracks stay drawn by
 // the app: they are not part of the stream.
 // Direct embedded text needs the app clock for a correction. HLS manifest timing is baked in.
 BOOL embedded=[command[@"origin"] isEqual:@"embedded"]&&_offset==0;
 NSString *language=embedded&&[command[@"language"] isKindOfClass:NSString.class]&&[command[@"language"] length]<=35?command[@"language"]:nil;
 NSString *title=embedded&&[command[@"title"] isKindOfClass:NSString.class]&&[command[@"title"] length]<=256?command[@"title"]:nil;
 if(!(_nativeLanguage==language||[_nativeLanguage isEqual:language])||!(_nativeTitle==title||[_nativeTitle isEqual:title])){[self resetNativeSelection];_nativeLanguage=[language copy];_nativeTitle=[title copy];}
 _command=[command copy];_revision=[command[@"revision"] integerValue];_offset=offset;
 [self disableEmbedded:item];
 if(!_session){NSURLSessionConfiguration *configuration=NSURLSessionConfiguration.ephemeralSessionConfiguration;configuration.URLCache=nil;configuration.HTTPCookieStorage=nil;configuration.URLCredentialStorage=nil;configuration.requestCachePolicy=NSURLRequestReloadIgnoringLocalCacheData;configuration.timeoutIntervalForRequest=15;configuration.timeoutIntervalForResource=20;configuration.HTTPMaximumConnectionsPerHost=1;if(self.configureSession)self.configureSession(configuration);_session=[NSURLSession sessionWithConfiguration:configuration delegate:self delegateQueue:NSOperationQueue.mainQueue];}
 if(!_timer&&!self.manualTimer){__weak PorticoSubtitleSelection *weakSelf=self;_timer=[NSTimer scheduledTimerWithTimeInterval:1 repeats:YES block:^(NSTimer *timer){[weakSelf timerTick];}];}
 if(!sameResource||refreshRequested)[self fetch];else [self tick:_time];
}
/** Reads a download's WebVTT file off the main thread and keeps its cues. Tags are dropped (the
 * line is plain text, italic when the cue is), and a cue that cannot be read costs only itself. */
- (void)readLocal:(NSURL *)url source:(NSString *)source {
 NSInteger epoch=_localEpoch;BOOL (^allowed)(NSURL *)=[self.allowsLocalFile copy];__weak PorticoSubtitleSelection *weakSelf=self;
 dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{
  NSData *data=PorticoReadLocalSubtitle(url,source,SubtitleByteLimit,allowed);
  NSString *text=data?[[NSString alloc]initWithData:data encoding:NSUTF8StringEncoding]:nil;
  NSMutableArray *cues=[NSMutableArray new];
  if(text){
   NSArray<NSString *> *lines=[[text stringByReplacingOccurrencesOfString:@"\r\n" withString:@"\n"] componentsSeparatedByCharactersInSet:NSCharacterSet.newlineCharacterSet];
   NSRegularExpression *timing=[NSRegularExpression regularExpressionWithPattern:@"^\\s*(?:(\\d{1,3}):)?(\\d{2}):(\\d{2})[.,](\\d{3})\\s+-->\\s+(?:(\\d{1,3}):)?(\\d{2}):(\\d{2})[.,](\\d{3})(.*)$" options:0 error:nil];
   NSRegularExpression *tags=[NSRegularExpression regularExpressionWithPattern:@"<[^>]{0,64}>" options:0 error:nil];
   NSRegularExpression *linePosition=[NSRegularExpression regularExpressionWithPattern:@"\\bline:(-?\\d{1,3})(%?)" options:0 error:nil];
   NSUInteger textBytes=0;
   for(NSUInteger i=0;i<lines.count&&cues.count<20000;i++){
    NSString *line=lines[i];NSTextCheckingResult *m=[timing firstMatchInString:line options:0 range:NSMakeRange(0,line.length)];if(!m)continue;
    int64_t (^part)(NSUInteger)=^int64_t(NSUInteger group){NSRange r=[m rangeAtIndex:group];return r.location==NSNotFound?0:[line substringWithRange:r].longLongValue;};
    int64_t start=((part(1)*60+part(2))*60+part(3))*1000000+part(4)*1000,end=((part(5)*60+part(6))*60+part(7))*1000000+part(8)*1000;
    NSString *settings=[line substringWithRange:[m rangeAtIndex:9]];
    NSMutableArray<NSString *> *body=[NSMutableArray new];
    for(i++;i<lines.count&&lines[i].length;i++)[body addObject:lines[i]];
    NSString *raw=[body componentsJoinedByString:@"\n"];
    BOOL italic=[raw containsString:@"<i>"]||[raw containsString:@"<i."];
    NSString *plain=[tags stringByReplacingMatchesInString:raw options:0 range:NSMakeRange(0,raw.length) withTemplate:@""];
    for(NSArray<NSString *> *entity in @[@[@"&lt;",@"<"],@[@"&gt;",@">"],@[@"&nbsp;",@" "],@[@"&lrm;",@""],@[@"&rlm;",@""],@[@"&amp;",@"&"]])plain=[plain stringByReplacingOccurrencesOfString:entity[0] withString:entity[1]];
    plain=[plain stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
    if(end<=start||end>86400000000||!PlainText(plain))continue;
    textBytes+=[plain lengthOfBytesUsingEncoding:NSUTF8StringEncoding];if(textBytes>4194304)break;
    // A cue placed in the upper half of the picture is drawn at the top.
    BOOL top=NO;NSTextCheckingResult *position=[linePosition firstMatchInString:settings options:0 range:NSMakeRange(0,settings.length)];
    if(position){NSInteger value=[settings substringWithRange:[position rangeAtIndex:1]].integerValue;BOOL percent=[position rangeAtIndex:2].length>0;top=percent?(value>=0&&value<50):(value>=0&&value<8);}
    [cues addObject:@{@"start":@(start),@"end":@(end),@"text":plain,@"italic":@(italic),@"top":@(top)}];
   }
   [cues sortUsingComparator:^NSComparisonResult(NSDictionary *a,NSDictionary *b){return [a[@"start"] compare:b[@"start"]];}];
  }
  NSMutableArray *ends=[NSMutableArray new];int64_t maxEnd=0;for(NSDictionary *cue in cues){maxEnd=MAX(maxEnd,[cue[@"end"] longLongValue]);[ends addObject:@(maxEnd)];}
  dispatch_async(dispatch_get_main_queue(),^{
   PorticoSubtitleSelection *self=weakSelf;if(!self||self->_localEpoch!=epoch||!self->_local)return;
   if(!text){[self status:@"subtitle_download_unavailable"];return;}
   self->_cues=cues;self->_prefixEnds=ends;self->_authorizedUntil=DBL_MAX;[self status:@""];[self tick:self->_time];
  });
 });
}
- (void)fetch {
 if(_local||!_url||!_session||_task)return;
 _lastRequest=[self now];_bytes=[NSMutableData new];_failed=NO;_transientFailure=NO;_responseEtag=nil;
 NSMutableURLRequest *request=[NSMutableURLRequest requestWithURL:_url cachePolicy:NSURLRequestReloadIgnoringLocalCacheData timeoutInterval:15];[request setValue:@"application/json" forHTTPHeaderField:@"Accept"];
 _notModified=NO;if(_etag.length&&_cues)[request setValue:_etag forHTTPHeaderField:@"If-None-Match"];
 // A scoped media grant, not a bearer credential. Redirects are denied below.
 _task=[_session dataTaskWithRequest:request];[_task resume];
}
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {[PorticoServerTrust handleChallenge:challenge completion:completionHandler];}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completionHandler {completionHandler(nil);}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveResponse:(NSURLResponse *)response completionHandler:(void (^)(NSURLSessionResponseDisposition))completionHandler {
 if(task!=_task){completionHandler(NSURLSessionResponseCancel);return;}
 NSHTTPURLResponse *http=[response isKindOfClass:NSHTTPURLResponse.class]?(id)response:nil;
 // 304 is the server saying: checked in full, still yours, unchanged. Only ever in answer to the
 // validator sent above; the cache policy never lets the system answer on the server's behalf.
 if(http.statusCode==304&&_etag.length&&_cues){_notModified=YES;completionHandler(NSURLSessionResponseCancel);return;}
 if(http.statusCode>=500&&http.statusCode<=599){_failed=YES;_transientFailure=YES;[self status:@"subtitle_connection_failed"];completionHandler(NSURLSessionResponseCancel);return;}
 id tag=[http valueForHTTPHeaderField:@"ETag"];_responseEtag=([tag isKindOfClass:NSString.class]&&[tag length]<=256)?[tag copy]:nil;
 if(http.statusCode!=200||![response.MIMEType.lowercaseString isEqual:@"application/json"]||response.expectedContentLength>(int64_t)SubtitleByteLimit){_failed=YES;_cues=nil;_authorizedUntil=0;[self setText:@""];[self status:@"subtitle_unavailable"];completionHandler(NSURLSessionResponseCancel);return;}
 completionHandler(NSURLSessionResponseAllow);
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {
 if(task!=_task||_failed)return;if(_bytes.length+data.length>SubtitleByteLimit){_failed=YES;[_task cancel];_cues=nil;_authorizedUntil=0;[self setText:@""];[self status:@"subtitle_size_exceeded"];return;}[_bytes appendData:data];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
 if(task!=_task)return;_task=nil;
 if(_notModified){_notModified=NO;_bytes=nil;_authorizedUntil=[self now]+40;[self status:@""];[self tick:_time];return;}
 if(_failed||error){_bytes=nil;if(_transientFailure||(!_failed&&error))_lastRequest=[self now]-20;[self tick:_time];if(!_failed)[self status:@"subtitle_connection_failed"];return;}
 id document=[NSJSONSerialization JSONObjectWithData:_bytes options:0 error:nil];_bytes=nil;
 BOOL valid=[document isKindOfClass:NSDictionary.class]&&PositiveInteger(document[@"version"])&&[document[@"version"] isEqual:@1]&&[document[@"timeDomain"] isEqual:@"source-relative"]&&[document[@"cues"] isKindOfClass:NSArray.class]&&[document[@"cues"] count]<=20000;
 NSMutableArray *cues=[NSMutableArray new],*ends=[NSMutableArray new];int64_t previous=-1,maxEnd=0;NSUInteger textBytes=0;
 if(valid)for(id cue in document[@"cues"]){int64_t start=0,end=0;
  // Strict about the envelope, forgiving about cues: one bad cue costs only itself.
  if(![cue isKindOfClass:NSDictionary.class]||!DecimalUS(cue[@"startUs"],86400000000,NO,&start)||!DecimalUS(cue[@"endUs"],86400000000,NO,&end)||end<=start||start<previous||!PlainText(cue[@"text"]))continue;
  textBytes+=[cue[@"text"] lengthOfBytesUsingEncoding:NSUTF8StringEncoding];if(textBytes>4194304){valid=NO;break;}
  previous=start;maxEnd=MAX(maxEnd,end);[cues addObject:@{@"start":@(start),@"end":@(end),@"text":cue[@"text"],@"italic":@([cue[@"italic"]isEqual:@YES]),@"top":@([cue[@"top"]isEqual:@YES])}];[ends addObject:@(maxEnd)];
 }
 if(valid&&cues.count==0&&[document[@"cues"] count]>0)valid=NO;
 if(!valid){_etag=nil;_cues=nil;_authorizedUntil=0;[self setText:@""];[self status:@"subtitle_document_invalid"];return;}
 _etag=_responseEtag;_cues=cues;_prefixEnds=ends;_authorizedUntil=[self now]+40;[self status:@""];[self tick:_time];
}
- (void)tick:(CMTime)time {
 _time=time;if(_nativeSelected){[self setText:@""];return;}if(!_command||!_cues||[self now]>_authorizedUntil||!CMTIME_IS_NUMERIC(time)){[self setText:@""];return;}
 CMTime converted=CMTimeConvertScale(time,1000000,kCMTimeRoundingMethod_RoundHalfAwayFromZero);int64_t now=converted.value-_offset;
 NSUInteger low=0,high=_cues.count;while(low<high){NSUInteger mid=low+(high-low)/2;if([_cues[mid][@"start"] longLongValue]<=now)low=mid+1;else high=mid;}
 _italic=NO;_top=NO;NSMutableArray<NSString *> *active=[NSMutableArray new];
 for(NSInteger i=(NSInteger)low-1;i>=0;i--){if([_prefixEnds[(NSUInteger)i] longLongValue]<=now)break;NSDictionary *cue=_cues[(NSUInteger)i];if([cue[@"end"] longLongValue]>now){[active insertObject:cue[@"text"] atIndex:0];_italic|=[cue[@"italic"]boolValue];_top|=[cue[@"top"]boolValue];}}
 [self setText:[active componentsJoinedByString:@"\n"]];
}
- (void)dealloc{[_task cancel];[_session invalidateAndCancel];[_timer invalidate];}
@end
