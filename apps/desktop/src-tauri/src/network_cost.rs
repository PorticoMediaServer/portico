//! Cost policy accepts explicit OS facts only; failures and guessed costs stay unknown.
use serde::Serialize;

#[derive(Clone,Copy,Debug,Default,PartialEq,Eq,Serialize)]
#[serde(rename_all="lowercase")]
pub enum NetworkCost { #[default] Unknown, Metered, Unmetered }

pub(crate) fn network_manager_cost(state:Option<u32>,metered:Option<u32>)->NetworkCost {
    if !state.is_some_and(|v|matches!(v,50|60|70)){return NetworkCost::Unknown}
    match metered {Some(1)=>NetworkCost::Metered,Some(2)=>NetworkCost::Unmetered,_=>NetworkCost::Unknown}
}

pub(crate) fn windows_cost(cost:Option<u32>)->NetworkCost {
    // NLM_CONNECTION_COST: explicit unrestricted/fixed/variable plus the cost flags.
    // Congestion alone is not a usage charge; unknown future flags grant nothing.
    let Some(cost)=cost else{return NetworkCost::Unknown};
    if cost&!0x000f_0007!=0{return NetworkCost::Unknown}
    if cost&0x000d_0006!=0{return NetworkCost::Metered}
    if cost&0xffff==1{NetworkCost::Unmetered}else{NetworkCost::Unknown}
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_explicit_connected_network_manager_cost_grants_unmetered(){
        for state in [None,Some(0),Some(10),Some(20),Some(30),Some(40),Some(99)]{
            for metered in [None,Some(0),Some(1),Some(2),Some(3),Some(4)]{assert_eq!(network_manager_cost(state,metered),NetworkCost::Unknown);}
        }
        for state in [50,60,70]{
            assert_eq!(network_manager_cost(Some(state),Some(1)),NetworkCost::Metered);
            assert_eq!(network_manager_cost(Some(state),Some(2)),NetworkCost::Unmetered);
            for guess in [None,Some(0),Some(3),Some(4)]{assert_eq!(network_manager_cost(Some(state),guess),NetworkCost::Unknown);}
        }
    }
    #[test]
    fn windows_unknown_or_limited_cost_never_grants_unmetered(){
        for value in [None,Some(0),Some(0x20000),Some(8),Some(0x10000001)]{assert_eq!(windows_cost(value),NetworkCost::Unknown);}
        for value in [1,0x20001]{assert_eq!(windows_cost(Some(value)),NetworkCost::Unmetered);}
        for value in [2,4,3,5,0x10001,0x40001,0x80001,0xf0007]{assert_eq!(windows_cost(Some(value)),NetworkCost::Metered);}
    }
}
