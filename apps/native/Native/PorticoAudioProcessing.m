#import "PorticoAudioProcessing.h"
#import "PorticoEqualizer.h"
#import <sqlite3.h>

NSNotificationName const PorticoAudioProcessingChanged=@"PorticoAudioProcessingChanged";
static BOOL EQNumber(id n,double lo,double hi){return [n isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)n)!=CFBooleanGetTypeID()&&isfinite([n doubleValue])&&[n doubleValue]>=lo&&[n doubleValue]<=hi;}
static BOOL EQStep(id n){return EQNumber(n,-12,12)&&fabs([n doubleValue]*2-round([n doubleValue]*2))<.000001;}
static NSError *EQError(NSString *message){return [NSError errorWithDomain:@"PorticoAudio" code:1 userInfo:@{NSLocalizedDescriptionKey:message}];}

@implementation PorticoAudioProcessing {
 sqlite3 *_db;NSString *_mode;BOOL _enabled,_sessionConfigured;float _preamp,_bands[10];double _requestedRate;NSInteger _requestedChannels;
 NSString *_requestError;
}
+ (instancetype)shared {static PorticoAudioProcessing *s;static dispatch_once_t once;dispatch_once(&once,^{s=[self new];});return s;}
- (instancetype)init {
 if((self=[super init])){
  _mode=@"smart";
  NSURL *base=[NSFileManager.defaultManager URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask].firstObject;
  NSURL *dir=[base URLByAppendingPathComponent:@"AudioProcessing" isDirectory:YES];[NSFileManager.defaultManager createDirectoryAtURL:dir withIntermediateDirectories:YES attributes:nil error:nil];
  NSURL *file=[dir URLByAppendingPathComponent:@"app-equalizer.sqlite3"];
  if(sqlite3_open_v2(file.fileSystemRepresentation,&_db,SQLITE_OPEN_READWRITE|SQLITE_OPEN_CREATE|SQLITE_OPEN_FULLMUTEX,NULL)==SQLITE_OK){
   sqlite3_busy_timeout(_db,1500);char *error=NULL;
   int result=sqlite3_exec(_db,"PRAGMA foreign_keys=ON; CREATE TABLE IF NOT EXISTS app_equalizer (singleton INTEGER PRIMARY KEY NOT NULL CHECK(singleton=1), enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), preamp REAL NOT NULL CHECK(preamp BETWEEN -12 AND 12)); CREATE TABLE IF NOT EXISTS app_equalizer_band (band INTEGER PRIMARY KEY NOT NULL CHECK(band BETWEEN 0 AND 9), gain REAL NOT NULL CHECK(gain BETWEEN -12 AND 12)); CREATE TABLE IF NOT EXISTS app_audio_processing (singleton INTEGER PRIMARY KEY NOT NULL CHECK(singleton=1), sample_rate_matching TEXT NOT NULL CHECK(sample_rate_matching IN('smart','strict','off')));",NULL,NULL,&error);
   if(result!=SQLITE_OK){sqlite3_free(error);sqlite3_close(_db);_db=NULL;}
  }else{if(_db)sqlite3_close(_db);_db=NULL;}
  [file setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
  [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(routeChanged:) name:AVAudioSessionRouteChangeNotification object:nil];
  [self loadEqualizer];
  if(_db){sqlite3_stmt *stmt=NULL;if(sqlite3_prepare_v2(_db,"SELECT sample_rate_matching FROM app_audio_processing WHERE singleton=1",-1,&stmt,NULL)==SQLITE_OK&&sqlite3_step(stmt)==SQLITE_ROW){const unsigned char *text=sqlite3_column_text(stmt,0);NSString *mode=text?[NSString stringWithUTF8String:(const char *)text]:nil;if([@[@"smart",@"strict",@"off"] containsObject:mode])_mode=mode;}sqlite3_finalize(stmt);}
 }return self;
}
/* One app-local value; output and account changes never select or replace it. */
- (void)loadEqualizer {
 _enabled=NO;_preamp=0;memset(_bands,0,sizeof(_bands));
 if(!_db)return;sqlite3_stmt *stmt=NULL;
 if(sqlite3_prepare_v2(_db,"SELECT enabled,preamp FROM app_equalizer WHERE singleton=1",-1,&stmt,NULL)==SQLITE_OK)
  if(sqlite3_step(stmt)==SQLITE_ROW){_enabled=sqlite3_column_int(stmt,0)!=0;_preamp=(float)sqlite3_column_double(stmt,1);}
 sqlite3_finalize(stmt);stmt=NULL;
 if(sqlite3_prepare_v2(_db,"SELECT band,gain FROM app_equalizer_band ORDER BY band",-1,&stmt,NULL)==SQLITE_OK){
  int count=0;while(sqlite3_step(stmt)==SQLITE_ROW){int band=sqlite3_column_int(stmt,0);if(band>=0&&band<10){_bands[band]=(float)sqlite3_column_double(stmt,1);count++;}}
  BOOL valid=count==10&&EQStep(@(_preamp));for(int i=0;i<10;i++)valid=valid&&EQStep(@(_bands[i]));
  if(!valid){_enabled=NO;_preamp=0;memset(_bands,0,sizeof(_bands));}
 }sqlite3_finalize(stmt);
}
- (void)routeChanged:(NSNotification *)note {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self routeChanged:note];});return;}
 _sessionConfigured=NO;[NSNotificationCenter.defaultCenter postNotificationName:PorticoAudioProcessingChanged object:self userInfo:@{@"routeChanged":@YES}];
}
- (BOOL)setEqualizer:(NSDictionary *)settings error:(NSError **)error {
 NSArray *bands=[settings[@"bands"] isKindOfClass:NSArray.class]?settings[@"bands"]:nil;
 id enabled=settings[@"enabled"];BOOL valid=[enabled isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)enabled)==CFBooleanGetTypeID()&&EQStep(settings[@"preampDb"])&&bands.count==10;
 for(id gain in bands)valid=valid&&EQStep(gain);
 for(NSString *key in settings)valid=valid&&[@[@"enabled",@"preampDb",@"bands"] containsObject:key];
 if(!valid){if(error)*error=EQError(@"audio_equalizer_invalid");return NO;}
 if(!_db){if(error)*error=EQError(@"audio_equalizer_storage_unavailable");return NO;}
 BOOL ok=sqlite3_exec(_db,"BEGIN IMMEDIATE",NULL,NULL,NULL)==SQLITE_OK;sqlite3_stmt *stmt=NULL;
 if(ok)ok=sqlite3_prepare_v2(_db,"INSERT INTO app_equalizer(singleton,enabled,preamp) VALUES(1,?,?) ON CONFLICT(singleton) DO UPDATE SET enabled=excluded.enabled,preamp=excluded.preamp",-1,&stmt,NULL)==SQLITE_OK;
 if(ok){sqlite3_bind_int(stmt,1,[enabled boolValue]);sqlite3_bind_double(stmt,2,[settings[@"preampDb"] doubleValue]);ok=sqlite3_step(stmt)==SQLITE_DONE;}sqlite3_finalize(stmt);stmt=NULL;
 if(ok)ok=sqlite3_prepare_v2(_db,"INSERT INTO app_equalizer_band(band,gain) VALUES(?,?) ON CONFLICT(band) DO UPDATE SET gain=excluded.gain",-1,&stmt,NULL)==SQLITE_OK;
 for(int i=0;ok&&i<10;i++){sqlite3_reset(stmt);sqlite3_bind_int(stmt,1,i);sqlite3_bind_double(stmt,2,[bands[i] doubleValue]);ok=sqlite3_step(stmt)==SQLITE_DONE;}sqlite3_finalize(stmt);
 if(ok)ok=sqlite3_exec(_db,"COMMIT",NULL,NULL,NULL)==SQLITE_OK;if(!ok){sqlite3_exec(_db,"ROLLBACK",NULL,NULL,NULL);if(error)*error=EQError(@"audio_equalizer_storage_unavailable");return NO;}
 _enabled=[enabled boolValue];_preamp=[settings[@"preampDb"] floatValue];for(int i=0;i<10;i++)_bands[i]=[bands[i] floatValue];
 [NSNotificationCenter.defaultCenter postNotificationName:PorticoAudioProcessingChanged object:self];return YES;
}
- (BOOL)resetEqualizer:(NSError **)error {
 return [self setEqualizer:@{@"enabled":@(_enabled),@"preampDb":@0,@"bands":@[@0,@0,@0,@0,@0,@0,@0,@0,@0,@0]} error:error];
}
- (void)applyEqualizer:(AVAudioUnitEQ *)equalizer {
 PorticoConfigureEqualizer(equalizer,_enabled,_preamp,_bands);
}
- (BOOL)setSampleRateMatching:(NSString *)mode error:(NSError **)error {
 if(![mode isKindOfClass:NSString.class]||![@[@"smart",@"strict",@"off"] containsObject:mode]){if(error)*error=EQError(@"audio_rate_request_invalid");return NO;}
 if(!_db){if(error)*error=EQError(@"audio_rate_storage_unavailable");return NO;}
 sqlite3_stmt *stmt=NULL;BOOL ok=sqlite3_prepare_v2(_db,"INSERT INTO app_audio_processing(singleton,sample_rate_matching) VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET sample_rate_matching=excluded.sample_rate_matching",-1,&stmt,NULL)==SQLITE_OK;
 if(ok){sqlite3_bind_text(stmt,1,mode.UTF8String,-1,SQLITE_TRANSIENT);ok=sqlite3_step(stmt)==SQLITE_DONE;}sqlite3_finalize(stmt);
 if(!ok){if(error)*error=EQError(@"audio_rate_storage_unavailable");return NO;}
 BOOL changed=![_mode isEqual:mode];_mode=[mode copy];
 if(changed){_sessionConfigured=NO;[NSNotificationCenter.defaultCenter postNotificationName:PorticoAudioProcessingChanged object:self userInfo:@{@"sampleRateMatching":mode}];}
 return YES;
}
- (NSString *)sampleRateMatching {return _mode;}
- (BOOL)configureRate:(double)rate channels:(AVAudioChannelCount)channels error:(NSError **)error {
 NSString *mode=_mode;
 if(!isfinite(rate)||rate<8000||rate>768000||channels<1||channels>32||![@[@"smart",@"strict",@"off"] containsObject:mode]){if(error)*error=EQError(@"audio_rate_request_invalid");return NO;}
 AVAudioSession *audio=AVAudioSession.sharedInstance;BOOL strict=[mode isEqual:@"strict"];
 double requested=[mode isEqual:@"off"]?0:rate;
 BOOL change=strict||!_sessionConfigured||fabs(_requestedRate-requested)>.01||_requestedChannels!=channels;
 _requestedChannels=channels;NSError *failure=nil;
 if(change){
  /* Preferences are requests. Read back sampleRate/channels after activation and report rejection. */
  [audio setActive:NO error:&failure];
  if(!failure)[audio setCategory:AVAudioSessionCategoryPlayback mode:AVAudioSessionModeDefault options:0 error:&failure];
  if(@available(iOS 15.0,tvOS 15.0,*)){if(!failure)[audio setSupportsMultichannelContent:YES error:&failure];}
  // Zero explicitly withdraws our previous request. Merely skipping the setter in Off
  // would leave the last song's preferred rate in the session.
  if(!failure)[audio setPreferredSampleRate:requested error:&failure];
  if(!failure)[audio setActive:YES error:&failure];
  NSInteger maximum=audio.maximumOutputNumberOfChannels;
  if(!failure&&maximum>0)[audio setPreferredOutputNumberOfChannels:MIN((NSInteger)channels,maximum) error:&failure];
  if(!failure){_requestedRate=requested;_sessionConfigured=YES;}
 }
 if(!failure&&strict&&fabs(audio.sampleRate-rate)>.01)failure=EQError(@"audio_rate_strict_unavailable");
 _requestError=failure?@"audio_rate_request_failed":nil;
 if(failure&&error)*error=failure;return failure==nil;
}
- (NSDictionary *)snapshot {
 AVAudioSession *audio=AVAudioSession.sharedInstance;NSMutableArray *bands=[NSMutableArray new];for(int i=0;i<10;i++)[bands addObject:@(_bands[i])];
 return @{@"sampleRateMatching":_mode?:@"smart",@"requestedSampleRate":@(_requestedRate),@"outputSampleRate":@(audio.sampleRate),@"requestedChannels":@(_requestedChannels),@"outputChannels":@(audio.outputNumberOfChannels),@"maximumOutputChannels":@(audio.maximumOutputNumberOfChannels),@"sampleRateMatched":@(_requestedRate>0&&fabs(audio.sampleRate-_requestedRate)<.01),@"requestError":_requestError?:@"",@"eqEnabled":@(_enabled),@"eqPreampDb":@(_preamp),@"eqBands":bands,@"bitPerfectVerified":@NO};
}
- (void)dealloc {if(_db)sqlite3_close(_db);[NSNotificationCenter.defaultCenter removeObserver:self];}
@end
