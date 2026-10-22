import {afterEach,describe,expect,it,vi} from 'vitest';
import type {IChartApi,IPanePrimitive,PaneAttachedParameter} from 'lightweight-charts';
import type {Candle,DrawingConfig} from '../../types';
import {DrawingLayer} from './DrawingLayer';
import type {DrawingPriceSeries} from './DrawingPrimitive';

class Element extends EventTarget {
 style:Record<string,string>={};dataset:Record<string,string>={};className='';children:Element[]=[];parent:Element|null=null;capture=new Set<number>();
 constructor(readonly ownerDocument:TestDocument){super();}
 setAttribute=vi.fn();
 appendChild(child:Element){this.children.push(child);child.parent=this;return child;}
 remove(){if(this.parent)this.parent.children=this.parent.children.filter(child=>child!==this);}
 getBoundingClientRect(){return {left:10+Number.parseFloat(this.style.left||'0'),top:20+Number.parseFloat(this.style.top||'0'),width:Number.parseFloat(this.style.width||'400'),height:Number.parseFloat(this.style.height||'300')};}
 setPointerCapture(id:number){this.capture.add(id);}hasPointerCapture(id:number){return this.capture.has(id);}releasePointerCapture(id:number){this.capture.delete(id);}
}
class TestDocument extends EventTarget {createElement(){return new Element(this);}}
function pointer(element:Element,type:string,x:number,y:number,id=1){
 const event=new Event(type,{cancelable:true});Object.assign(event,{clientX:x+10,clientY:y+20,pointerId:id,button:0});element.dispatchEvent(event);return event;
}
const bars=[{time:1000},{time:1060},{time:1120}] as Candle[];
function harness(position=''){
 const document=new TestDocument(),element=new Element(document),paneElement=new Element(document),options={handleScale:{mouseWheel:true,pinch:false,axisPressedMouseMove:{time:true,price:false}},handleScroll:{mouseWheel:false,pressedMouseMove:true,horzTouchDrag:true,vertTouchDrag:false}},commit=vi.fn(),toolChange=vi.fn();let primitive:IPanePrimitive|null=null,zoom=1;
 element.style.position=position;
 const pane={getHTMLElement:()=>paneElement,paneIndex:()=>0,attachPrimitive:vi.fn((value:IPanePrimitive)=>{primitive=value;value.attached?.({chart,requestUpdate:vi.fn()} as unknown as PaneAttachedParameter);}),detachPrimitive:vi.fn((value:IPanePrimitive)=>value.detached?.())};
 const chart={chartElement:()=>element,priceScale:()=>({width:()=>0}),paneSize:()=>({width:400,height:300}),timeScale:()=>({timeToIndex:(time:number,nearest?:boolean)=>{const index=bars.findIndex(bar=>bar.time===time);return index!==-1?index:nearest?Math.min(2,Math.max(0,Math.ceil((time-1000)/60))):null;},logicalToCoordinate:(logical:number)=>logical*100*zoom,coordinateToLogical:(x:number)=>x/100/zoom,coordinateToTime:(x:number)=>bars[Math.round(x/100/zoom)]?.time??null}),options:()=>options,applyOptions:vi.fn()};
 const series={getPane:()=>pane,coordinateToPrice:(y:number)=>1000-y,priceToCoordinate:(price:number)=>1000-price};
 const layer=new DrawingLayer(chart as unknown as IChartApi,series as unknown as DrawingPriceSeries,commit,toolChange);layer.update([],bars,60);
 return {layer,overlay:element.children[0],element,pane,document,chart,commit,toolChange,options,zoom:(value:number)=>{zoom=value;primitive?.updateAllViews?.();}};
}
afterEach(()=>vi.unstubAllGlobals());

describe('drawing interaction ownership',()=>{
 it('keeps the input overlay relative to the chart and restores only positioning it owns',()=>{
  for(const original of ['','static','relative','absolute']){
   const h=harness(original);
   expect(h.element.style.position).toBe(original===''||original==='static'?'relative':original);
   h.layer.dispose();expect(h.element.style.position).toBe(original);
  }
 });
 it('commits arrows and horizontals at UTC/price anchors, restores chart controls and clears each tool',()=>{
  const h=harness();
  for(const type of ['arrowUp','arrowDown','horizontal'] as const){
   h.layer.setTool(type);expect(h.chart.applyOptions).toHaveBeenLastCalledWith({handleScroll:false,handleScale:false});expect(h.overlay.style.display).toBe('block');
   pointer(h.overlay,'pointerdown',250,75);pointer(h.overlay,'pointerup',250,75);expect(h.commit).toHaveBeenLastCalledWith(type,[{time:1150,price:925}]);expect(h.layer.read().tool).toBeNull();expect(h.overlay.style.display).toBe('none');expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);
  }
  expect(h.toolChange).toHaveBeenCalledTimes(3);h.layer.dispose();
 });
 it('uses two separate pointer taps for trendlines, cancels with Escape and ignores a cancelled touch',()=>{
  const h=harness();h.layer.setTool('trendline');pointer(h.overlay,'pointerdown',125,40,7);pointer(h.overlay,'pointerup',125,40,7);expect(h.layer.read().draft).toEqual([{time:1075,price:960}]);expect(h.commit).not.toHaveBeenCalled();
  pointer(h.overlay,'pointerdown',275,80,8);pointer(h.overlay,'pointercancel',275,80,8);pointer(h.overlay,'pointerup',275,80,8);expect(h.commit).not.toHaveBeenCalled();
  const escape=new Event('keydown',{cancelable:true});Object.assign(escape,{key:'Escape'});h.document.dispatchEvent(escape);expect(h.layer.read()).toMatchObject({tool:null,draft:[]});expect(escape.defaultPrevented).toBe(true);
  h.layer.setTool('trendline');pointer(h.overlay,'pointerdown',125,40);pointer(h.overlay,'pointerup',125,40);pointer(h.overlay,'pointermove',275,80);pointer(h.overlay,'pointerdown',275,80);pointer(h.overlay,'pointerup',275,80);expect(h.commit).toHaveBeenCalledOnce();expect(h.commit).toHaveBeenCalledWith('trendline',[{time:1075,price:960},{time:1165,price:920}]);h.layer.dispose();
 });
 it('rejects scale/outside clicks and unavailable/invalid prices without committing',()=>{
  const h=harness();h.layer.setTool('arrowUp');pointer(h.overlay,'pointerdown',420,40);pointer(h.overlay,'pointerup',420,40);expect(h.commit).not.toHaveBeenCalled();h.layer.update([],[],60);pointer(h.overlay,'pointerdown',50,40);pointer(h.overlay,'pointerup',50,40);expect(h.commit).not.toHaveBeenCalled();
  h.layer.update([],bars,60);h.layer.setSeries({getPane:()=>h.pane,coordinateToPrice:()=>-1,priceToCoordinate:()=>10} as unknown as DrawingPriceSeries);pointer(h.overlay,'pointerdown',50,40);pointer(h.overlay,'pointerup',50,40);expect(h.commit).not.toHaveBeenCalled();h.layer.dispose();
 });
 it('projects updated persistence data in the live viewport and removes every handler and primitive once',()=>{
  const h=harness(),drawing:DrawingConfig={id:'saved',exchange:'binance',market:'spot',instrument:'BTCUSDT',type:'arrowUp',points:[{time:1090,price:950}]};h.layer.update([drawing],bars,60);expect(h.layer.read().drawings[0].coordinates).toEqual([{x:150,y:50}]);h.zoom(2);expect(h.layer.read().drawings[0].coordinates).toEqual([{x:300,y:50}]);
  h.layer.setTool('trendline');pointer(h.overlay,'pointerdown',25,25);h.layer.dispose();h.layer.dispose();expect(h.element.children).toHaveLength(0);expect(h.pane.detachPrimitive).toHaveBeenCalledOnce();expect(h.overlay.capture.size).toBe(0);expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);
  pointer(h.overlay,'pointerup',25,25);h.document.dispatchEvent(Object.assign(new Event('keydown'),{key:'Escape'}));expect(h.commit).not.toHaveBeenCalled();expect(h.toolChange).not.toHaveBeenCalled();
 });
});
