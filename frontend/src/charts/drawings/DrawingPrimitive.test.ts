import {describe,expect,it,vi} from 'vitest';
import type {IChartApi,PaneAttachedParameter} from 'lightweight-charts';
import type {Candle,DrawingConfig} from '../../types';
import {DrawingPrimitive,drawDrawings,type DrawingPriceSeries} from './DrawingPrimitive';

function canvas(){
 const paths:{kind:string;color:string;points:number[][]}[]=[];let points:number[][]=[];
 const context={strokeStyle:'',fillStyle:'',globalAlpha:1,lineWidth:1,lineCap:'',lineJoin:'',save:vi.fn(),restore:vi.fn(),rect:vi.fn(),clip:vi.fn(),setLineDash:vi.fn(),beginPath:()=>{points=[];},moveTo:(x:number,y:number)=>points.push([x,y]),lineTo:(x:number,y:number)=>points.push([x,y]),closePath:vi.fn(),arc:vi.fn(),stroke:()=>paths.push({kind:'stroke',color:context.strokeStyle,points:[...points]}),fill:()=>paths.push({kind:'fill',color:context.fillStyle,points:[...points]})};
 return {context:context as unknown as CanvasRenderingContext2D,paths};
}
const scope={exchange:'binance',market:'spot',instrument:'BTCUSDT'} as const;
const drawings:DrawingConfig[]=[{...scope,id:'up',type:'arrowUp',points:[{time:1000,price:80}]},{...scope,id:'down',type:'arrowDown',points:[{time:1060,price:60}]},{...scope,id:'horizontal',type:'horizontal',points:[{time:1000,price:50}]},{...scope,id:'trend',type:'trendline',points:[{time:1000,price:90},{time:1120,price:70}]}];

describe('drawing pane primitive',()=>{
 it('renders both arrow directions anchored at the tip, pane-wide horizontals and finite trendline segments',()=>{
  const h=canvas();drawDrawings(h.context,[{id:'up',type:'arrowUp',points:[],coordinates:[{x:20,y:30}]},{id:'down',type:'arrowDown',points:[],coordinates:[{x:60,y:50}]},{id:'h',type:'horizontal',points:[],coordinates:[{x:20,y:70}]},{id:'t',type:'trendline',points:[],coordinates:[{x:-20,y:10},{x:120,y:80}]}],100,70);
  expect(h.context.rect).toHaveBeenCalledWith(0,0,100,70);expect(h.context.clip).toHaveBeenCalledOnce();expect(h.context.restore).toHaveBeenCalledOnce();
  expect(h.paths[0]).toEqual({kind:'fill',color:'#26a69a',points:[[20,30],[13,40],[17,40],[17,54],[23,54],[23,40],[27,40]]});
  expect(h.paths[1]).toEqual({kind:'fill',color:'#ef5350',points:[[60,50],[53,40],[57,40],[57,26],[63,26],[63,40],[67,40]]});
  expect(h.paths[2].points).toEqual([[0,70],[100,70]]);expect(h.paths[3].points).toEqual([[0,20],[100,70]]);
 });
 it('reprojects persisted price/time anchors on viewport changes, uses media canvas and cleans up',()=>{
  let zoom=1;const requestUpdate=vi.fn(),updateViews=vi.fn(),chart={timeScale:()=>({timeToIndex:(time:number)=>[1000,1060,1120].indexOf(time),logicalToCoordinate:(logical:number)=>logical*20*zoom})} as unknown as IChartApi;
  const series={priceToCoordinate:(price:number)=>(100-price)*zoom} as DrawingPriceSeries;
  const primitive=new DrawingPrimitive(series,updateViews);primitive.attached({chart,requestUpdate} as unknown as PaneAttachedParameter);primitive.setData(drawings,[{time:1000}] as Candle[],60);
  expect(requestUpdate).toHaveBeenCalledOnce();expect(primitive.read().map(value=>value.coordinates[0])).toEqual([{x:0,y:20},{x:20,y:40},{x:0,y:50},{x:0,y:10}]);
  zoom=2;primitive.updateAllViews();expect(primitive.read()[1].coordinates).toEqual([{x:40,y:80}]);
  const h=canvas(),renderer=primitive.paneViews()[0].renderer()!;renderer.draw({useMediaCoordinateSpace:(draw:(scope:unknown)=>void)=>draw({context:h.context,mediaSize:{width:100,height:200}})} as Parameters<typeof renderer.draw>[0]);expect(h.paths).toHaveLength(4);
  primitive.setSeries({priceToCoordinate:(price:number)=>200-price} as DrawingPriceSeries);expect(primitive.read()[0].coordinates[0].y).toBe(120);
  primitive.detached();expect(primitive.read()).toEqual([]);expect(updateViews).toHaveBeenCalled();
 });
 it('omits drawings while the new market has no bars and never supplies autoscale expansion',()=>{
  const primitive=new DrawingPrimitive({priceToCoordinate:()=>20} as unknown as DrawingPriceSeries);primitive.setData(drawings,[],60);expect(primitive.read()).toEqual([]);expect(primitive).not.toHaveProperty('autoscaleInfo');
 });
});
