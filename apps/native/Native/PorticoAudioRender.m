#import "PorticoAudioRender.h"
#import "PorticoServerTrust.h"
#import "PorticoAudioProcessing.h"
#import "PorticoEqualizer.h"
#import "PorticoAudioCapture.h"
#import "PorticoAudioEffects.h"
#import "PorticoListeningPolicy.h"
#import <Libporticosubtitle/PorticoSubtitleCore.h>
#import <AudioToolbox/AudioToolbox.h>
#import <CommonCrypto/CommonDigest.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#include <sys/time.h>
#import <math.h>
#import <mach/mach_time.h>

static NSDictionary *APDictionary(id v){return [v isKindOfClass:NSDictionary.class]?v:nil;}
static BOOL APNumber(id v,double min,double max){return [v isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)v)!=CFBooleanGetTypeID()&&isfinite([v doubleValue])&&[v doubleValue]>=min&&[v doubleValue]<=max;}
static BOOL APInteger(id v,double min,double max){return APNumber(v,min,max)&&floor([v doubleValue])==[v doubleValue];}
static BOOL APID(id v){return [v isKindOfClass:NSString.class]&&[v length]>0&&[v length]<=128&&[v rangeOfString:@"^[A-Za-z0-9_-]+$" options:NSRegularExpressionSearch].location!=NSNotFound;}
static BOOL APSameOrigin(NSURL *a,NSURL *b){NSNumber *ap=a.port?:@([a.scheme isEqual:@"https"]?443:80),*bp=b.port?:@([b.scheme isEqual:@"https"]?443:80);return [a.scheme.lowercaseString isEqual:b.scheme.lowercaseString]&&[a.host.lowercaseString isEqual:b.host.lowercaseString]&&[ap isEqual:bp]&&!a.user&&!a.password&&!a.fragment;}

#pragma mark - Version 2: the device decodes the file (spec §18.1, plan §9.6)

/*
 * Plexamp's model. The server says where the file is (the original, with Range, or a FLAC
 * conversion) and what a decoder can't know by itself: the exact gapless trim and the gains.
 * Bytes land in an evictable sparse cache; Core Audio decodes them (ExtAudioFile over a blocking
 * read callback, on each deck's own queue); the engine outputs exactly `durationFrames` per track,
 * gain baked into the samples. Two decks: a gapless successor of the same format is appended to
 * the current deck's player node, so the join is sample exact by construction; a crossfade or a
 * format change uses a second node scheduled on the output sample clock.
 */


/** A player's time for its last render, or nil: AVAudioPlayerNode raises for a node that isn't
 * attached to a running engine (a route change, an engine stopped under us), so ask only then. */
static AVAudioTime *ARPlayerTime(AVAudioPlayerNode *node,AVAudioTime *nodeTime){
 // The render time of a node that hasn't rendered yet (a second node scheduled for later) is
 // neither sample- nor host-valid, and asking with it raises (the harness's crossfade run hit it).
 if(!node||!nodeTime||!(nodeTime.isSampleTimeValid||nodeTime.isHostTimeValid)||!node.engine||!node.engine.isRunning)return nil;
 @try{return [node playerTimeForNodeTime:nodeTime];}@catch(NSException *e){return nil;}
}
static NSString *const ARCacheFolder=@"PorticoAudio";
static const long long ARFetchChunk=4LL*1024*1024;
static const double ARDecodeAhead=20,ARPrepareSeconds=10,ARStartSeconds=1.0,ARReadTimeout=20;

static NSString *ARHex(NSString *text){
 NSData *data=[text dataUsingEncoding:NSUTF8StringEncoding];unsigned char digest[CC_SHA256_DIGEST_LENGTH];CC_SHA256(data.bytes,(CC_LONG)data.length,digest);
 NSMutableString *out=[NSMutableString new];for(int i=0;i<16;i++)[out appendFormat:@"%02x",digest[i]];return out;
}
static NSURL *ARCacheDirectory(void){
 NSURL *base=[NSFileManager.defaultManager URLsForDirectory:NSCachesDirectory inDomains:NSUserDomainMask].firstObject;
 NSURL *dir=[base URLByAppendingPathComponent:ARCacheFolder isDirectory:YES];
 [NSFileManager.defaultManager createDirectoryAtURL:dir withIntermediateDirectories:YES attributes:nil error:nil];
 [dir setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];return dir;
}
/** The cache never pressures the device: at most 1 GB and 5% of the free space (tvOS, whose
 * storage is small and purgeable: 256 MB and 2%), never less than a playable window. It lives in
 * Caches, excluded from backup, so the system may evict it at any time. */
static long long ARCacheBudget(void){
#if TARGET_OS_TV
 NSNumber *free=nil;[ARCacheDirectory() getResourceValue:&free forKey:NSURLVolumeAvailableCapacityKey error:nil];
 long long cap=256LL*1024*1024;double share=.02;
#else
 NSNumber *free=nil;[ARCacheDirectory() getResourceValue:&free forKey:NSURLVolumeAvailableCapacityForImportantUsageKey error:nil];
 long long cap=1024LL*1024*1024;double share=.05;
#endif
 if(free.longLongValue>0)cap=MIN(cap,(long long)(free.doubleValue*share));
 return MAX(cap,48LL*1024*1024);
}
// Counted: two decks of one file (repeat one, a prepared copy of the playing track) share a key,
// and closing one must not let eviction take the file from the other (INT T6).
static NSCountedSet<NSString *> *ARActiveKeys(void){static NSCountedSet *keys;static dispatch_once_t once;dispatch_once(&once,^{keys=[NSCountedSet new];});return keys;}
/** Least recently used files go first once the cache passes its budget; files in use stay. */
static void AREvict(void){
 NSURL *dir=ARCacheDirectory();NSArray *keys=@[NSURLContentModificationDateKey,NSURLTotalFileAllocatedSizeKey];long long budget=ARCacheBudget();
 NSArray<NSURL *> *files=[NSFileManager.defaultManager contentsOfDirectoryAtURL:dir includingPropertiesForKeys:keys options:0 error:nil];
 long long total=0;NSMutableArray *bins=[NSMutableArray new];
 for(NSURL *f in files){if(![f.pathExtension isEqual:@"bin"])continue;NSDictionary *v=[f resourceValuesForKeys:keys error:nil];total+=[v[NSURLTotalFileAllocatedSizeKey] longLongValue];[bins addObject:@{@"url":f,@"date":v[NSURLContentModificationDateKey]?:NSDate.distantPast,@"size":v[NSURLTotalFileAllocatedSizeKey]?:@0}];}
 if(total<=budget)return;
 [bins sortUsingComparator:^NSComparisonResult(NSDictionary *a,NSDictionary *b){return [a[@"date"] compare:b[@"date"]];}];
 NSSet *active;@synchronized(ARActiveKeys()){active=[ARActiveKeys() copy];}
 for(NSDictionary *b in bins){if(total<=budget*3/4)break;NSURL *f=b[@"url"];NSString *key=[f.URLByDeletingPathExtension.lastPathComponent componentsSeparatedByString:@"~"].firstObject;if([active containsObject:key])continue;
  [NSFileManager.defaultManager removeItemAtURL:f error:nil];[NSFileManager.defaultManager removeItemAtURL:[[f URLByDeletingPathExtension] URLByAppendingPathExtension:@"done"] error:nil];total-=[b[@"size"] longLongValue];}
}

/** The file's bytes: a local file (the conformance harness), or a sparse cache filled by Range
 * requests. Reads block (on a deck's decode queue) until their bytes arrive, the fetch fails,
 * the byte budget of a prepared presentation runs out, or the cache is closed. */
@interface ARBytes : NSObject<NSURLSessionDataDelegate>
@property(atomic,readonly) long long size;
@property(atomic,copy,readonly) NSString *failure;
- (instancetype)initWithURL:(NSURL *)url key:(NSString *)key size:(long long)size limit:(long long)limit;
- (BOOL)read:(void *)buffer offset:(long long)offset length:(UInt32)length actual:(UInt32 *)actual;
- (long long)waitForSize;
/** Read ahead to the end (a committed successor is cached whole before its boundary). */
- (void)readAll;
- (void)unlimit;
- (void)close;
@end
@implementation ARBytes {
 NSURL *_url;NSString *_key,*_fileKey;NSCondition *_lock;NSMutableIndexSet *_have;int _fd;BOOL _file,_closed,_limited,_limitHit,_whole,_private;
 long long _limit,_fetched,_want,_ahead,_expected,_budget;NSURLSession *_session;NSURLSessionDataTask *_task;long long _taskAt,_taskNext;NSOperationQueue *_queue;
}
@synthesize size=_size,failure=_failure;
- (instancetype)initWithURL:(NSURL *)url key:(NSString *)key size:(long long)size limit:(long long)limit {
 if((self=[super init])){
  _url=url;_key=key;_lock=[NSCondition new];_have=[NSMutableIndexSet new];_size=size>0?size:-1;_limit=limit>0?limit:0;_limited=limit>0;_ahead=16*1024*1024;_fd=-1;_budget=ARCacheBudget();
  if(url.isFileURL){_file=YES;_fd=open(url.fileSystemRepresentation,O_RDONLY);if(_fd<0){_failure=@"audio_file_open_failed";return self;}struct stat st;fstat(_fd,&st);_size=st.st_size;[_have addIndexesInRange:NSMakeRange(0,(NSUInteger)_size)];return self;}
  BOOL shared;@synchronized(ARActiveKeys()){shared=[ARActiveKeys() countForObject:key]>0;[ARActiveKeys() addObject:key];}
  AREvict();
  // INT T6: a second deck of one file (repeat one; the committed deck opening a track that is still
  // playing) must never truncate or punch the file the first deck is reading, because the first
  // deck counts those bytes as present. It shares the file only when it's whole and small enough
  // never to be released behind the reader; otherwise it fills a private copy, deleted on close.
  _fileKey=key;
  NSURL *dir=ARCacheDirectory();NSString *bin=[dir URLByAppendingPathComponent:[key stringByAppendingPathExtension:@"bin"]].path,*done=[dir URLByAppendingPathComponent:[key stringByAppendingPathExtension:@"done"]].path;
  if(shared){
   struct stat st;BOOL whole=_size>0&&_size<=_budget/2&&[NSFileManager.defaultManager fileExistsAtPath:done]&&stat(bin.fileSystemRepresentation,&st)==0&&st.st_size==_size;
   if(whole&&(_fd=open(bin.fileSystemRepresentation,O_RDONLY))>=0){[_have addIndexesInRange:NSMakeRange(0,(NSUInteger)_size)];_whole=YES;return self;}
   _private=YES;_fileKey=[NSString stringWithFormat:@"%@~%@",key,NSUUID.UUID.UUIDString];
   bin=[dir URLByAppendingPathComponent:[_fileKey stringByAppendingPathExtension:@"bin"]].path;done=[dir URLByAppendingPathComponent:[_fileKey stringByAppendingPathExtension:@"done"]].path;
  }
  _fd=open(bin.fileSystemRepresentation,O_RDWR|O_CREAT,0600);
  if(_fd<0){_failure=@"audio_cache_unavailable";return self;}
  struct stat st;fstat(_fd,&st);
  if(!_private&&_size>0&&[NSFileManager.defaultManager fileExistsAtPath:done]&&st.st_size==_size){[_have addIndexesInRange:NSMakeRange(0,(NSUInteger)_size)];_whole=YES;}
  else{ftruncate(_fd,0);[NSFileManager.defaultManager removeItemAtPath:done error:nil];}
  futimes(_fd,NULL);
  NSURLSessionConfiguration *configuration=NSURLSessionConfiguration.ephemeralSessionConfiguration;configuration.URLCache=nil;configuration.HTTPCookieStorage=nil;configuration.URLCredentialStorage=nil;configuration.HTTPShouldSetCookies=NO;configuration.timeoutIntervalForRequest=25;
  _queue=[NSOperationQueue new];_queue.maxConcurrentOperationCount=1;
  _session=[NSURLSession sessionWithConfiguration:configuration delegate:self delegateQueue:_queue];
  [_queue addOperationWithBlock:^{[self pump];}];
 }
 return self;
}
- (BOOL)complete {return _size>0&&[_have containsIndexesInRange:NSMakeRange(0,(NSUInteger)_size)];}
/** Starts the next Range request, if one is due (delegate queue): the first missing bytes from
 * where the decoder reads, up to the read-ahead (the whole file once `readAll`). */
- (void)pump {
 [_lock lock];
 if(_closed||_task||_failure||_file||[self complete]){[_lock unlock];return;}
 if(_limited&&_fetched>=_limit){_limitHit=YES;[_lock broadcast];[_lock unlock];return;}
 NSMutableURLRequest *request=[NSMutableURLRequest requestWithURL:_url cachePolicy:NSURLRequestReloadIgnoringLocalCacheData timeoutInterval:25];request.HTTPShouldHandleCookies=NO;
 if(_size<=0){
  if(_have.count>0){[_lock unlock];return;} // a stream of unknown length is read once, from the start
  _taskAt=0;
 }else{
  // A file bigger than the whole cache (a two-hour 24/96 FLAC) keeps only a window around where
  // it is read: bytes well behind are released (a later seek back fetches them again).
  BOOL whole=_whole&&_size<=_budget/2;
  if(_size>_budget/2)[self releaseBehind:MAX(0,_want-8LL*1024*1024)];
  long long want=MIN(MAX(0,_want),_size-1),from=whole?0:want,horizon=whole?_size:MIN(_size,want+_ahead);
  long long first=(long long)[self firstMissingFrom:from];
  if(first>=horizon){[_lock unlock];return;}
  long long to=MIN(horizon,first+ARFetchChunk);
  NSUInteger present=[_have indexGreaterThanOrEqualToIndex:(NSUInteger)first];if(present!=NSNotFound)to=MIN(to,(long long)present);
  if(_limited)to=MIN(to,first+(_limit-_fetched));
  [request setValue:[NSString stringWithFormat:@"bytes=%lld-%lld",first,to-1] forHTTPHeaderField:@"Range"];
  _taskAt=first;
 }
 _taskNext=_taskAt;_task=[_session dataTaskWithRequest:request];[_task resume];
 [_lock unlock];
}
- (void)releaseBehind:(long long)offset {
 long long end=offset&~4095LL;if(end<=0)return;
 NSUInteger first=_have.firstIndex;if(first==NSNotFound||(long long)first>=end)return;
 // Another deck may be reading this very file (INT T6): the shared file is released only once it's ours alone.
 if(!_private){NSUInteger readers;@synchronized(ARActiveKeys()){readers=[ARActiveKeys() countForObject:_key];}if(readers>1)return;}
 long long start=(long long)first&~4095LL;
 // The file stops being whole: its done-marker goes first, so a later open never trusts holes (INT T6).
 if(_fileKey.length)[NSFileManager.defaultManager removeItemAtURL:[ARCacheDirectory() URLByAppendingPathComponent:[_fileKey stringByAppendingPathExtension:@"done"]] error:nil];
 _whole=NO;
 struct fpunchhole hole={0,0,start,end-start};
 if(fcntl(_fd,F_PUNCHHOLE,&hole)==0)[_have removeIndexesInRange:NSMakeRange((NSUInteger)start,(NSUInteger)(end-start))];
}
- (NSUInteger)firstMissingFrom:(long long)from {
 __block NSUInteger i=(NSUInteger)from;
 [_have enumerateRangesUsingBlock:^(NSRange range,BOOL *stop){if(NSLocationInRange(i,range)){i=NSMaxRange(range);*stop=YES;}}];
 return i;
}
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {[PorticoServerTrust handleChallenge:challenge completion:completionHandler];}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))done {done(nil);}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveResponse:(NSURLResponse *)raw completionHandler:(void (^)(NSURLSessionResponseDisposition))done {
 NSHTTPURLResponse *r=(NSHTTPURLResponse *)raw;[_lock lock];
 BOOL ok=NO;
 if(r.statusCode==206){NSString *range=[r valueForHTTPHeaderField:@"Content-Range"];long long start=-1,total=-1;
  NSScanner *s=[NSScanner scannerWithString:range?:@""];if([s scanString:@"bytes" intoString:nil]&&[s scanLongLong:&start]){[s scanUpToString:@"/" intoString:nil];[s scanString:@"/" intoString:nil];[s scanLongLong:&total];}
  ok=start==_taskAt;if(total>0&&_size<=0)_size=total;}
 else if(r.statusCode==200&&_taskAt==0){ok=YES;if(r.expectedContentLength>0)_size=r.expectedContentLength;}
 else if(r.statusCode==403&&_limited){_limitHit=YES;}
 else _failure=r.statusCode==404||r.statusCode==410?@"audio_source_unavailable":@"audio_fetch_failed";
 if(!ok){_task=nil;[_lock broadcast];}
 [_lock unlock];done(ok?NSURLSessionResponseAllow:NSURLSessionResponseCancel);
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {
 [_lock lock];
 if(_closed||task!=_task){[_lock unlock];return;}
 long long at=_taskNext;__block BOOL wrote=YES;
 [data enumerateByteRangesUsingBlock:^(const void *bytes,NSRange range,BOOL *stop){if(pwrite(self->_fd,bytes,range.length,at+(long long)range.location)!=(ssize_t)range.length){wrote=NO;*stop=YES;}}];
 if(!wrote){_failure=@"audio_cache_write_failed";[task cancel];_task=nil;}
 else{[_have addIndexesInRange:NSMakeRange((NSUInteger)at,data.length)];_taskNext+=(long long)data.length;_fetched+=(long long)data.length;}
 [_lock broadcast];[_lock unlock];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
 [_lock lock];
 if(task==_task){_task=nil;
  if(error&&error.code!=NSURLErrorCancelled&&!_closed){_failure=@"audio_connection_interrupted";}
  if(!error&&_size<=0)_size=_taskNext; // a stream of unknown length has ended
  if([self complete]){fsync(_fd);NSString *done=[ARCacheDirectory() URLByAppendingPathComponent:[_fileKey stringByAppendingPathExtension:@"done"]].path;[NSFileManager.defaultManager createFileAtPath:done contents:nil attributes:nil];}
 }
 [_lock broadcast];[_lock unlock];
 if(!_failure)[self pump];
}
- (BOOL)read:(void *)buffer offset:(long long)offset length:(UInt32)length actual:(UInt32 *)actual {
 *actual=0;[_lock lock];
 NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:ARReadTimeout];
 for(;;){
  if(_closed||(_failure&&!_file)){[_lock unlock];return NO;}
  long long end=_size>0?MIN(_size,offset+length):offset+length;
  if(_size>0&&offset>=_size){[_lock unlock];return YES;}
  if(end>offset&&[_have containsIndexesInRange:NSMakeRange((NSUInteger)offset,(NSUInteger)(end-offset))]){
   ssize_t n=pread(_fd,buffer,(size_t)(end-offset),offset);[_lock unlock];if(n<0)return NO;*actual=(UInt32)n;return YES;
  }
  if(_limitHit&&_limited){[_lock unlock];return NO;}
  if(_want!=offset){_want=offset;[_queue addOperationWithBlock:^{[self pump];}];}
  if(![_lock waitUntilDate:deadline]){[_lock unlock];return NO;}
 }
}
- (long long)waitForSize {
 [_lock lock];NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:ARReadTimeout];
 while(_size<=0&&!_closed&&!_failure){if(![_lock waitUntilDate:deadline])break;}
 long long size=_size;[_lock unlock];return size;
}
- (void)readAll {[_lock lock];_whole=YES;[_lock unlock];[_queue addOperationWithBlock:^{[self pump];}];}
- (void)unlimit {[_lock lock];_limited=NO;_limitHit=NO;[_lock broadcast];[_lock unlock];[_queue addOperationWithBlock:^{[self pump];}];}
- (void)close {
 [_lock lock];if(_closed){[_lock unlock];return;}_closed=YES;[_task cancel];_task=nil;[_lock broadcast];[_lock unlock];
 [_session invalidateAndCancel];_session=nil;
 if(_key){@synchronized(ARActiveKeys()){[ARActiveKeys() removeObject:_key];}}
 int fd=_fd;_fd=-1;if(fd>=0)close(fd);
 if(_private){NSURL *dir=ARCacheDirectory();for(NSString *ext in @[@"bin",@"done"])[NSFileManager.defaultManager removeItemAtURL:[dir URLByAppendingPathComponent:[_fileKey stringByAppendingPathExtension:ext]] error:nil];}
}
- (void)dealloc {[self close];}
@end

/** Refuses a redirect: the index is read from exactly the address the plan names. */
@interface ARIndexFetch : NSObject<NSURLSessionTaskDelegate> @end
@implementation ARIndexFetch
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))done {done(nil);}
@end

/** Core Audio decoding over ARBytes: AudioFile with a blocking read callback, wrapped by
 * ExtAudioFile (packet tables, priming, sample-accurate seeks). Confined to one deck's queue. */
@interface ARDecoder : NSObject
@property(nonatomic,readonly) AVAudioFormat *format;
@property(nonatomic,readonly) long long total;
/** What Core Audio reports: the packet table's priming and remainder, and its own frame count. */
@property(nonatomic,readonly) long long primed,remainder,fileFrames;
- (instancetype)initWithBytes:(ARBytes *)bytes plan:(NSDictionary *)plan;
- (NSString *)open;
- (NSString *)seek:(long long)frame;
/** The next frames of the track (nil at its end, or with `error`). */
- (AVAudioPCMBuffer *)read:(AVAudioFrameCount)frames error:(NSString **)error;
- (void)close;
@end
@implementation ARDecoder {
 ARBytes *_bytes;NSDictionary *_plan;PSAudio *_opus;long long _opusBytePosition;AudioFileID _file;ExtAudioFileRef _ext;long long _first,_next;
 AudioConverterRef _aac;SInt64 _packet;long long _packetFrames,_aacSkip;
 void *_packetBuffer;AudioStreamPacketDescription *_packetDescriptions;UInt32 _packetCapacity,_aacInputChannels;
 // The server's seek index (MP3, ADTS): a second AudioFile over the bytes from an entry on, decoded
 // packet by packet from there; `_ixSkip` frames are dropped to reach the exact frame.
 NSArray<NSNumber *> *_offsets,*_frames;BOOL _indexTried;AudioFileID _shifted;AudioConverterRef _ix;long long _base,_ixSkip;SInt64 _ixPacket;
 void *_ixBuffer;AudioStreamPacketDescription *_ixDescriptions;UInt32 _ixCapacity,_ixChannels;
}
static int AROpusRead(void *opaque,uint8_t *buffer,int count){ARDecoder *d=(__bridge ARDecoder *)opaque;UInt32 actual=0;if(![d->_bytes read:buffer offset:d->_opusBytePosition length:(UInt32)count actual:&actual])return -5;d->_opusBytePosition+=actual;return actual?(int)actual:-541478725;}
static int64_t AROpusSeek(void *opaque,int64_t offset,int whence){ARDecoder *d=(__bridge ARDecoder *)opaque;long long size=d->_bytes.size;if(whence&0x10000)return size;whence&=~0x20000;long long base=whence==SEEK_SET?0:whence==SEEK_CUR?d->_opusBytePosition:whence==SEEK_END?size:-1;if(base<0||offset < -base||(size>0&&offset>size-base))return -22;return d->_opusBytePosition=base+offset;}
static OSStatus ARRead(void *client,SInt64 position,UInt32 count,void *buffer,UInt32 *actual){ARDecoder *d=(__bridge ARDecoder *)client;return [d->_bytes read:buffer offset:position length:count actual:actual]?noErr:kAudioFileUnspecifiedError;}
// A converted stream of unknown length reads as very long; its real end reads as end of file.
static SInt64 ARSize(void *client){ARDecoder *d=(__bridge ARDecoder *)client;long long size=d->_bytes.size;return size>0?size:(1LL<<40);}
/** AAC packets are decoded without the file wrapper's inferred priming. The server's raw
 * frame window applies equally to iTunSMPB, edit-list-only AAC and SBR output frames. */
static OSStatus ARAACInput(AudioConverterRef converter,UInt32 *count,AudioBufferList *data,AudioStreamPacketDescription **descriptions,void *client){
 ARDecoder *d=(__bridge ARDecoder *)client;
 UInt32 packets=MIN(*count,128),bytes=d->_packetCapacity;
 OSStatus status=AudioFileReadPacketData(d->_file,false,&bytes,d->_packetDescriptions,d->_packet,&packets,d->_packetBuffer);
 if(status!=noErr&&status!=kAudioFileEndOfFileError){*count=0;return status;}
 d->_packet+=packets;*count=packets;
 data->mNumberBuffers=1;data->mBuffers[0].mNumberChannels=d->_aacInputChannels;data->mBuffers[0].mDataByteSize=bytes;data->mBuffers[0].mData=d->_packetBuffer;
 if(descriptions)*descriptions=d->_packetDescriptions;
 return noErr;
}

// The bytes from an index entry on, as a file of their own (a packet boundary starts a valid stream).
static OSStatus ARShiftedRead(void *client,SInt64 position,UInt32 count,void *buffer,UInt32 *actual){ARDecoder *d=(__bridge ARDecoder *)client;return [d->_bytes read:buffer offset:d->_base+position length:count actual:actual]?noErr:kAudioFileUnspecifiedError;}
static SInt64 ARShiftedSize(void *client){ARDecoder *d=(__bridge ARDecoder *)client;long long size=d->_bytes.size;return size>d->_base?size-d->_base:0;}
/** Priming-method control usually belongs to a sample-rate converter. A same-rate AAC
 * decoder can omit that property: accept only its actual zero-prime, no-resampling case.
 * The file encoder's raw priming is still removed exactly once by the measured plan. */
static BOOL ARAACRawPriming(AudioConverterRef converter) {
 UInt32 none=kConverterPrimeMethod_None;OSStatus status=AudioConverterSetProperty(converter,kAudioConverterPrimeMethod,sizeof(none),&none);
 if(status==noErr)return YES;
 if(status!=kAudioConverterErr_PropertyNotSupported)return NO;
 AudioConverterPrimeInfo prime={0};UInt32 size=sizeof(prime);
 if(AudioConverterGetProperty(converter,kAudioConverterPrimeInfo,&size,&prime)!=noErr||prime.leadingFrames||prime.trailingFrames)return NO;
 AudioStreamBasicDescription input={0},output={0};size=sizeof(input);
 if(AudioConverterGetProperty(converter,kAudioConverterCurrentInputStreamDescription,&size,&input)!=noErr)return NO;
 size=sizeof(output);if(AudioConverterGetProperty(converter,kAudioConverterCurrentOutputStreamDescription,&size,&output)!=noErr)return NO;
 return isfinite(input.mSampleRate)&&isfinite(output.mSampleRate)&&input.mSampleRate>0&&fabs(input.mSampleRate-output.mSampleRate)<.01;
}
static OSStatus ARIndexInput(AudioConverterRef converter,UInt32 *count,AudioBufferList *data,AudioStreamPacketDescription **descriptions,void *client){
 ARDecoder *d=(__bridge ARDecoder *)client;
 UInt32 packets=MIN(*count,128),bytes=d->_ixCapacity;
 OSStatus status=AudioFileReadPacketData(d->_shifted,false,&bytes,d->_ixDescriptions,d->_ixPacket,&packets,d->_ixBuffer);
 if(status!=noErr&&status!=kAudioFileEndOfFileError){*count=0;return status;}
 d->_ixPacket+=packets;*count=packets;
 data->mNumberBuffers=1;data->mBuffers[0].mNumberChannels=d->_ixChannels;data->mBuffers[0].mDataByteSize=bytes;data->mBuffers[0].mData=d->_ixBuffer;
 if(descriptions)*descriptions=d->_ixDescriptions;
 return noErr;
}
- (instancetype)initWithBytes:(ARBytes *)bytes plan:(NSDictionary *)plan {if((self=[super init])){_bytes=bytes;_plan=plan;}return self;}
+ (AudioFileTypeID)hint:(NSDictionary *)plan {
 NSString *c=plan[@"container"];
 if([c isEqual:@"flac"])return kAudioFileFLACType;if([c isEqual:@"mp4"]||[c isEqual:@"m4a"])return kAudioFileM4AType;if([c isEqual:@"mp3"])return kAudioFileMP3Type;
 if([c isEqual:@"wav"])return kAudioFileWAVEType;if([c isEqual:@"aiff"])return kAudioFileAIFFType;if([c isEqual:@"adts"])return kAudioFileAAC_ADTSType;if([c isEqual:@"caf"])return kAudioFileCAFType;return 0;
}
- (NSString *)open {
 if(_bytes.failure)return _bytes.failure;
 if(![_plan[@"mode"] isEqual:@"converted"]&&[_bytes waitForSize]<=0)return _bytes.failure?:@"audio_source_size_unknown";
 if([_plan[@"codec"] isEqual:@"opus"]){
  int rate=0,channels=0;_opus=ps_audio_open((__bridge void *)self,AROpusRead,AROpusSeek,&rate,&channels);
  if(!_opus||rate!=[_plan[@"sampleRate"] intValue]||channels!=[_plan[@"channels"] intValue])return @"audio_opus_plan_mismatch";
  static const AudioChannelLabel labels[]={kAudioChannelLabel_Left,kAudioChannelLabel_Right,kAudioChannelLabel_Center,kAudioChannelLabel_LFEScreen,kAudioChannelLabel_LeftSurround,kAudioChannelLabel_RightSurround,kAudioChannelLabel_LeftCenter,kAudioChannelLabel_RightCenter,kAudioChannelLabel_CenterSurround,kAudioChannelLabel_LeftSurroundDirect,kAudioChannelLabel_RightSurroundDirect};
  size_t layoutSize=offsetof(AudioChannelLayout,mChannelDescriptions)+(size_t)channels*sizeof(AudioChannelDescription);AudioChannelLayout *layout=calloc(1,layoutSize);layout->mChannelLayoutTag=kAudioChannelLayoutTag_UseChannelDescriptions;layout->mNumberChannelDescriptions=channels;
  BOOL known=YES;for(int ch=0;ch<channels;ch++){int label=ps_audio_channel_at(_opus,ch);if(label<0||label>10){known=NO;break;}layout->mChannelDescriptions[ch].mChannelLabel=labels[label];}
  AVAudioChannelLayout *order=known?[[AVAudioChannelLayout alloc]initWithLayout:layout]:nil;free(layout);if(!order)return @"audio_opus_layout_unsupported";
  _format=[[AVAudioFormat alloc]initWithCommonFormat:AVAudioPCMFormatFloat32 sampleRate:rate interleaved:NO channelLayout:order];
  _first=[_plan[@"trim"][@"startFrames"] longLongValue];_total=[_plan[@"durationFrames"] longLongValue];return [self seek:0];
 }
 OSStatus status=AudioFileOpenWithCallbacks((__bridge void *)self,ARRead,NULL,ARSize,NULL,[ARDecoder hint:_plan],&_file);
 if(status!=noErr)return @"audio_file_read_failed";
 status=ExtAudioFileWrapAudioFileID(_file,false,&_ext);if(status!=noErr)return @"audio_file_decode_failed";
 AudioStreamBasicDescription source={0};UInt32 size=sizeof(source);
 if(ExtAudioFileGetProperty(_ext,kExtAudioFileProperty_FileDataFormat,&size,&source)!=noErr||source.mSampleRate<=0)return @"audio_format_unsupported";
 AVAudioChannelCount channels=(AVAudioChannelCount)source.mChannelsPerFrame;
 if(channels<1||channels>32||channels!=[_plan[@"channels"] unsignedIntValue])return @"audio_channel_plan_mismatch";
 NSInteger maximum=AVAudioSession.sharedInstance.maximumOutputNumberOfChannels;
 if(maximum>0&&channels>(AVAudioChannelCount)maximum)return @"audio_output_channels_unsupported";
 _format=[[AVAudioFormat alloc] initStandardFormatWithSampleRate:source.mSampleRate channels:channels];
 if(channels>2){
  UInt32 layoutSize=0;Boolean writable=false;
  if(ExtAudioFileGetPropertyInfo(_ext,kExtAudioFileProperty_FileChannelLayout,&layoutSize,&writable)!=noErr||layoutSize<sizeof(AudioChannelLayout)||layoutSize>65536)return @"audio_channel_layout_unsupported";
  AudioChannelLayout *layout=malloc(layoutSize);OSStatus result=ExtAudioFileGetProperty(_ext,kExtAudioFileProperty_FileChannelLayout,&layoutSize,layout);
  AVAudioChannelLayout *known=result==noErr?[[AVAudioChannelLayout alloc]initWithLayout:layout]:nil;free(layout);
  if(!known||known.channelCount!=channels)return @"audio_channel_layout_invalid";
  _format=[[AVAudioFormat alloc]initWithCommonFormat:AVAudioPCMFormatFloat32 sampleRate:source.mSampleRate interleaved:NO channelLayout:known];
 }
 AudioStreamBasicDescription client=*_format.streamDescription;
 if(ExtAudioFileSetProperty(_ext,kExtAudioFileProperty_ClientDataFormat,sizeof(client),&client)!=noErr)return @"audio_client_format_unsupported";
 if([_plan[@"codec"] isEqual:@"aac"]){
  double rate=[_plan[@"sampleRate"] doubleValue];
  _format=[[AVAudioFormat alloc] initStandardFormatWithSampleRate:rate channels:channels];client=*_format.streamDescription;
  if(AudioConverterNew(&source,&client,&_aac)!=noErr)return @"audio_aac_configuration_failed";
  UInt32 cookieSize=0,writable=0;
  if(AudioFileGetPropertyInfo(_file,kAudioFilePropertyMagicCookieData,&cookieSize,&writable)==noErr&&cookieSize>0){
   if(cookieSize>65536)return @"audio_aac_configuration_limit";
   void *cookie=malloc(cookieSize);if(!cookie)return @"audio_decoder_memory_unavailable";
   OSStatus got=AudioFileGetProperty(_file,kAudioFilePropertyMagicCookieData,&cookieSize,cookie);
   if(got==noErr)got=AudioConverterSetProperty(_aac,kAudioConverterDecompressionMagicCookie,cookieSize,cookie);free(cookie);
   if(got!=noErr)return @"audio_aac_configuration_read_failed";
  }
  if(!ARAACRawPriming(_aac))return @"audio_aac_frames_unavailable";
  UInt32 maxPacket=0,packetSize=sizeof(maxPacket);
  if(AudioFileGetProperty(_file,kAudioFilePropertyMaximumPacketSize,&packetSize,&maxPacket)!=noErr||maxPacket==0||maxPacket>32768)return @"audio_aac_packet_limit";
  _packetCapacity=maxPacket*128;_packetBuffer=malloc(_packetCapacity);_packetDescriptions=calloc(128,sizeof(AudioStreamPacketDescription));
  if(!_packetBuffer||!_packetDescriptions)return @"audio_decoder_memory_unavailable";
  UInt32 sourceSize=sizeof(source);
  if(AudioConverterGetProperty(_aac,kAudioConverterCurrentInputStreamDescription,&sourceSize,&source)!=noErr||source.mSampleRate<=0)return @"audio_aac_clock_unavailable";
  _aacInputChannels=source.mChannelsPerFrame;
  _packetFrames=llround(source.mFramesPerPacket*rate/source.mSampleRate);
  if(_packetFrames<=0||_packetFrames>8192)return @"audio_aac_clock_unsupported";
  _first=[_plan[@"trim"][@"startFrames"] longLongValue];_total=[_plan[@"durationFrames"] longLongValue];
  // Converted AAC carries its new encoder delay in the output container, independently of
  // the original file's already-applied trim. Its presentation length remains the plan's.
  // ADTS (the server's AAC target under a bitrate limit, B-10.11) has no packet table: its
  // priming is the plan's trim (`aac-priming`, the converting encoder's own delay), kept above.
  if([_plan[@"mode"] isEqual:@"converted"]&&![_plan[@"container"] isEqual:@"adts"]){
   AudioFilePacketTableInfo table={0};UInt32 tableSize=sizeof(table);
   if(AudioFileGetProperty(_file,kAudioFilePropertyPacketTableInfo,&tableSize,&table)!=noErr||table.mPrimingFrames<0)return @"audio_aac_priming_unavailable";
   _first=llround(table.mPrimingFrames*rate/source.mSampleRate);
  }
  return [self seek:0];
 }
 // `trim` counts raw decoder output. Core Audio already drops a packet table's priming (AAC and
 // ALAC in MP4), so only the rest is skipped here, never twice (spec §18.1; the §18.8 fixtures).
 AudioFilePacketTableInfo table={0};UInt32 tableSize=sizeof(table);long long primed=0;
 if(AudioFileGetProperty(_file,kAudioFilePropertyPacketTableInfo,&tableSize,&table)==noErr){primed=MAX(0,table.mPrimingFrames);_remainder=table.mRemainderFrames;}
 _primed=primed;SInt64 length=0;UInt32 lengthSize=sizeof(length);if(ExtAudioFileGetProperty(_ext,kExtAudioFileProperty_FileLengthFrames,&lengthSize,&length)==noErr)_fileFrames=length;
 long long start=[_plan[@"trim"][@"startFrames"] longLongValue];_total=[_plan[@"durationFrames"] longLongValue];
 // Never assume Apple's default AAC priming matches an encoder's edit list. Reconcile the
 // packet table and length against the server's raw window. A contradictory decoder cannot
 // recover already-discarded real samples: give this file to ordinary playback instead.
 long long end=[_plan[@"trim"][@"endFrames"] longLongValue];
 long long raw=start+_total+end;
 if(fabs(source.mSampleRate-[_plan[@"sampleRate"] doubleValue])>.01)return @"audio_clock_plan_mismatch";
 if(_fileFrames==_total)_first=0;
 else {
  if(primed>start)return @"audio_priming_plan_mismatch";
  _first=start-primed;
  if(_fileFrames>0&&(_first+_total>_fileFrames||_fileFrames>raw))return @"audio_length_plan_mismatch";
 }

 return [self seek:0];
}
/**
 * Reads the plan's seek index once (`GET …/audio-index`, direct MP3 and ADTS only), on this deck's
 * queue. `{version: 1, offsets, frames}`, both rising, at most 4096 entries (client-core's
 * `parseSeekIndex`). Without one the file is placed by Core Audio as before.
 */
- (BOOL)loadIndex {
 if(_indexTried)return _offsets!=nil;
 _indexTried=YES;
 NSString *container=_plan[@"container"];
 if(![_plan[@"mode"] isEqual:@"direct"]||!([container isEqual:@"mp3"]||[container isEqual:@"adts"]))return NO;
 NSDictionary *named=APDictionary(_plan[@"seekIndex"]);NSString *path=[named[@"url"] isKindOfClass:NSString.class]?named[@"url"]:nil;
 NSURL *media=[NSURL URLWithString:_plan[@"url"]];NSURL *url=path.length&&path.length<=4096?[NSURL URLWithString:path relativeToURL:media].absoluteURL:nil;
 if(!media||!url||!APSameOrigin(url,media))return NO;
 NSURLSessionConfiguration *configuration=NSURLSessionConfiguration.ephemeralSessionConfiguration;configuration.URLCache=nil;configuration.HTTPCookieStorage=nil;configuration.URLCredentialStorage=nil;configuration.HTTPShouldSetCookies=NO;configuration.timeoutIntervalForRequest=4;
 NSURLSession *session=[NSURLSession sessionWithConfiguration:configuration delegate:[ARIndexFetch new] delegateQueue:nil];
 __block NSData *body=nil;dispatch_semaphore_t done=dispatch_semaphore_create(0);
 [[session dataTaskWithURL:url completionHandler:^(NSData *data,NSURLResponse *response,NSError *error){
  if(!error&&[response isKindOfClass:NSHTTPURLResponse.class]&&((NSHTTPURLResponse *)response).statusCode==200&&data.length<=(1<<20))body=data;
  dispatch_semaphore_signal(done);
 }] resume];
 dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,(int64_t)(5*NSEC_PER_SEC)));
 [session invalidateAndCancel];
 NSDictionary *json=body?APDictionary([NSJSONSerialization JSONObjectWithData:body options:0 error:nil]):nil;
 NSArray *offsets=[json[@"offsets"] isKindOfClass:NSArray.class]?json[@"offsets"]:nil,*frames=[json[@"frames"] isKindOfClass:NSArray.class]?json[@"frames"]:nil;
 if(![json[@"version"] isEqual:@1]||!offsets.count||offsets.count>4096||offsets.count!=frames.count)return NO;
 for(NSUInteger i=0;i<offsets.count;i++){
  if(!APInteger(offsets[i],0,9007199254740991.0)||!APInteger(frames[i],0,9007199254740991.0))return NO;
  if(i>0&&([offsets[i] longLongValue]<=[offsets[i-1] longLongValue]||[frames[i] longLongValue]<[frames[i-1] longLongValue]))return NO;
 }
 _offsets=offsets;_frames=frames;return YES;
}
- (void)leaveIndex {
 if(_ix){AudioConverterDispose(_ix);_ix=NULL;}
 if(_shifted){AudioFileClose(_shifted);_shifted=NULL;}
 free(_ixBuffer);_ixBuffer=NULL;free(_ixDescriptions);_ixDescriptions=NULL;
}
/**
 * Places a part-way position by the index: the last entry at or before the raw frame (one earlier
 * when that leaves the decoder fewer packets than it needs to warm up: MP3 3, AAC 2, as the web's),
 * a fresh decoder over the bytes from that entry, and the frames up to the position dropped. Only
 * the bytes from the entry on are fetched. NO when there is no index or it can't be used here.
 */
- (BOOL)seekIndexed:(long long)frame {
 if(![self loadIndex])return NO;
 BOOL mp3=[_plan[@"container"] isEqual:@"mp3"];
 long long raw=[_plan[@"trim"][@"startFrames"] longLongValue]+frame;
 NSUInteger lo=0,hi=_frames.count-1;
 while(lo<hi){NSUInteger mid=(lo+hi+1)/2;if([_frames[mid] longLongValue]<=raw)lo=mid;else hi=mid-1;}
 long long preroll=mp3?3*1152:2*1024;
 if(lo>0&&raw-[_frames[lo] longLongValue]<preroll)lo--;
 if([_frames[lo] longLongValue]>raw)return NO;
 [self leaveIndex];
 _base=[_offsets[lo] longLongValue];
 if(AudioFileOpenWithCallbacks((__bridge void *)self,ARShiftedRead,NULL,ARShiftedSize,NULL,[ARDecoder hint:_plan],&_shifted)!=noErr){_shifted=NULL;return NO;}
 AudioStreamBasicDescription source={0};UInt32 size=sizeof(source);
 if(AudioFileGetProperty(_shifted,kAudioFilePropertyDataFormat,&size,&source)!=noErr||source.mSampleRate<=0||source.mFramesPerPacket==0){[self leaveIndex];return NO;}
 AudioStreamBasicDescription client=*_format.streamDescription;
 if(AudioConverterNew(&source,&client,&_ix)!=noErr){_ix=NULL;[self leaveIndex];return NO;}
 UInt32 prime=kConverterPrimeMethod_None;
 if(AudioConverterSetProperty(_ix,kAudioConverterPrimeMethod,sizeof(prime),&prime)!=noErr){[self leaveIndex];return NO;}
 UInt32 cookieSize=0,writable=0;
 if(AudioFileGetPropertyInfo(_shifted,kAudioFilePropertyMagicCookieData,&cookieSize,&writable)==noErr&&cookieSize>0&&cookieSize<=65536){
  void *cookie=malloc(cookieSize);
  if(cookie&&AudioFileGetProperty(_shifted,kAudioFilePropertyMagicCookieData,&cookieSize,cookie)==noErr)AudioConverterSetProperty(_ix,kAudioConverterDecompressionMagicCookie,cookieSize,cookie);
  free(cookie);
 }
 UInt32 maxPacket=0,packetSize=sizeof(maxPacket);
 if(AudioFileGetProperty(_shifted,kAudioFilePropertyMaximumPacketSize,&packetSize,&maxPacket)!=noErr||maxPacket==0||maxPacket>32768){[self leaveIndex];return NO;}
 _ixCapacity=maxPacket*128;_ixBuffer=malloc(_ixCapacity);_ixDescriptions=calloc(128,sizeof(AudioStreamPacketDescription));
 if(!_ixBuffer||!_ixDescriptions){[self leaveIndex];return NO;}
 _ixChannels=source.mChannelsPerFrame;_ixPacket=0;
 // The index counts raw frames at the file's packet clock (MP3 1152 or 576, ADTS 1024 per block);
 // a decoder that outputs at another rate (HE-AAC) is scaled to the plan's clock.
 double scale=client.mSampleRate/source.mSampleRate;
 _ixSkip=llround((raw-[_frames[lo] longLongValue])*scale);
 if(_ixSkip<0){[self leaveIndex];return NO;}
 _next=frame;return YES;
}
- (NSString *)seek:(long long)frame {
 frame=MAX(0,MIN(frame,_total));
 if(_opus){
  if(!ps_audio_rewind(_opus))return @"audio_opus_seek_failed";
  long long skip=_first+frame;AVAudioPCMBuffer *scratch=[[AVAudioPCMBuffer alloc]initWithPCMFormat:_format frameCapacity:4096];
  while(skip>0){int got=ps_audio_read(_opus,scratch.floatChannelData,(int)MIN(4096LL,skip));if(got<=0)return @"audio_opus_trim_unavailable";skip-=got;}
  _next=frame;return nil;
 }
 // A part-way position in a direct MP3 or ADTS file lands exactly by the server's index; Core
 // Audio alone estimates MP3 positions or reads every byte before them.
 if(frame>0&&[self seekIndexed:frame])return nil;
 [self leaveIndex];
 if(_aac){
  // Two complete packets warm the codec; discard their output plus the intra-packet offset.
  // Packet lookup is constant time and AudioFile reads the container's existing byte index.
  long long target=_first+frame;_packet=MAX(0,target/_packetFrames-2);_aacSkip=target-_packet*_packetFrames;
  if(AudioConverterReset(_aac)!=noErr)return @"audio_aac_seek_failed";
  _next=frame;return nil;
 }
 if(ExtAudioFileSeek(_ext,_first+frame)!=noErr)return @"audio_seek_failed";
 _next=frame;return nil;
}
- (AVAudioPCMBuffer *)read:(AVAudioFrameCount)frames error:(NSString **)error {
 long long left=_total-_next;if(left<=0)return nil;
 AVAudioFrameCount want=(AVAudioFrameCount)MIN((long long)frames,left);
 AVAudioPCMBuffer *buffer=[[AVAudioPCMBuffer alloc] initWithPCMFormat:_format frameCapacity:want];
 buffer.frameLength=want; // sizes the buffer list for the read
 UInt32 got=want;OSStatus status=noErr;
 if(_opus){int read=ps_audio_read(_opus,buffer.floatChannelData,(int)want);if(read<0){if(error)*error=@"audio_opus_packet_failed";return nil;}got=(UInt32)read;}
 else if(_aac&&!_ix){
  do {
   buffer.frameLength=want;got=want;
   status=AudioConverterFillComplexBuffer(_aac,ARAACInput,(__bridge void *)self,&got,buffer.mutableAudioBufferList,NULL);
   if(status!=noErr||got==0)break;
   UInt32 drop=(UInt32)MIN(_aacSkip,got);_aacSkip-=drop;
   if(drop)for(AVAudioChannelCount ch=0;ch<_format.channelCount;ch++)memmove(buffer.floatChannelData[ch],buffer.floatChannelData[ch]+drop,(got-drop)*sizeof(float));
   got-=drop;
  }while(got==0);
 }else if(_ix){
  do {
   buffer.frameLength=want;got=want;
   status=AudioConverterFillComplexBuffer(_ix,ARIndexInput,(__bridge void *)self,&got,buffer.mutableAudioBufferList,NULL);
   if(status!=noErr||got==0)break;
   UInt32 drop=(UInt32)MIN(_ixSkip,(long long)got);_ixSkip-=drop;
   if(drop)for(AVAudioChannelCount ch=0;ch<_format.channelCount;ch++)memmove(buffer.floatChannelData[ch],buffer.floatChannelData[ch]+drop,(got-drop)*sizeof(float));
   got-=drop;
  }while(got==0);
 }else status=ExtAudioFileRead(_ext,&got,buffer.mutableAudioBufferList);
 if(status!=noErr){if(error)*error=_bytes.failure?:@"audio_stream_read_failed";return nil;}
 if(got==0){_total=_next;return nil;} // the file ends before its plan says: stop cleanly
 buffer.frameLength=got;_next+=got;return buffer;
}
- (void)close {
 ps_audio_close(_opus);_opus=NULL;[self leaveIndex];
 if(_aac){AudioConverterDispose(_aac);_aac=NULL;}
 if(_ext){ExtAudioFileDispose(_ext);_ext=NULL;}
 if(_file){AudioFileClose(_file);_file=NULL;}
 free(_packetBuffer);_packetBuffer=NULL;free(_packetDescriptions);_packetDescriptions=NULL;
}
- (void)dealloc {[self close];}
@end

/** One track: its bytes, decoder, player node and the frames scheduled on it. */
@interface ARDeck : NSObject
@property(nonatomic,strong) NSDictionary *plan;
@property(nonatomic,copy) NSString *identity;
@property(nonatomic,strong) ARBytes *bytes;
@property(nonatomic,strong) ARDecoder *decoder;
@property(nonatomic,strong) dispatch_queue_t queue;
@property(nonatomic,strong) AVAudioPlayerNode *node;
@property(nonatomic,strong) AVAudioUnitTimePitch *speed;
@property(nonatomic,strong) AVAudioUnitEQ *equalizer;
@property(nonatomic) BOOL sharedNode;
@property(nonatomic) double gain;
@property(nonatomic) BOOL levelingSelected,levelingProcessed;
@property(nonatomic) long long origin;      // track frame at `nodeOffset`
@property(nonatomic) long long nodeOffset;  // node sample where this deck's frames begin
@property(nonatomic) long long scheduled;   // frames of this deck on the node
@property(nonatomic) long long played;      // frames of this deck played back
@property(nonatomic) long long total;
@property(nonatomic) double sampleRate;
@property(nonatomic) BOOL opened,opening,pumping,eof,ended,failed,closed,started,candidate,held;
@property(nonatomic,strong) NSMutableArray<AVAudioPCMBuffer *> *holding; // a candidate's first seconds, before commit
@property(nonatomic) long long heldFrames;
@property(nonatomic) long long planFrames; // the plan's durationFrames (exit test (a): decoded against planned)
@property(nonatomic) NSInteger seek;
@property(nonatomic) NSUInteger serial;
@property(nonatomic) NSUInteger failures;
@property(nonatomic) NSTimeInterval retryAfter;
@end
@implementation ARDeck @end

@interface PorticoFileRender : NSObject
@property(nonatomic,copy) void (^onFact)(NSDictionary *fact);
@property(nonatomic,readonly) BOOL active;
+ (NSString *)validate:(NSDictionary *)render source:(NSString *)source;
+ (BOOL)decodes:(NSDictionary *)plan;
- (void)applyCommand:(NSDictionary *)command;
- (NSDictionary *)snapshot;
- (double)position;
- (void)pauseOutput;
- (void)invalidate;
#if DEBUG
- (void)debugConfigurationChange;
#endif
@end
@implementation PorticoFileRender {
 AVAudioEngine *_engine;
#if DEBUG
 PorticoAudioCapture *_capture;
#endif
 ARDeck *_primary,*_candidate,*_incoming,*_retiring;
 NSDictionary *_command;NSString *_token,*_committing,*_boundary;NSTimer *_timer;
 BOOL _active,_paused,_endedSent,_configChanged;double _fadeSeconds,_rate,_heard;NSInteger _seekRevision;NSTimeInterval _lastFact;
 long long _incomingOutputStart;BOOL _incomingStartIsHost;
}
static BOOL ARPlan(NSDictionary *p,NSURL *source){
 if(!APDictionary(p)||![p[@"version"] isEqual:@2]||![@[@"direct",@"converted"] containsObject:p[@"mode"]]||![p[@"id"] isKindOfClass:NSString.class]||[p[@"id"] length]<1||[p[@"id"] length]>256)return NO;
 if(!PorticoAudioPlanFactsValid(p))return NO;
 if(!APInteger(p[@"sampleRate"],8000,768000)||!APInteger(p[@"channels"],1,32)||!APInteger(p[@"durationFrames"],1,9007199254740991.0)||!APDictionary(p[@"trim"])||!APInteger(p[@"trim"][@"startFrames"],0,9007199254740991.0)||!APInteger(p[@"trim"][@"endFrames"],0,9007199254740991.0))return NO;
 if(![PorticoFileRender decodes:p])return NO;
 NSURL *url=[NSURL URLWithString:p[@"url"]?:@""];if(!url||![@[@"http",@"https"] containsObject:url.scheme]||!APSameOrigin(url,source))return NO;
 if(p[@"gain"]&&!APDictionary(p[@"gain"]))return NO;
 for(NSString *k in @[@"trackDb",@"albumDb"])if(p[@"gain"][k]&&!APNumber(p[@"gain"][k],-60,30))return NO;
 for(NSString *k in @[@"trackPeak",@"albumPeak"])if(p[@"gain"][k]&&!APNumber(p[@"gain"][k],.000001,64))return NO;
 return YES;
}
/** The (container, codec) pairs this engine decodes; `audioDecode` declares only what passes the §18.8 fixtures. */
+ (BOOL)decodes:(NSDictionary *)plan {
 NSString *c=plan[@"container"],*k=plan[@"codec"];
 if(![c isKindOfClass:NSString.class]||![k isKindOfClass:NSString.class])return NO;
 if([@[@"wav",@"aiff"] containsObject:c])return [k hasPrefix:@"pcm"];
 NSDictionary *pairs=@{@"ogg":@[@"opus"],@"flac":@[@"flac"],@"mp4":@[@"aac",@"alac"],@"m4a":@[@"aac",@"alac"],@"mp3":@[@"mp3"],@"adts":@[@"aac"]};
 return [pairs[c] containsObject:k];
}
+ (NSString *)validate:(NSDictionary *)render source:(NSString *)source {
 NSURL *origin=[NSURL URLWithString:source?:@""];NSDictionary *settings=APDictionary(render[@"settings"]);
 if(!origin||!ARPlan(APDictionary(render[@"plan"]),origin))return @"audio_render_plan_invalid";
 if(!PorticoListeningEffectsValid(settings)||!APInteger(render[@"epoch"],0,9007199254740991.0)||!APNumber(render[@"startSeconds"],0,1e9))return @"audio_render_settings_invalid";
 for(NSString *key in @[@"prepared",@"committed"]){NSDictionary *p=APDictionary(render[key]);if(render[key]&&render[key]!=NSNull.null&&!p)return @"audio_prepared_invalid";if(p&&(!ARPlan(APDictionary(p[@"plan"]),origin)||!APID(p[@"token"])))return @"audio_prepared_binding_invalid";}
 return nil;
}
- (instancetype)init {if((self=[super init])){_seekRevision=-1;_rate=1;}return self;}
- (BOOL)active {return _active;}

#pragma mark Gains

/** Source normalization is separate from the sole device-local EQ preamp. */
+ (BOOL)hasLevelingGain:(NSDictionary *)plan settings:(NSDictionary *)settings {
 return PorticoAudioHasLocalLeveling(plan,settings);
}
+ (double)gainFor:(NSDictionary *)plan settings:(NSDictionary *)settings {
 return PorticoAudioGainLinear(plan,settings);
}

#pragma mark Decks

- (ARDeck *)deck:(NSDictionary *)plan at:(long long)frame candidate:(BOOL)candidate {
 ARDeck *d=[ARDeck new];d.plan=plan;d.identity=plan[@"id"];d.origin=frame;d.total=[plan[@"durationFrames"] longLongValue];d.sampleRate=[plan[@"sampleRate"] doubleValue];d.seek=-1;d.candidate=candidate;d.holding=[NSMutableArray new];
 d.gain=(double)(float)[PorticoFileRender gainFor:plan settings:_command[@"audioRender"][@"settings"]];
 d.levelingSelected=[PorticoFileRender hasLevelingGain:plan settings:_command[@"audioRender"][@"settings"]];
 NSString *url=plan[@"url"];BOOL converted=[plan[@"mode"] isEqual:@"converted"];
 if(converted&&frame>0)url=[url stringByAppendingFormat:@"%@fromFrame=%lld",[url containsString:@"?"]?@"&":@"?",frame];
 long long size=converted?-1:[plan[@"bytes"] longLongValue];
 long long limit=candidate?MAX(0,[plan[@"prefetchBytes"] longLongValue]):0;
 d.bytes=[[ARBytes alloc] initWithURL:[NSURL URLWithString:url] key:ARHex([NSString stringWithFormat:@"%@|%@|%lld",plan[@"id"],converted?url:@"",size]) size:size limit:limit];
 d.queue=dispatch_queue_create("tv.getportico.audio.deck",DISPATCH_QUEUE_SERIAL);
 return d;
}
- (void)attach:(ARDeck *)d format:(AVAudioFormat *)format {
 d.node=[AVAudioPlayerNode new];d.equalizer=[[AVAudioUnitEQ alloc]initWithNumberOfBands:10];
 [_engine attachNode:d.node];[_engine attachNode:d.equalizer];
 if(format.channelCount<=2&&fabs(_rate-1)>.00001){d.speed=[AVAudioUnitTimePitch new];d.speed.rate=_rate;[_engine attachNode:d.speed];[_engine connect:d.node to:d.speed format:format];[_engine connect:d.speed to:d.equalizer format:format];}
 else [_engine connect:d.node to:d.equalizer format:format];
 [_engine connect:d.equalizer to:_engine.mainMixerNode format:format];
 [PorticoAudioProcessing.shared applyEqualizer:d.equalizer];
}
- (void)close:(ARDeck *)d {
 if(!d||d.closed)return;d.closed=YES;d.serial++;[d.bytes close];
 ARDecoder *decoder=d.decoder;dispatch_async(d.queue,^{[decoder close];});
 if(d.node&&!d.sharedNode){[d.node stop];[_engine detachNode:d.node];if(d.speed)[_engine detachNode:d.speed];[_engine detachNode:d.equalizer];}
 [d.holding removeAllObjects];d.heldFrames=0;
}
- (void)reset {[self close:_primary];[self close:_candidate];[self close:_incoming];[self close:_retiring];_primary=_candidate=_incoming=_retiring=nil;_token=_committing=_boundary=nil;_endedSent=NO;}
/** Opens and decodes on the deck's queue, a little at a time, as far as its target allows. */
- (void)pump:(ARDeck *)d {
 if(!d||d.closed||d.pumping||d.eof||d.failed||NSProcessInfo.processInfo.systemUptime<d.retryAfter)return;
 double target=d.candidate&&!d.held?ARPrepareSeconds:ARDecodeAhead;
 double ahead=d.candidate&&!d.held?d.heldFrames/d.sampleRate:(d.scheduled-d.played)/MAX(1.0,d.sampleRate);
 if(d.opened&&ahead>=target)return;
 d.pumping=YES;NSUInteger serial=d.serial;BOOL open=!d.opened;long long start=d.origin;__weak PorticoFileRender *weakSelf=self;__weak ARDeck *weakDeck=d;
 ARDecoder *decoder=d.decoder?:[[ARDecoder alloc] initWithBytes:d.bytes plan:d.plan];d.decoder=decoder;
 BOOL converted=[d.plan[@"mode"] isEqual:@"converted"];
 dispatch_async(d.queue,^{
  NSString *error=nil;
  if(open){error=[decoder open];if(!error&&start>0&&!converted)error=[decoder seek:start];}
  NSMutableArray *buffers=[NSMutableArray new];BOOL end=NO;
  double frames=0,budget=MAX(0,target-ahead)*decoder.format.sampleRate;
  while(!error&&frames<MAX(budget,4096)&&frames<decoder.format.sampleRate*4){
   AVAudioPCMBuffer *b=[decoder read:8192 error:&error];if(!b){end=!error;break;}
   [buffers addObject:b];frames+=b.frameLength;
  }
  dispatch_async(dispatch_get_main_queue(),^{PorticoFileRender *self=weakSelf;ARDeck *deck=weakDeck;if(!self||!deck||deck.closed||deck.serial!=serial)return;
   deck.pumping=NO;
   if(error){
#if DEBUG
    NSLog(@"Portico audio decoder diagnostic: %@",error);
#endif
    deck.failures++;deck.retryAfter=NSProcessInfo.processInfo.systemUptime+MIN(8.0,.5*pow(2,MIN(4,deck.failures-1)));
    if(deck.failures>=4||deck.candidate){deck.failed=YES;[self emit:deck.candidate?@"audioPreparationFailed":@"audioRenderFailed" extra:@{@"error":error}];}
    else{deck.opened=deck.opened||!open;if(open)deck.decoder=nil;}
    return;}
   deck.failures=0;if(open){deck.opened=YES;deck.total=decoder.total;deck.planFrames=[deck.plan[@"durationFrames"] longLongValue];}
   for(AVAudioPCMBuffer *b in buffers)[self deliver:b deck:deck];
   if(end){deck.eof=YES;deck.total=MIN(deck.total,deck.origin+deck.scheduled+deck.heldFrames);}
   [self tick];
  });
 });
}
/** Gain goes into the samples, so a gapless join switches it at exactly the right frame. */
- (void)deliver:(AVAudioPCMBuffer *)b deck:(ARDeck *)d {
 if((float)d.gain!=1.0f){float g=(float)d.gain;for(AVAudioChannelCount c=0;c<b.format.channelCount;c++){float *x=b.floatChannelData[c];for(AVAudioFrameCount i=0;i<b.frameLength;i++)x[i]*=g;}}
 if(b.frameLength&&d.levelingSelected)d.levelingProcessed=YES;
 if(d.candidate&&!d.held){[d.holding addObject:b];d.heldFrames+=b.frameLength;return;}
 [self schedule:b deck:d];
}
- (void)schedule:(AVAudioPCMBuffer *)b deck:(ARDeck *)d {
 if(!d.node)[self attach:d format:b.format];
 d.scheduled+=b.frameLength;NSUInteger serial=d.serial;AVAudioFrameCount length=b.frameLength;__weak PorticoFileRender *weakSelf=self;__weak ARDeck *weakDeck=d;
 [d.node scheduleBuffer:b atTime:nil options:0 completionCallbackType:AVAudioPlayerNodeCompletionDataPlayedBack completionHandler:^(AVAudioPlayerNodeCompletionCallbackType type){
  dispatch_async(dispatch_get_main_queue(),^{PorticoFileRender *self=weakSelf;ARDeck *deck=weakDeck;if(!self||!deck||deck.closed||deck.serial!=serial)return;deck.played+=length;if(deck.eof&&deck.played>=deck.scheduled)deck.ended=YES;[self tick];});
 }];
}
- (double)deckPosition:(ARDeck *)d {
 if(!d)return 0;
 AVAudioTime *node=d.node.lastRenderTime,*player=ARPlayerTime(d.node,node);
 double frames=player&&player.isSampleTimeValid?MAX(0,(double)player.sampleTime-d.nodeOffset):0;
 frames=MIN(frames,(double)d.scheduled);
 return (d.origin+frames)/MAX(1.0,d.sampleRate);
}
- (double)position {
 // Where the listener is: the primary's clock, or, while it can't be read (the engine restarting
 // after a route change), the last position it gave.
 AVAudioTime *n=_primary.node.lastRenderTime,*p=ARPlayerTime(_primary.node,n);
 if(p.isSampleTimeValid)_heard=[self deckPosition:_primary];
 return _primary?_heard:0;
}
- (void)startPrimary {
 if(!_primary||!_primary.node||_primary.scheduled<=0)return;
 if(!_primary.started&&_primary.scheduled<ARStartSeconds*_primary.sampleRate&&!_primary.eof)return;
 // A paused seek is complete when its decoded head is scheduled at the requested frame.
 // Waiting for the node to play leaves the listening owner's seek pending until Resume.
 if(_paused){
  if(_primary.seek>=0){NSInteger seek=_primary.seek;_primary.seek=-1;[self emit:@"seekApplied" extra:@{@"seekRevision":@(seek),@"position":@(_primary.origin/_primary.sampleRate)}];}
  return;
 }
 NSError *error=nil;
 if(!_engine.running&&![_engine startAndReturnError:&error]){
#if DEBUG
  NSLog(@"Portico audio output diagnostic: %@",error);
#endif
  [self emit:@"audioRenderFailed" extra:@{@"error":@"audio_output_unavailable"}];return;
 }
#if DEBUG
 if(!_capture){_capture=[PorticoAudioCapture new];[_capture start:_engine.mainMixerNode];}
#endif
 if(!_primary.node.playing)[_primary.node play];
 if(!_primary.started){_primary.started=YES;[self emit:@"ready" extra:@{}];
  if(_primary.seek>=0){NSInteger seek=_primary.seek;_primary.seek=-1;[self emit:@"seekApplied" extra:@{@"seekRevision":@(seek),@"position":@(_primary.origin/_primary.sampleRate)}];}}
}

#pragma mark The successor

/** The committed candidate goes on air: appended to the current node (gapless, same format), or on
 * its own node at the output sample where it must start (crossfade, another format). */
- (void)scheduleIncoming {
 ARDeck *next=_candidate;NSDictionary *committed=APDictionary(_command[@"audioRender"][@"committed"]);
 if(!next||_paused||!_primary||!_primary.eof||!committed||![committed[@"token"] isEqual:_token]||next.heldFrames<=0)return;
 if(_primary.scheduled+_primary.origin<_primary.total)return; // the current deck isn't fully on the node yet
 // Strict deliberately ends this deck before the listening owner promotes its committed
 // successor through the ordinary start path. That path requests the next file's rate even
 // when the two rates match. Never append buffers across a session reconfiguration.
 if([PorticoAudioProcessing.shared.sampleRateMatching isEqual:@"strict"])return;
 NSDictionary *settings=_command[@"audioRender"][@"settings"];
 BOOL sameAlbum=[committed[@"sameAlbum"] boolValue];
 double requested=PorticoAudioTransitionSeconds(_primary.plan,next.plan,[settings[@"crossfadeSeconds"] doubleValue],sameAlbum,[settings[@"smoothTransitions"] isEqual:@YES]);
 double shorter=MIN(_primary.total/_primary.sampleRate,next.total/next.sampleRate);
 double fade=sameAlbum?0:MIN(requested,shorter/2);
 AVAudioFormat *format=_primary.node?[_primary.node outputFormatForBus:0]:nil;AVAudioPCMBuffer *first=next.holding.firstObject;
 BOOL chain=fade<=0&&format&&first&&format.sampleRate==first.format.sampleRate&&format.channelCount==first.format.channelCount;
 [self close:_retiring];_retiring=nil;
 _candidate=nil;_incoming=next;_boundary=[_token copy];next.held=YES;next.candidate=NO;[next.bytes unlimit];[next.bytes readAll];_fadeSeconds=fade;
 if(chain){
  next.node=_primary.node;next.speed=_primary.speed;next.equalizer=_primary.equalizer;next.sharedNode=YES;next.nodeOffset=_primary.nodeOffset+_primary.scheduled;
  for(AVAudioPCMBuffer *b in next.holding)[self schedule:b deck:next];[next.holding removeAllObjects];next.heldFrames=0;
  return;
 }
 for(AVAudioPCMBuffer *b in next.holding)[self schedule:b deck:next];[next.holding removeAllObjects];next.heldFrames=0;
 // The output sample at which the current deck's last frame plays, from one render cycle's
 // (node time, player time) pair: no two clocks, no drift between reads.
 AVAudioTime *nodeTime=_primary.node.lastRenderTime,*player=ARPlayerTime(_primary.node,nodeTime);
 double outputRate=[_engine.outputNode outputFormatForBus:0].sampleRate;if(outputRate<=0)outputRate=nodeTime.sampleRate;
 double remaining=player.isSampleTimeValid?(_primary.nodeOffset+_primary.scheduled-(double)player.sampleTime)/_primary.sampleRate/MAX(.25,_rate):0;
 // Scheduled on host time (the wall clock both nodes share). A node's sample time runs at its
 // own playback rate behind the time-pitch unit, so at 1.25x a sample-time start came early
 // (the harness measured −575 ms; INT T15).
 double delay=MAX(0,remaining-fade);next.node.volume=fade>0?0:1;
 _incomingStartIsHost=NO;
 if(nodeTime.isHostTimeValid){uint64_t host=nodeTime.hostTime+[AVAudioTime hostTimeForSeconds:delay];_incomingOutputStart=(long long)host;_incomingStartIsHost=YES;[next.node playAtTime:[AVAudioTime timeWithHostTime:host]];}
 else if(nodeTime.isSampleTimeValid){long long at=nodeTime.sampleTime+(long long)llround(delay*MAX(.25,_rate)*nodeTime.sampleRate);_incomingOutputStart=at;[next.node playAtTime:[AVAudioTime timeWithSampleTime:at atRate:nodeTime.sampleRate]];}
 else [next.node play];
 next.started=YES;
}
- (void)boundary {
 ARDeck *old=_primary,*next=_incoming;NSString *token=_boundary;
 // Measured, not assumed: a chained successor's first frame against the last frame scheduled for
 // the old deck on the same node; a second node's start against the old deck's remaining audio,
 // both read from the players' own clocks at this render.
 long long gap=next.sharedNode?next.nodeOffset-(old.nodeOffset+old.scheduled):0,tail=old.total-(old.origin+old.scheduled);
 double overlap=0;BOOL chained=next.sharedNode;
 if(!next.sharedNode){AVAudioTime *n=old.node.lastRenderTime,*p=ARPlayerTime(old.node,n),*m=next.node.lastRenderTime,*q=ARPlayerTime(next.node,m);
  double remaining=p.isSampleTimeValid?(old.nodeOffset+old.scheduled-(double)p.sampleTime)/old.sampleRate:0,elapsed=q.isSampleTimeValid?(double)q.sampleTime/next.sampleRate:0;
  overlap=(MAX(0,remaining)+elapsed)/MAX(.25,_rate);
  // On host time when the start was: the old deck's end, predicted from its own clock at this
  // render, less the host time the new deck was started at. A time-pitch unit primes its input
  // at a start, so the new player's own sample time runs ahead there and can't be used at 1.25x.
  if(_incomingStartIsHost&&n.isHostTimeValid&&p.isSampleTimeValid)overlap=[AVAudioTime secondsForHostTime:n.hostTime]+MAX(0,remaining)/MAX(.25,_rate)-[AVAudioTime secondsForHostTime:(uint64_t)_incomingOutputStart];}
 _primary=next;_incoming=nil;_boundary=nil;_token=nil;_committing=nil;_endedSent=NO;next.started=YES;
 if(next.sharedNode){old.sharedNode=YES;[self close:old];_retiring=nil;next.sharedNode=NO;} // the node is the new primary's now
 else _retiring=old;
 [self emit:@"audioBoundary" extra:@{@"token":token?:@"",@"position":@([self position]),@"gapFrames":@(gap),@"tailFrames":@(tail),@"chained":@(chained),@"fadeSeconds":@(_fadeSeconds),@"overlapSeconds":@(overlap),@"planFrames":@(old.planFrames),@"decodedFrames":@(old.origin+old.scheduled)}];
}

#pragma mark Commands

- (void)applyCommand:(NSDictionary *)command {
 NSDictionary *render=APDictionary(command[@"audioRender"]);if(!render){[self invalidate];return;}
 if(!_engine){_engine=[AVAudioEngine new];
  [NSNotificationCenter.defaultCenter removeObserver:self name:PorticoAudioProcessingChanged object:nil];
  [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(processingChanged:) name:PorticoAudioProcessingChanged object:nil];[NSNotificationCenter.defaultCenter addObserver:self selector:@selector(configurationChanged:) name:AVAudioEngineConfigurationChangeNotification object:_engine];
  [NSNotificationCenter.defaultCenter removeObserver:self name:AVAudioSessionMediaServicesWereResetNotification object:nil];
  [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(mediaServicesReset:) name:AVAudioSessionMediaServicesWereResetNotification object:nil];}
 NSDictionary *pending=APDictionary(command[@"pendingSeek"]),*plan=render[@"plan"];double rate=[command[@"rate"] doubleValue];if(!isfinite(rate)||rate<=0)rate=1;
 BOOL changed=!_primary||![_primary.identity isEqual:plan[@"id"]],seek=pending&&[pending[@"revision"] integerValue]!=_seekRevision;
 double oldGain=[PorticoFileRender gainFor:plan settings:_command[@"audioRender"][@"settings"]];
 BOOL wasPaused=_paused;_command=[command copy];_paused=[command[@"paused"] boolValue];_active=YES;
 if([plan[@"channels"] unsignedIntegerValue]>2&&fabs(rate-1)>.00001){[self emit:@"rateStatus" extra:@{@"rateError":@"audio_multichannel_rate_unavailable"}];rate=1;}
 if(fabs(_rate-rate)>.00001){_rate=rate;_configChanged=YES;for(ARDeck *d in @[_primary?:NSNull.null,_incoming?:NSNull.null,_retiring?:NSNull.null]){if((id)d==NSNull.null)continue;d.speed.rate=rate;d.speed.bypass=fabs(rate-1)<.00001;}}
 BOOL regain=!changed&&_primary&&fabs([PorticoFileRender gainFor:plan settings:render[@"settings"]]-oldGain)>.000001;
 double position=seek?[pending[@"positionSeconds"] doubleValue]:changed?[render[@"startSeconds"] doubleValue]:[self position];
 if(changed||_configChanged){
  [_engine stop];
  NSError *sessionError=nil;
  if(![PorticoAudioProcessing.shared configureRate:[plan[@"sampleRate"] doubleValue] channels:[plan[@"channels"] unsignedIntValue] error:&sessionError]){[self pauseOutput];[self emit:@"audioRenderFailed" extra:@{@"error":@"audio_route_unavailable"}];return;}
 }
 if(changed||seek||regain||_configChanged){
  // A route or format change mid-edge: the committed successor comes back as a buffered candidate
  // under the same token, scheduled again when the rebuilt primary reaches its end.
  NSDictionary *keep=_configChanged&&!changed&&!seek?APDictionary(render[@"committed"]):nil;
  _configChanged=NO;[self close:_primary];[self close:_incoming];[self close:_retiring];_incoming=_retiring=nil;_endedSent=NO;
  if(keep&&!_candidate){_candidate=[self deck:keep[@"plan"] at:0 candidate:YES];[_candidate.bytes unlimit];[_candidate.bytes readAll];_token=[keep[@"token"] copy];_committing=[keep[@"token"] copy];[self pump:_candidate];}
  if(changed&&_candidate&&![_candidate.identity isEqual:APDictionary(render[@"prepared"])[@"plan"][@"id"]]){[self close:_candidate];_candidate=nil;_token=nil;}
  long long frame=(long long)llround(MAX(0,position)*[plan[@"sampleRate"] doubleValue]);
  _primary=[self deck:plan at:MIN(frame,[plan[@"durationFrames"] longLongValue]) candidate:NO];_heard=MAX(0,position);
  _primary.seek=seek?[pending[@"revision"] integerValue]:-1;if(seek)_seekRevision=_primary.seek;
  [self pump:_primary];
 }
 NSDictionary *prepared=APDictionary(render[@"prepared"]),*committed=APDictionary(render[@"committed"]);
 if(prepared&&![prepared[@"token"] isEqual:_token]){[self close:_candidate];_token=[prepared[@"token"] copy];_committing=nil;_candidate=[self deck:prepared[@"plan"] at:0 candidate:YES];[self pump:_candidate];}
 else if(!prepared&&!committed&&!_incoming){[self close:_candidate];_candidate=nil;_token=nil;_committing=nil;}
 if(_paused)[self pauseOutput];
 else{
  [self startPrimary];
  if(wasPaused){if(_retiring&&!_retiring.ended)[_retiring.node play];if(_incoming&&!_incoming.sharedNode&&_incoming.started)[_incoming.node play];}
  if(committed&&[_token isEqual:committed[@"token"]]&&!_incoming)[self scheduleIncoming];
 }
 if(!_timer&&!_paused){__weak PorticoFileRender *weakSelf=self;_timer=[NSTimer timerWithTimeInterval:.01 repeats:YES block:^(NSTimer *t){[weakSelf tick];}];[NSRunLoop.mainRunLoop addTimer:_timer forMode:NSRunLoopCommonModes];}
 [self emit:@"rateStatus" extra:@{}];
}
- (void)processingChanged:(NSNotification *)note {
 for(ARDeck *d in @[_primary?:NSNull.null,_incoming?:NSNull.null,_retiring?:NSNull.null]){if((id)d!=NSNull.null&&d.equalizer)[PorticoAudioProcessing.shared applyEqualizer:d.equalizer];}
 if((note.userInfo[@"sampleRateMatching"]||note.userInfo[@"routeChanged"])&&_active&&_command){_configChanged=YES;[self applyCommand:_command];}
}
#if DEBUG
/** The harness's stand-in for a route change: the engine stops and says its configuration changed. */
- (void)debugConfigurationChange {[_engine stop];[NSNotificationCenter.defaultCenter postNotificationName:AVAudioEngineConfigurationChangeNotification object:_engine];}
#endif
/** The media services restarted: every node and the engine itself are gone. A new engine, the
 * decks rebuilt where the listener was (INT T7). */
- (void)mediaServicesReset:(NSNotification *)note {
 dispatch_async(dispatch_get_main_queue(),^{
  double position=[self position];
  [NSNotificationCenter.defaultCenter removeObserver:self name:AVAudioEngineConfigurationChangeNotification object:self->_engine];
  [self reset];self->_engine=nil;self->_heard=position;
  if(!self->_active||!self->_command)return;
  NSMutableDictionary *again=[self->_command mutableCopy];self->_configChanged=YES;[self applyCommand:again];});
}
- (void)configurationChanged:(NSNotification *)note {
 dispatch_async(dispatch_get_main_queue(),^{if(!self->_active||!self->_command)return;
  // The output changed (a route, a sample rate): nodes stopped. Pick up where the listener is.
  NSMutableDictionary *again=[self->_command mutableCopy];self->_configChanged=YES;[self applyCommand:again];});
}
- (void)pauseOutput {
 _paused=YES;[_primary.node pause];if(_incoming&&!_incoming.sharedNode)[_incoming.node pause];[_retiring.node pause];
 // Nothing moves while paused: no 100 Hz loop (INT T13). The next command starts it again.
 [_timer invalidate];_timer=nil;
}
- (void)invalidate {
#if DEBUG
 [_capture stop];_capture=nil;
#endif
 _active=NO;[_timer invalidate];_timer=nil;[self reset];_command=nil;_seekRevision=-1;[_engine stop];}

#pragma mark The loop

- (void)tick {
 if(!_active||!_primary||_primary.closed)return;
 [self pump:_primary];[self pump:_candidate];[self pump:_incoming];
 if(_candidate&&!_candidate.held&&_candidate.opened&&!_committing&&(_candidate.heldFrames>=ARPrepareSeconds*_candidate.sampleRate||_candidate.eof)&&!_candidate.started){_candidate.started=YES;[self emit:@"audioPrepared" extra:@{@"token":_token?:@""}];}
 [self startPrimary];
 // The boundary: a chained successor's first frame on the shared node, or the second node's first render.
 if(_incoming&&!_paused){
  BOOL crossed=NO;
  if(_incoming.sharedNode){AVAudioTime *n=_incoming.node.lastRenderTime,*p=ARPlayerTime(_incoming.node,n);crossed=p.isSampleTimeValid&&p.sampleTime>=_incoming.nodeOffset;}
  else{AVAudioTime *n=_incoming.node.lastRenderTime,*p=ARPlayerTime(_incoming.node,n);crossed=p.isSampleTimeValid&&p.sampleTime>0;}
  if(crossed)[self boundary];
 }
 if(_retiring){
  double elapsed=MAX(0,[self position]-_primary.origin/_primary.sampleRate)/MAX(.25,_rate),t=_fadeSeconds>0?MIN(1,elapsed/_fadeSeconds):1;
  _primary.node.volume=(float)sin(t*M_PI_2);_retiring.node.volume=(float)cos(t*M_PI_2);
  if(t>=1||_retiring.ended){[self close:_retiring];_retiring=nil;_primary.node.volume=1;}
 }
 if(!_paused){
  double remaining=(_primary.total/_primary.sampleRate-[self position])/MAX(.25,_rate);
  double fade=[_command[@"audioRender"][@"settings"][@"crossfadeSeconds"] doubleValue];
  if(_primary.eof&&_candidate.started&&_token&&!_committing&&!APDictionary(_command[@"audioRender"][@"committed"])&&remaining<=fade+2){_committing=[_token copy];[self emit:@"audioCommit" extra:@{@"token":_token}];}
  NSDictionary *committed=APDictionary(_command[@"audioRender"][@"committed"]);
  if(committed&&[_token isEqual:committed[@"token"]]&&!_incoming)[self scheduleIncoming];
  NSTimeInterval now=NSProcessInfo.processInfo.systemUptime;if(now-_lastFact>=.25){_lastFact=now;
   BOOL strict=[PorticoAudioProcessing.shared.sampleRateMatching isEqual:@"strict"];
   BOOL holding=!strict&&APDictionary(_command[@"audioRender"][@"prepared"])&&_committing;
   if(_primary.ended&&!_incoming&&!holding){if(!_endedSent){_endedSent=YES;[self emit:@"ended" extra:@{}];}}
   else if(_primary.started)[self emit:@"progress" extra:@{}];}
 }
}
- (void)emit:(NSString *)type extra:(NSDictionary *)extra {
 if(!_active||!self.onFact)return;
 NSDictionary *status=[self snapshot];
 NSMutableDictionary *fact=[@{@"type":type,@"position":status[@"positionSeconds"],@"paused":@(_paused),@"monotonicSeconds":@(NSProcessInfo.processInfo.systemUptime)} mutableCopy];
 for(NSString *key in @[@"effectiveRate",@"supportedRates",@"rateReady",@"rateWaiting",@"rateError",@"eqApplied",@"levelingApplied"])fact[key]=status[key];
 if(status[@"levelingGainDb"])fact[@"levelingGainDb"]=status[@"levelingGainDb"];
 [fact addEntriesFromDictionary:extra];
 // Decoder/OS diagnostic prose never becomes UI copy.
 if([fact[@"error"] isKindOfClass:NSString.class]&&[fact[@"error"] length])fact[@"error"]=[type isEqual:@"audioPreparationFailed"]?@"audio_preparation_failed":@"audio_render_failed";
 if([fact[@"rateError"] isKindOfClass:NSString.class]&&[fact[@"rateError"] length])fact[@"rateError"]=@"audio_rate_unavailable";
 self.onFact(fact);
}
- (NSDictionary *)snapshot {
 BOOL ready=_active&&_primary&&_primary.started;
 BOOL waiting=_active&&_primary&&!_primary.eof&&_primary.scheduled<=_primary.played;
 BOOL running=ready&&!_paused&&!_primary.ended&&_engine.running&&_primary.node.playing;
 NSMutableDictionary *facts=[@{@"positionSeconds":@([self position]),@"ready":@(ready),@"observed":@(_primary.started),@"seeking":@((BOOL)(_primary&&_primary.seek>=0)),
  @"effectiveRate":@(running?_rate:0),@"supportedRates":@[@0.5,@0.75,@1,@1.25,@1.5,@1.75,@2,@2.5,@3],@"rateReady":@(ready),@"rateWaiting":@((BOOL)(waiting&&!_paused)),@"rateError":@"",@"rendering":@(_active)
#if DEBUG
  // Exit test (j): decoded audio held per deck (scheduled but not yet played, or a candidate's head).
  ,@"decodedAheadSeconds":@(_primary&&_primary.sampleRate>0?MAX(0,_primary.scheduled-_primary.played)/_primary.sampleRate:0)
  ,@"candidateHeldSeconds":@(_candidate&&_candidate.sampleRate>0?_candidate.heldFrames/_candidate.sampleRate:0)
#endif
 } mutableCopy];
#if DEBUG
 if(_capture)facts[@"outputCapture"]=[_capture snapshot];
#endif
 facts[@"supportedRates"]=[_primary.plan[@"channels"] unsignedIntegerValue]>2?@[@1]:facts[@"supportedRates"];
 facts[@"audioPath"]=[PorticoAudioProcessing.shared snapshot];
 AVAudioTime *renderTime=ARPlayerTime(_primary.node,_primary.node.lastRenderTime);
 BOOL heardCurrent=_active&&_primary.started&&!_primary.failed&&!_primary.ended&&!_endedSent&&renderTime.isSampleTimeValid&&renderTime.sampleTime>_primary.nodeOffset;
 facts[@"eqApplied"]=@(heardCurrent&&PorticoEqualizerApplied(_primary.equalizer,_engine));
 facts[@"levelingApplied"]=@(heardCurrent&&_primary.levelingProcessed);
 // This deck's Float32 coefficient was multiplied into its decoded buffers in
 // deliver:deck:. The node clock above proves those same buffers are now heard;
 // neither the requested plan nor the crossfade node volume is a measurement.
 if([facts[@"levelingApplied"] boolValue]&&isfinite(_primary.gain)&&_primary.gain>0){
  double db=20*log10(_primary.gain);
  if(isfinite(db)&&db>=-60&&db<=30)facts[@"levelingGainDb"]=@(db);
 }
 double frames=[_primary.plan[@"durationFrames"] doubleValue],rate=[_primary.plan[@"sampleRate"] doubleValue],bytes=[_primary.plan[@"bytes"] doubleValue];
 if(frames>0&&rate>0&&bytes>0)facts[@"containerAverageBitrateBps"]=@(llround(bytes*8*rate/frames));
 facts[@"sourceSampleRate"]=_primary.plan[@"sampleRate"]?:@0;facts[@"sourceChannels"]=_primary.plan[@"channels"]?:@0;
 facts[@"appliedGainLinear"]=@(_primary?_primary.gain:1);
 return facts;
}
- (void)dealloc {[_timer invalidate];[self reset];[NSNotificationCenter.defaultCenter removeObserver:self];}

#pragma mark Conformance (§18.8; Debug only)
#if DEBUG
/** Decodes every §18.8 fixture in `directory` exactly as a direct plan would, and checks frame
 * counts, first and last samples, and the album's joins, to the frame. */
+ (NSDictionary *)conformance:(NSString *)directory {
 NSMutableArray *results=[NSMutableArray new];NSMutableDictionary *album=[NSMutableDictionary new];
 NSArray *names=[[NSFileManager.defaultManager contentsOfDirectoryAtPath:directory error:nil] sortedArrayUsingSelector:@selector(compare:)];
 for(NSString *name in names){
  if(![name.pathExtension isEqual:@"json"])continue;
  NSDictionary *side=[NSJSONSerialization JSONObjectWithData:[NSData dataWithContentsOfFile:[directory stringByAppendingPathComponent:name]] options:0 error:nil];
  if(!APDictionary(side)||![side[@"file"] isKindOfClass:NSString.class]||![side[@"container"] isKindOfClass:NSString.class]||![side[@"codec"] isKindOfClass:NSString.class]||!APDictionary(side[@"trim"]))continue;
  NSString *container=[side[@"container"] isEqual:@"mp4"]?@"mp4":side[@"container"];
  NSDictionary *plan=@{@"version":@2,@"mode":@"direct",@"id":name,@"container":container,@"codec":side[@"codec"],@"sampleRate":side[@"sampleRate"],@"channels":side[@"channels"],@"durationFrames":side[@"durationFrames"],@"trim":side[@"trim"]};
  NSMutableDictionary *r=[@{@"file":side[@"file"],@"container":container,@"codec":side[@"codec"],@"expectedFrames":side[@"durationFrames"]} mutableCopy];
  if(![PorticoFileRender decodes:plan]){r[@"status"]=@"not-declared";[results addObject:r];continue;}
  ARBytes *bytes=[[ARBytes alloc] initWithURL:[NSURL fileURLWithPath:[directory stringByAppendingPathComponent:side[@"file"]]] key:@"" size:-1 limit:0];
  ARDecoder *decoder=[[ARDecoder alloc] initWithBytes:bytes plan:plan];
  NSString *error=[decoder open];long long frames=0;double worst=0,inner=0;long long worstAt=-1,expectedTotal=[side[@"durationFrames"] longLongValue],edge=side[@"edgeFrames"]?[side[@"edgeFrames"] longLongValue]:64;double lagError[7]={0};float first=NAN,last=NAN;
  double amplitude=[side[@"signal"][@"amplitude"] doubleValue],frequency=[side[@"signal"][@"frequency"] doubleValue],offset=[side[@"signal"][@"offset"] doubleValue],rate=[side[@"sampleRate"] doubleValue];
  while(!error){AVAudioPCMBuffer *b=[decoder read:8192 error:&error];if(!b)break;
   const float *x=b.floatChannelData[0];
   for(AVAudioFrameCount i=0;i<b.frameLength;i++){double expected=amplitude*sin(2*M_PI*frequency*(frames+i+offset)/rate);double e=fabs(x[i]-expected);if(e>worst){worst=e;worstAt=frames+i;}if(frames+i>=edge&&frames+i<expectedTotal-edge)inner=MAX(inner,e);
    for(int k=-3;k<=3;k++)lagError[k+3]+=fabs(x[i]-amplitude*sin(2*M_PI*frequency*(frames+i+k+offset)/rate));}
   if(isnan(first))first=x[0];last=x[b.frameLength-1];frames+=b.frameLength;}
  [decoder close];[bytes close];
  r[@"primed"]=@(decoder.primed);r[@"remainder"]=@(decoder.remainder);r[@"fileFrames"]=@(decoder.fileFrames);r[@"frames"]=@(frames);r[@"first"]=isnan(first)?(id)NSNull.null:@(first);r[@"last"]=isnan(last)?(id)NSNull.null:@(last);r[@"maxError"]=@(worst);r[@"maxErrorAt"]=@(worstAt);r[@"maxInnerError"]=@(inner);
  // Alignment, independent of a lossy codec's edges: the lag (in frames) that best fits the signal.
  int lag=0;for(int k=-3;k<=3;k++)if(lagError[k+3]<lagError[lag+3])lag=k;r[@"bestLag"]=@(lag);r[@"tolerance"]=side[@"tolerance"];
  // §18.8: exact frames, best-fit lag 0, and the error within `tolerance` except the first and last
  // `edgeFrames` of a lossy file, which a codec's cut-edge transients may take to `edgeTolerance`.
  double tolerance=[side[@"tolerance"] doubleValue],edgeTolerance=side[@"edgeTolerance"]?[side[@"edgeTolerance"] doubleValue]:(tolerance>=.001?.1:tolerance);
  r[@"edgeTolerance"]=@(edgeTolerance);
  r[@"status"]=error?error:(frames==expectedTotal&&lag==0&&inner<=tolerance&&worst<=edgeTolerance?@"pass":@"fail");
  [results addObject:r];
  if([name hasPrefix:@"album-"]){NSString *set=[name componentsSeparatedByString:@"-"][1];NSMutableArray *tracks=album[set]?:[NSMutableArray new];[tracks addObject:@{@"frames":@(frames),@"expected":side[@"durationFrames"],@"offset":side[@"signal"][@"offset"],@"status":r[@"status"]}];album[set]=tracks;}
 }
 // The joins: each track must start exactly where the previous one ended (the sine's phase
 // continues across the album), so the boundary error is the frame count error at each edge.
 NSMutableDictionary *joins=[NSMutableDictionary new];
 for(NSString *set in album){NSArray *tracks=album[set];long long worst=0;BOOL all=YES;
  for(NSUInteger i=1;i<tracks.count;i++){long long end=[tracks[i-1][@"offset"] longLongValue]+[tracks[i-1][@"frames"] longLongValue],start=[tracks[i][@"offset"] longLongValue];worst=MAX(worst,llabs(end-start));all=all&&[tracks[i][@"status"] isEqual:@"pass"];}
  joins[set]=@{@"tracks":@(tracks.count),@"worstBoundaryFrames":@(worst),@"allTracksPass":@(all)};}
 return @{@"fixtures":results,@"albums":joins};
}
#endif
@end

#pragma mark - The render facade

/** The controller's one render engine: a version 2 plan decodes on the device (PorticoFileRender).
 * The version 1 PCM-window plan is gone (FCORE-2): anything else is refused. */
@implementation PorticoAudioRender {
 PorticoFileRender *_file;
}
static BOOL ARVersion2(NSDictionary *render){return [APDictionary(render)[@"plan"][@"version"] isEqual:@2];}
+ (NSString *)validate:(NSDictionary *)render source:(NSString *)source binding:(NSDictionary *)binding {
 return ARVersion2(render)?[PorticoFileRender validate:render source:source]:@"audio_render_binding_invalid";
}
+ (BOOL)playable:(NSDictionary *)plan {NSDictionary *p=APDictionary(plan);return [p[@"version"] isEqual:@2]&&[@[@"direct",@"converted"] containsObject:p[@"mode"]]&&[p[@"url"] isKindOfClass:NSString.class]&&[PorticoFileRender decodes:p]&&APInteger(p[@"durationFrames"],1,9007199254740991.0)&&APInteger(p[@"sampleRate"],8000,768000);}
- (instancetype)init {if((self=[super init])){_file=[PorticoFileRender new];}return self;}
- (void)setOnFact:(void (^)(NSDictionary *))onFact {_onFact=[onFact copy];_file.onFact=_onFact;}
- (BOOL)active {return [_file active];}
- (double)position {return [_file position];}
/** A command without a version 2 plan stops the engine (the file engine invalidates itself). */
- (void)applyCommand:(NSDictionary *)command {
 NSMutableDictionary *next=[command mutableCopy];if(!ARVersion2(APDictionary(command[@"audioRender"])))next[@"audioRender"]=NSNull.null;
 [_file applyCommand:next];
}
- (NSDictionary *)snapshot {return [_file snapshot];}
- (void)pauseOutput {[_file pauseOutput];}
- (void)invalidate {[_file invalidate];}
+ (void)purgeCache {
 NSSet *active;@synchronized(ARActiveKeys()){active=[ARActiveKeys() copy];}
 NSURL *dir=ARCacheDirectory();
 for(NSURL *f in [NSFileManager.defaultManager contentsOfDirectoryAtURL:dir includingPropertiesForKeys:nil options:0 error:nil]){
  if(![active containsObject:[f.URLByDeletingPathExtension.lastPathComponent componentsSeparatedByString:@"~"].firstObject])[NSFileManager.defaultManager removeItemAtURL:f error:nil];
 }
}
#if DEBUG
static NSUInteger ARPrivateCopies(NSString *key){
 NSUInteger n=0;for(NSURL *f in [NSFileManager.defaultManager contentsOfDirectoryAtURL:ARCacheDirectory() includingPropertiesForKeys:nil options:0 error:nil])if([f.lastPathComponent hasPrefix:[key stringByAppendingString:@"~"]])n++;return n;
}
static NSData *ARDebugRead(ARBytes *bytes,long long offset,UInt32 length){
 NSMutableData *d=[NSMutableData dataWithLength:length];UInt32 got=0;if(![bytes read:d.mutableBytes offset:offset length:length actual:&got])return nil;d.length=got;return d;
}
+ (NSDictionary *)debugSharedReaders:(NSURL *)url {
 NSString *key=[@"t6-" stringByAppendingString:NSUUID.UUID.UUIDString];const UInt32 n=32768;NSMutableDictionary *r=[NSMutableDictionary new];
 // A is a prepared deck still inside its prefetch cap: 64 KB of the file, the rest missing.
 __block long long size=-1;dispatch_semaphore_t head=dispatch_semaphore_create(0);NSMutableURLRequest *probe=[NSMutableURLRequest requestWithURL:url];probe.HTTPMethod=@"HEAD";
 [[NSURLSession.sharedSession dataTaskWithRequest:probe completionHandler:^(NSData *data,NSURLResponse *response,NSError *error){size=response.expectedContentLength;dispatch_semaphore_signal(head);}] resume];
 dispatch_semaphore_wait(head,dispatch_time(DISPATCH_TIME_NOW,(int64_t)(10*NSEC_PER_SEC)));r[@"size"]=@(size);
 if(size<=4*n){r[@"pass"]=@NO;r[@"error"]=@"no size";return r;}
 ARBytes *a=[[ARBytes alloc] initWithURL:url key:key size:size limit:65536];
 NSData *a1=ARDebugRead(a,0,n);
 // B opens the same key while A is partial (repeat one; the committed deck on the playing track).
 ARBytes *b=[[ARBytes alloc] initWithURL:url key:key size:size limit:0];r[@"privateCopiesWhileBothOpen"]=@(ARPrivateCopies(key));
 NSData *a2=ARDebugRead(a,0,n),*b1=ARDebugRead(b,0,n),*bEnd=ARDebugRead(b,size-n,n);
 r[@"firstReaderIntact"]=@(a1.length==n&&[a1 isEqual:a2]);r[@"secondReaderMatches"]=@([b1 isEqual:a1]);
 [a unlimit];[a readAll];NSData *aEnd=ARDebugRead(a,size-n,n);r[@"tailsMatch"]=@(bEnd.length==n&&[aEnd isEqual:bEnd]);
 [b close];r[@"privateCopiesAfterClose"]=@(ARPrivateCopies(key));
 // A is whole now: a third reader shares its file read-only instead of copying it.
 NSString *done=[ARCacheDirectory() URLByAppendingPathComponent:[key stringByAppendingPathExtension:@"done"]].path;
 for(int i=0;i<50&&![NSFileManager.defaultManager fileExistsAtPath:done];i++)[NSThread sleepForTimeInterval:.1];
 ARBytes *c=[[ARBytes alloc] initWithURL:url key:key size:size limit:0];r[@"privateCopiesWhenWhole"]=@(ARPrivateCopies(key));
 r[@"sharedReaderMatches"]=@([ARDebugRead(c,0,n) isEqual:a1]);
 [a close];[c close];
 ARBytes *d=[[ARBytes alloc] initWithURL:url key:key size:size limit:0];r[@"reopenedMatches"]=@([ARDebugRead(d,0,n) isEqual:a1]&&[ARDebugRead(d,size-n,n) isEqual:aEnd]);[d close];
 for(NSString *ext in @[@"bin",@"done"])[NSFileManager.defaultManager removeItemAtURL:[ARCacheDirectory() URLByAppendingPathComponent:[key stringByAppendingPathExtension:ext]] error:nil];
 r[@"pass"]=@(size>n&&[r[@"firstReaderIntact"] boolValue]&&[r[@"secondReaderMatches"] boolValue]&&[r[@"tailsMatch"] boolValue]&&[r[@"privateCopiesWhileBothOpen"] integerValue]==1&&[r[@"privateCopiesAfterClose"] integerValue]==0&&[r[@"privateCopiesWhenWhole"] integerValue]==0&&[r[@"sharedReaderMatches"] boolValue]&&[r[@"reopenedMatches"] boolValue]);
 return r;
}
- (void)debugConfigurationChange {[_file debugConfigurationChange];}
#endif
#if DEBUG
// At launch, not when React Native first asks for the playback module (it may never, before sign-in).
+ (void)load {dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(2*NSEC_PER_SEC)),dispatch_get_main_queue(),^{[PorticoAudioRender runConformanceIfRequested];});}
+ (void)runConformanceIfRequested {
 static BOOL ran;if(ran)return;ran=YES;
 NSString *directory=NSProcessInfo.processInfo.environment[@"PORTICO_AUDIO_CONFORMANCE"],*out=NSProcessInfo.processInfo.environment[@"PORTICO_AUDIO_CONFORMANCE_OUT"];
 if(!directory.length||!out.length)return;
 dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
  NSDictionary *result=[PorticoFileRender conformance:directory];
  NSData *json=[NSJSONSerialization dataWithJSONObject:result options:NSJSONWritingPrettyPrinted|NSJSONWritingSortedKeys error:nil];
  [json writeToFile:out atomically:YES];
 });
}
#endif
@end
