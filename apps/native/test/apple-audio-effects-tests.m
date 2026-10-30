#import <Foundation/Foundation.h>
#import "../Native/PorticoAudioEffects.h"
#import "../Native/PorticoListeningPolicy.h"

static int checks,failures;
static void check(BOOL value,const char *name){checks++;if(!value){failures++;fprintf(stderr,"FAIL %s\n",name);}}
static BOOL near(double actual,double expected){return isfinite(actual)&&fabs(actual-expected)<1e-10;}
static NSDictionary *gain(double db,id peak){NSMutableDictionary *g=[@{@"trackDb":@(db)} mutableCopy];if(peak)g[@"trackPeak"]=peak;return @{@"gain":g};}
static NSDictionary *envelope(NSArray *head,NSArray *tail){return @{@"loudnessEnvelope":@{@"intervalMs":@100,@"startLufs":head,@"endLufs":tail}};}

int main(void){@autoreleasepool{
 NSDictionary *track=@{@"normalization":@"track"},*album=@{@"normalization":@"album"},*off=@{@"normalization":@"off"};
 check(near(PorticoAudioGainLinear(gain(-6,@1),track),0.5011872336272722),"track attenuation uses measured gain");
 check(near(PorticoAudioGainLinear(gain(6,@0.25),track),1.9952623149688795),"known safe peak permits amplification");
 check(near(PorticoAudioGainLinear(gain(6,@0.75),track),1.0/0.75),"measured peak limits amplification");
 check(near(PorticoAudioGainLinear(gain(6,nil),track),1),"missing peak never amplifies");
 check(near(PorticoAudioGainLinear(gain(6,@0.25),@{@"normalization":@"track",@"neverBoost":@YES}),1),"Never boost clamps actual positive gain");
 check(near(PorticoAudioGainLinear(gain(-6,@1),@{@"normalization":@"track",@"neverBoost":@YES}),0.5011872336272722),"Never boost retains attenuation");
 check(near(PorticoAudioGainLinear(gain(-6,@1),off),1),"off does not process source gain");
 check(near(PorticoAudioGainLinear(@{},track),1),"unknown gain remains unity");
 check(near(PorticoAudioGainLinear(gain(-6,@1),album),0.5011872336272722),"album uses track fallback only when album gain absent");
 NSDictionary *both=@{@"gain":@{@"trackDb":@(-6),@"trackPeak":@1,@"albumDb":@(-3),@"albumPeak":@0.5}};
 check(near(PorticoAudioGainLinear(both,album),0.7079457843841379),"album gain uses real album measurements");
 check(near(PorticoAudioGainLinear(@{@"loudnessAppliedDb":@(-12)},track),1),"server baked normalization is never applied again");
 check(near(PorticoAudioGainLinear(@{@"conversionGainDb":@(-6)},track),1),"encoded conversion gain is not a client control");
 check(near(PorticoAudioGainLinear(gain(-6,@1),@{@"normalization":@"track",@"preampDb":@12}),0.5011872336272722),"normalization never uses a second EQ preamp");
 check(!PorticoAudioHasLocalLeveling(gain(0,@1),track),"saved normalization with zero gain is not applied leveling");
 check(!PorticoAudioHasLocalLeveling(gain(6,@1),track),"peak-clamped unity is not applied leveling");
 check(!PorticoAudioHasLocalLeveling(gain(6,@0.25),@{@"normalization":@"track",@"neverBoost":@YES}),"Never boost unity is not applied leveling");
 check(PorticoAudioHasLocalLeveling(gain(-0.00001,@1),track),"real Float32 attenuation below copy precision is applied");
 check(!PorticoAudioHasLocalLeveling(gain(-0.00000001,@1),track),"coefficient rounded to Float32 unity is not applied");
 check(!PorticoAudioHasLocalLeveling(@{@"loudnessAppliedDb":@(-12)},track),"baked server change is separate from local renderer fact");
 check(!PorticoAudioHasLocalLeveling(gain(-6,@1),@{@"normalization":@"off",@"crossfadeSeconds":@6}),"transition volume alone is not normalization");
 check(PorticoAudioPlanFactsValid(@{}),"unreported optional producer facts stay absent");
 check(PorticoAudioPlanFactsValid(@{@"sourceBitrateBps":@123456,@"conversionGainDb":@(-6),@"loudnessAppliedDb":@(-12)}),"measured and baked facts accepted without extra gain");
 check(!PorticoAudioPlanFactsValid(@{@"loudnessAppliedDb":@(-12),@"gain":@{@"trackDb":@(-12)}}),"contradictory source and baked gains refused");
 for(id bad in @[@YES,@(-1),@1.5,@(NAN),@9007199254740992.0])check(!PorticoAudioPlanFactsValid(@{@"sourceBitrateBps":bad}),"invalid measured bitrate refused");
 for(NSString *key in @[@"conversionGainDb",@"loudnessAppliedDb"])for(id bad in @[@YES,@(-61),@31,@(INFINITY),NSNull.null])check(!PorticoAudioPlanFactsValid(@{key:bad}),"invalid baked fact refused");
 NSDictionary *tail=envelope(@[],@[@(-24)]),*head=envelope(@[@(-12)],@[]);
 check(near(PorticoAudioTransitionSeconds(tail,head,6,NO,YES),3),"12LU measured head/tail contrast shapes six-second fade");
 check(near(PorticoAudioTransitionSeconds(tail,tail,6,NO,YES),6),"absent next head retains fixed fade");
 check(near(PorticoAudioTransitionSeconds(tail,head,6,NO,NO),6),"Smooth off retains fixed fade");
 check(near(PorticoAudioTransitionSeconds(tail,head,6,YES,YES),0),"same album never overlaps");
 check(near(PorticoAudioTransitionSeconds(tail,head,0,NO,YES),0),"zero configured fade stays zero");
 check(near(PorticoAudioTransitionSeconds(tail,head,0.1,NO,YES),0.1),"minimum shape never exceeds configured duration");
 check(near(PorticoAudioTransitionSeconds(@{},head,6,NO,YES),6),"unknown envelope never invents loudness");
 check(near(PorticoAudioLoudnessMean(@[@(-30),@(-10)]),-12.967086218813386),"envelope averages power rather than arithmetic LUFS");
 check(isnan(PorticoAudioTransitionSeconds(tail,head,13,NO,YES))&&isnan(PorticoAudioTransitionSeconds(tail,head,NAN,NO,YES)),"out-of-contract maximum refused");
 check(PorticoAudioEnvelopeValid(envelope(@[@(-120),@10],@[])[@"loudnessEnvelope"]),"real envelope boundaries accepted");
 NSMutableArray *tooMany=[NSMutableArray new];for(int i=0;i<31;i++)[tooMany addObject:@(-24)];
 for(id bad in @[@{@"intervalMs":@99,@"startLufs":@[],@"endLufs":@[]},@{@"intervalMs":@100,@"startLufs":tooMany,@"endLufs":@[]},@{@"intervalMs":@100,@"startLufs":@[@YES],@"endLufs":@[]},@{@"intervalMs":@100,@"startLufs":@[@(-121)],@"endLufs":@[]},@{@"intervalMs":@100,@"startLufs":@[],@"endLufs":@[@11]}])check(!PorticoAudioPlanFactsValid(@{@"loudnessEnvelope":bad}),"invalid envelope refused before rendering");
 check(!PorticoListeningEffectsValid(@{@"gapless":@YES,@"crossfadeSeconds":@6,@"normalization":@"track",@"preampDb":@0}),"old render preamp is rejected, not migrated");
 printf("Apple audio effects: %d checks, %d failures\n",checks,failures);return failures?1:0;
}}
