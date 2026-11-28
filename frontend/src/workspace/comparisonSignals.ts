export interface ComparisonCloseObservation {time:number;close:number|null}

/** Third consecutive UTC daily rise, using observed closes in their given order. */
export function comparisonThirdRiseTimes(points:readonly ComparisonCloseObservation[]):number[]{
 const stars:number[]=[];
 let previous:ComparisonCloseObservation|undefined,latestTime=-1,rises=0;
 for(const point of points){
  const validTime=Number.isSafeInteger(point.time)&&point.time>=0&&point.time%86400===0;
  if(!validTime){previous=undefined;rises=0;continue;}
  // A duplicate or older observation cannot establish a new chronological seed.
  if(point.time<=latestTime){previous=undefined;rises=0;continue;}
  latestTime=point.time;
  if(typeof point.close!=='number'||!Number.isFinite(point.close)||point.close<=0){previous=undefined;rises=0;continue;}
  rises=previous&&point.time-previous.time===86400&&previous.close!==null&&point.close>previous.close?rises+1:0;
  if(rises===3)stars.push(point.time);
  previous=point;
 }
 return stars;
}
