#import <Foundation/Foundation.h>
#import "../Native/PorticoListeningPolicy.h"
static int failures,checks;
static void check(BOOL value,const char *name){checks++;if(!value){failures++;fprintf(stderr,"FAIL %s\n",name);}}
int main(void){@autoreleasepool{
 NSDictionary *preferences=@{@"bookRate":@2.5,@"passoutMinutes":@30};
 check(PorticoListeningPreferencesValid(preferences),"actual canonical two-field listening projection accepted");
 check(PorticoListeningPreferencesValid(NSNull.null),"unreported preferences remain null");
 for(id value in @[@{},@{@"bookRate":@1},@{@"bookRate":@YES,@"passoutMinutes":@0},@{@"bookRate":@1.2,@"passoutMinutes":@0},@{@"bookRate":@1,@"passoutMinutes":@NO},@{@"bookRate":@1,@"passoutMinutes":@15}])check(!PorticoListeningPreferencesValid(value),"invalid or partial listening preferences refused");
 for(NSString *retired in @[@"musicRate",@"autoplayNext"]){NSMutableDictionary *value=[preferences mutableCopy];value[retired]=@1;check(!PorticoListeningPreferencesValid(value),"retired listening field is not an alias");}
 for(NSNumber *rate in @[@0.5,@0.75,@1,@1.25,@1.5,@1.75,@2,@2.5,@3])check(PorticoListeningRateAllowed(@"audiobook_file",rate),"all canonical audiobook rates accepted");
 check(PorticoListeningRateAllowed(@"song",@1),"music remains 1x");
 check(!PorticoListeningRateAllowed(@"song",@1.25)&&!PorticoListeningRateAllowed(@"song",@YES),"music cannot use book rates or Boolean rate");
 check([PorticoPreferredListeningRate(@"song",preferences)isEqual:@1]&&[PorticoPreferredListeningRate(@"song",NSNull.null)isEqual:@1],"music 1x is behavior independent of saved preference fields");
 check([PorticoPreferredListeningRate(@"audiobook_file",preferences)isEqual:@2.5],"book applies its actual preference");
 check(PorticoPreferredListeningRate(@"audiobook_file",NSNull.null)==nil,"unreported book preference does not fabricate a saved value");
 NSDictionary *effects=@{@"gapless":@YES,@"crossfadeSeconds":@6,@"normalization":@"track",@"neverBoost":@YES,@"smoothTransitions":@YES,@"sampleRateMatching":@"strict"};
 check(PorticoListeningEffectsValid(effects),"actual canonical typed audio effects accepted");
 check(PorticoListeningEffectsValid(@{@"gapless":@NO,@"crossfadeSeconds":@0,@"normalization":@"off"}),"unreported optional effects stay absent");
 for(NSDictionary *patch in @[@{@"preampDb":@0},@{@"gapless":@1},@{@"crossfadeSeconds":@13},@{@"normalization":@"automatic"},@{@"neverBoost":@1},@{@"smoothTransitions":NSNull.null},@{@"sampleRateMatching":@"automatic"}]){NSMutableDictionary *value=[effects mutableCopy];[value addEntriesFromDictionary:patch];check(!PorticoListeningEffectsValid(value),"invalid or unsupported effect field refused");}
 NSDictionary *book=@{@"skipBackMs":@60000,@"skipForwardMs":@90000,@"resumeRewindMs":@5000};
 check(PorticoAudiobookPreferencesValid(book),"independent canonical audiobook choices accepted");
 check(PorticoAudiobookPreferencesValid(nil),"older private policy defaults remain supported");
 for(NSString *key in @[@"skipBackMs",@"skipForwardMs",@"resumeRewindMs"]){NSMutableDictionary *bad=[book mutableCopy];bad[key]=@7000;check(!PorticoAudiobookPreferencesValid(bad),"unsupported book choice refused");bad[key]=@YES;check(!PorticoAudiobookPreferencesValid(bad),"Boolean book duration refused");}
 PorticoListeningLease lease=PorticoListeningLeaseAt(1,100,110,120,true);
 check(lease.valid&&lease.lease==11&&lease.horizon==21,"existing lease clock anchoring unchanged");
 check(!PorticoListeningMayPlay(true,false,false,false,false,true,11,lease.lease,lease.horizon),"existing lease expiry still stops playback");
 check(PorticoListeningTimerExpired(100,10,200,9),"existing monotonic timer still expires despite wall clock rollback");
 printf("Apple listening policy: %d checks, %d failures\n",checks,failures);return failures?1:0;
}}
