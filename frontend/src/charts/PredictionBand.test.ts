import {expect,it,vi} from 'vitest';
import type {IChartApi,ISeriesApi,PaneAttachedParameter,UTCTimestamp} from 'lightweight-charts';
import {PredictionBand} from './PredictionBand';

it('fills precisely between quantiles, follows live coordinates and releases references on detach',()=>{
 let zoom=1;const points:number[][]=[],fills:string[]=[],update=vi.fn(),context={fillStyle:'',save:vi.fn(),restore:vi.fn(),beginPath:()=>{points.length=0;},moveTo:(x:number,y:number)=>points.push([x,y]),lineTo:(x:number,y:number)=>points.push([x,y]),closePath:vi.fn(),fill:()=>fills.push(context.fillStyle)};
 const chart={timeScale:()=>({timeToCoordinate:(time:number)=>time*zoom})} as unknown as IChartApi,series={priceToCoordinate:(price:number)=>100-price*zoom} as unknown as ISeriesApi<'Line'>;
 const band=new PredictionBand(series,[{time:1 as UTCTimestamp,lower:20,upper:40},{time:2 as UTCTimestamp,lower:25,upper:45}],'#2563b833');band.attached({chart,requestUpdate:update} as unknown as PaneAttachedParameter);
 const renderer=band.paneViews()[0].renderer()!,target={useMediaCoordinateSpace:(draw:(scope:unknown)=>void)=>draw({context,mediaSize:{width:300,height:200}})} as Parameters<typeof renderer.draw>[0];
 renderer.draw(target);expect(points).toEqual([[1,60],[2,55],[2,75],[1,80]]);expect(fills).toEqual(['#2563b833']);
 zoom=2;band.setColor('#94f74433');renderer.draw(target);expect(points).toEqual([[2,20],[4,10],[4,50],[2,60]]);expect(fills[1]).toBe('#94f74433');expect(update).toHaveBeenCalledOnce();
 band.detached();renderer.draw(target);expect(fills).toHaveLength(2);
});
