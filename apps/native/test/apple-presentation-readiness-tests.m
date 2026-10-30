#import "../Native/PorticoPresentationReadiness.h"

int main(void){@autoreleasepool{
 NSUInteger checks=0;
#define CHECK(value) do { checks++; if(!(value)){fprintf(stderr,"Readiness check %lu failed at line %d\n",(unsigned long)checks,__LINE__);return 1;} } while(0)
 NSDictionary *binding=@{@"lease":@"lease-a",@"intentId":@3,@"sessionId":@"session-a",@"generation":@7,@"itemId":@"item-a"};
 CHECK(PorticoExternalPresentationBindingFact(nil,binding,@"lease-a")==NSNull.null);
 CHECK(PorticoExternalPresentationBindingFact(binding,nil,@"lease-a")==NSNull.null);
 CHECK(PorticoExternalPresentationBindingFact(binding,binding,nil)==NSNull.null);
 CHECK(PorticoExternalPresentationBindingFact(binding,binding,@"retired-lease")==NSNull.null);
 CHECK([PorticoExternalPresentationBindingFact(binding,binding,@"lease-a") isEqual:binding]);
 // Readiness from any other source/generation must never acknowledge the current fact.
 NSDictionary *other=@{@"lease":@"lease-b",@"intentId":@4,@"sessionId":@"session-b",@"generation":@8,@"itemId":@"item-b"};
 for(NSString *key in binding){
  NSMutableDictionary *stale=[binding mutableCopy];stale[key]=other[key];
  CHECK(PorticoExternalPresentationBindingFact(stale,binding,@"lease-a")==NSNull.null);
  CHECK(PorticoExternalPresentationBindingFact(binding,stale,@"lease-a")==NSNull.null);
 }
 for(NSString *key in @[@"intentId",@"generation"]){
  for(id invalid in @[@YES,@0,@-1,@1.5,@(NAN),@(INFINITY),@9007199254740992.0,@"7",NSNull.null]){
   NSMutableDictionary *malformed=[binding mutableCopy];malformed[key]=invalid;
   CHECK(PorticoExternalPresentationBindingFact(malformed,malformed,@"lease-a")==NSNull.null);
  }
 }
 for(NSString *key in @[@"lease",@"sessionId",@"itemId"]){
  for(id invalid in @[@"",@7,NSNull.null]){
   NSMutableDictionary *malformed=[binding mutableCopy];malformed[key]=invalid;
   CHECK(PorticoExternalPresentationBindingFact(malformed,malformed,@"lease-a")==NSNull.null);
  }
 }
 NSMutableString *session=[@"session-a" mutableCopy];NSMutableDictionary *mutable=[binding mutableCopy];mutable[@"sessionId"]=session;mutable[@"extra"]=@"not-public";
 NSDictionary *published=PorticoExternalPresentationBindingFact(mutable,mutable,@"lease-a");
 CHECK([published isEqual:binding]);
 [session appendString:@"-changed"];mutable[@"generation"]=@8;
 CHECK([published isEqual:binding]);
 CHECK(![published isKindOfClass:NSMutableDictionary.class]);
 printf("Apple external-presentation readiness: %lu checks passed\n",(unsigned long)checks);
 return 0;
}}
