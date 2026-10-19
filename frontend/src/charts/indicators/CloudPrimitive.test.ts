import {describe,it,expect,vi} from 'vitest';
import type {IChartApi,ISeriesApi,SeriesAttachedParameter} from 'lightweight-charts';
import {CloudPrimitive,drawCloud} from './CloudPrimitive';

function context(){
 const paths:{fill:string;points:number[][]}[]=[];let points:number[][]=[];
 const ctx={fillStyle:'',save:vi.fn(),restore:vi.fn(),beginPath:()=>{points=[];},rect:vi.fn(),clip:vi.fn(),moveTo:(x:number,y:number)=>points.push([x,y]),lineTo:(x:number,y:number)=>points.push([x,y]),closePath:vi.fn(),fill:()=>paths.push({fill:ctx.fillStyle,points:[...points]})};
 return {ctx:ctx as unknown as CanvasRenderingContext2D,paths};
}
describe('Ichimoku cloud canvas',()=>{
 it('fills only between span lines, clips to the pane and splits bull/bear intersections',()=>{
  const {ctx,paths}=context();drawCloud(ctx,[{x:10,a:20,b:40},{x:30,a:40,b:20}],100,80);
  expect(ctx.rect).toHaveBeenCalledWith(0,0,100,80);expect(ctx.clip).toHaveBeenCalledOnce();expect(ctx.save).toHaveBeenCalledOnce();expect(ctx.restore).toHaveBeenCalledOnce();
  expect(paths).toEqual([{fill:'rgba(38, 166, 154, 0.20)',points:[[10,20],[20,30],[20,30],[10,40]]},{fill:'rgba(239, 83, 80, 0.20)',points:[[20,30],[30,40],[30,20],[20,30]]}]);
  expect(paths.flatMap(path=>path.points).every(([,y])=>y>=20&&y<=40)).toBe(true);
 });
 it('uses fresh public chart coordinates on pan/zoom and uses the target media coordinate space',()=>{
  let zoom=1;const update=vi.fn(),chart={timeScale:()=>({timeToCoordinate:(time:number)=>(time-100)*zoom})} as unknown as IChartApi;
  const series={priceToCoordinate:(price:number)=>(100-price)*zoom} as unknown as ISeriesApi<'Line'>;
  const cloud=new CloudPrimitive();cloud.attached({chart,series,requestUpdate:update} as unknown as SeriesAttachedParameter);
  cloud.setData([{time:110,a:80,b:60},{time:120,a:75,b:65}]);expect(update).toHaveBeenCalledOnce();
  const first=context(),second=context(),target=(ctx:CanvasRenderingContext2D)=>({useMediaCoordinateSpace:(draw:(scope:unknown)=>void)=>draw({context:ctx,mediaSize:{width:400,height:300}})});
  const renderer=cloud.paneViews()[0].renderer()!;renderer.draw(target(first.ctx) as Parameters<typeof renderer.draw>[0]);
  expect(first.paths[0].points).toEqual([[10,20],[20,25],[20,35],[10,40]]);
  zoom=2;cloud.updateAllViews();renderer.draw(target(second.ctx) as Parameters<typeof renderer.draw>[0]);expect(second.paths[0].points).toEqual([[20,40],[40,50],[40,70],[20,80]]);
  cloud.detached();const detached=context();renderer.draw(target(detached.ctx) as Parameters<typeof renderer.draw>[0]);expect(detached.paths).toEqual([]);
 });
});
