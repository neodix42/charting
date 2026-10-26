import type {AnalysisOperator} from './screenerAnalysis';

export const analysisOperatorHelp:Record<AnalysisOperator,{description:string;example:string}>={
 above:{description:'The left value is greater than the right value at the current closed bar.',example:'Close is above SMA(200).'},
 below:{description:'The left value is less than the right value at the current closed bar.',example:'Kijun is below Tenkan.'},
 crossesAbove:{description:'The left value moves from at or below the right value on the preceding closed bar to above it on the current closed bar.',example:'Tenkan crosses above Kijun, comparing the preceding and current closed bars.'},
 crossesBelow:{description:'The left value moves from at or above the right value on the preceding closed bar to below it on the current closed bar.',example:'Tenkan crosses below Kijun, comparing the preceding and current closed bars.'},
 between:{description:'The left value is between the right value and the upper value, including both boundaries.',example:'RSI is between 40 and 60, including both boundaries.'},
 withinDistance:{description:'The absolute difference between the left and right values is at most the distance multiplier times the absolute distance-field value.',example:'Close is within 0.2 × ATR(14) of SMA(200).'},
 occurredWithin:{description:'The selected comparison was true at least once within the last N closed bars, including the current closed bar.',example:'A MACD histogram cross above 0 occurred within the last 3 closed bars.'},
 remainedFor:{description:'The selected comparison was true on every one of the last N closed bars, including the current closed bar.',example:'Close remained above SMA(200) for 5 closed bars.'},
 highest:{description:'The left value exceeds the highest right-side value over the previous N closed bars. The current bar is excluded from the reference window.',example:'Close exceeds the previous 20-bar High.'},
 lowest:{description:'The left value falls below the lowest right-side value over the previous N closed bars. The current bar is excluded from the reference window.',example:'Close falls below the previous 20-bar Low.'},
};

export function analysisOperatorDescription(operator:AnalysisOperator){const help=analysisOperatorHelp[operator];return`${help.description} Example: ${help.example}`;}
