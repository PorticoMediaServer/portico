#ifndef PorticoListeningPolicy_h
#define PorticoListeningPolicy_h
#include <stdbool.h>
#include <math.h>
typedef struct { double lease; double horizon; bool valid; } PorticoListeningLease;
/** Request-start anchoring intentionally subtracts all transport latency. */
static inline PorticoListeningLease PorticoListeningLeaseAt(double start,double server,double expiry,double horizon,bool leaseValid) {
 if(!isfinite(start)||!isfinite(server)||!isfinite(expiry)||!isfinite(horizon)||start<0)return (PorticoListeningLease){0,0,false};
 return (PorticoListeningLease){leaseValid?start+fmax(0,fmin(expiry,horizon)-server):0,start+fmax(0,horizon-server),true};
}
static inline bool PorticoListeningMayPlay(bool owner,bool terminal,bool interrupted,bool paused,bool ended,bool media,double now,double lease,double horizon) {
 return owner&&!terminal&&!interrupted&&!paused&&!ended&&media&&isfinite(now)&&now<lease&&now<horizon;
}
static inline bool PorticoListeningTimerExpired(double wallMS,double mono,double deadlineMS,double monoLimit) {
 return isfinite(deadlineMS)&&deadlineMS>0&&(wallMS>=deadlineMS||mono>=monoLimit);
}
/** The old long-pause trigger selects no hidden amount. Return only the
 * positive displacement of the chosen profile amount, clamped at the start. */
static inline double PorticoAudiobookResumeDisplacement(double elapsed,double position,double choiceMS) {
 if(!isfinite(elapsed)||elapsed<300||!isfinite(position)||position<=0||
   !(choiceMS==0||choiceMS==5000||choiceMS==15000||choiceMS==30000))return 0;
 return fmin(position,choiceMS/1000);
}
#ifdef __OBJC__
#import <Foundation/Foundation.h>
static inline BOOL PorticoPolicyNumber(id value,double minimum,double maximum) {
 return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)!=CFBooleanGetTypeID()&&isfinite([value doubleValue])&&[value doubleValue]>=minimum&&[value doubleValue]<=maximum;
}
static inline BOOL PorticoPolicyBool(id value) {
 return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)==CFBooleanGetTypeID();
}
static inline BOOL PorticoBookRate(id value) {
 return PorticoPolicyNumber(value,.5,3)&&[@[@0.5,@0.75,@1,@1.25,@1.5,@1.75,@2,@2.5,@3] containsObject:value];
}
/** The canonical native policy projects only these two listening preferences. */
static inline BOOL PorticoListeningPreferencesValid(id value) {
 if(value==NSNull.null)return YES;
 if(![value isKindOfClass:NSDictionary.class]||[value count]!=2||!PorticoBookRate(value[@"bookRate"])||!PorticoPolicyNumber(value[@"passoutMinutes"],0,120)||![@[@0,@30,@60,@90,@120] containsObject:value[@"passoutMinutes"]])return NO;
 return YES;
}
/** This is a separate private bridge projection; the server's closed listening
 * preference response still contains only bookRate and passoutMinutes. */
static inline BOOL PorticoAudiobookPreferencesValid(id value) {
 if(!value)return YES;
 if(![value isKindOfClass:NSDictionary.class]||[value count]!=3)return NO;
 return PorticoPolicyNumber(value[@"skipBackMs"],10000,60000)&&[@[@10000,@15000,@30000,@60000] containsObject:value[@"skipBackMs"]]
  &&PorticoPolicyNumber(value[@"skipForwardMs"],15000,90000)&&[@[@15000,@30000,@60000,@90000] containsObject:value[@"skipForwardMs"]]
  &&PorticoPolicyNumber(value[@"resumeRewindMs"],0,30000)&&[@[@0,@5000,@15000,@30000] containsObject:value[@"resumeRewindMs"]];
}
static inline BOOL PorticoListeningEffectsValid(id value) {
 if(![value isKindOfClass:NSDictionary.class]||!PorticoPolicyBool(value[@"gapless"])||!PorticoPolicyNumber(value[@"crossfadeSeconds"],0,12)||![@[@"off",@"track",@"album"] containsObject:value[@"normalization"]])return NO;
 for(NSString *key in value)if(![@[@"gapless",@"crossfadeSeconds",@"normalization",@"neverBoost",@"smoothTransitions",@"sampleRateMatching"] containsObject:key])return NO;
 for(NSString *key in @[@"neverBoost",@"smoothTransitions"])if(value[key]&&!PorticoPolicyBool(value[key]))return NO;
 if(value[@"sampleRateMatching"]&&![@[@"smart",@"strict",@"off"] containsObject:value[@"sampleRateMatching"]])return NO;
 return YES;
}
static inline BOOL PorticoListeningRateAllowed(NSString *kind,id value) {
 return [kind isEqual:@"audiobook_file"]?PorticoBookRate(value):PorticoPolicyNumber(value,1,1);
}
static inline NSNumber *PorticoPreferredListeningRate(NSString *kind,id preferences) {
 if(![kind isEqual:@"audiobook_file"])return @1;
 return [preferences isKindOfClass:NSDictionary.class]&&PorticoBookRate(preferences[@"bookRate"])?preferences[@"bookRate"]:nil;
}
#endif
#endif
