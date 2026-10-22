import {afterEach,describe,expect,it,vi} from 'vitest';
import type {IChartApi,IPanePrimitive,PaneAttachedParameter} from 'lightweight-charts';
import type {Candle,DrawingConfig,DrawingType} from '../../types';
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
function doubleClick(element:Element,x:number,y:number){return pointer(element,'dblclick',x,y);}
function escape(document:TestDocument){const event=new Event('keydown',{cancelable:true});Object.assign(event,{key:'Escape'});document.dispatchEvent(event);return event;}
const bars=[{time:1000},{time:1060},{time:1120}] as Candle[];
function harness(position=''){
 const document=new TestDocument(),element=new Element(document),paneElement=new Element(document),options={handleScale:{mouseWheel:true,pinch:false,axisPressedMouseMove:{time:true,price:false}},handleScroll:{mouseWheel:false,pressedMouseMove:true,horzTouchDrag:true,vertTouchDrag:false}},commit=vi.fn(),toolChange=vi.fn(),move=vi.fn(),moveChange=vi.fn();let primitive:IPanePrimitive|null=null,zoom=1,leftScaleWidth=0;
 element.style.position=position;
 const pane={getHTMLElement:()=>paneElement,paneIndex:()=>0,attachPrimitive:vi.fn((value:IPanePrimitive)=>{primitive=value;value.attached?.({chart,requestUpdate:vi.fn()} as unknown as PaneAttachedParameter);}),detachPrimitive:vi.fn((value:IPanePrimitive)=>value.detached?.())};
 const chart={chartElement:()=>element,priceScale:()=>({width:()=>leftScaleWidth}),paneSize:()=>({width:400,height:300}),timeScale:()=>({timeToIndex:(time:number,nearest?:boolean)=>{const index=bars.findIndex(bar=>bar.time===time);return index!==-1?index:nearest?Math.min(2,Math.max(0,Math.ceil((time-1000)/60))):null;},logicalToCoordinate:(logical:number)=>logical*100*zoom,coordinateToLogical:(x:number)=>x/100/zoom,coordinateToTime:(x:number)=>bars[Math.round(x/100/zoom)]?.time??null}),options:()=>options,applyOptions:vi.fn()};
 const series={getPane:()=>pane,coordinateToPrice:(y:number)=>1000-y,priceToCoordinate:(price:number)=>1000-price};
 const layer=new DrawingLayer(chart as unknown as IChartApi,series as unknown as DrawingPriceSeries,commit,toolChange,move,moveChange);layer.update([],bars,60);
 return {layer,overlay:element.children[0],element,pane,paneElement,document,chart,commit,toolChange,move,moveChange,options,leftScale:(value:number)=>{leftScaleWidth=value;},zoom:(value:number)=>{zoom=value;primitive?.updateAllViews?.();}};
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

const saved=(type:DrawingType='arrowUp',id='saved'):DrawingConfig=>({id,type,exchange:'binance',market:'spot',instrument:'BTCUSDT',points:type==='trendline'?[{time:1045,price:940},{time:1135,price:900}]:[{time:1090,price:950}]});

describe('moving saved drawings',()=>{
 it.each(['arrowUp','arrowDown','horizontal'] as const)('double-clicks and places a %s without jumping the grabbed point or committing its preview',type=>{
  const h=harness(),drawing=saved(type);h.layer.update([drawing],bars,60);
  // Grab the arrow body, or a horizontal segment far from its saved time anchor.
  const grab=type==='horizontal'?{x:300,y:50}:{x:152,y:type==='arrowUp'?62:38};
  expect(doubleClick(h.element,grab.x,grab.y).defaultPrevented).toBe(true);
  expect(h.layer.read()).toMatchObject({tool:null,movingId:'saved',drawings:[]});expect(h.layer.read().preview[0].points).toEqual(drawing.points);
  expect(h.overlay.style.display).toBe('block');expect(h.moveChange).toHaveBeenLastCalledWith(true);expect(h.chart.applyOptions).toHaveBeenLastCalledWith({handleScroll:false,handleScale:false});
  pointer(h.overlay,'pointermove',grab.x+40,grab.y+30);expect(h.layer.read().preview[0].coordinates).toEqual([{x:190,y:80}]);expect(h.move).not.toHaveBeenCalled();expect(drawing.points).toEqual([{time:1090,price:950}]);
  pointer(h.overlay,'pointerdown',grab.x+40,grab.y+30);pointer(h.overlay,'pointerup',grab.x+40,grab.y+30);
  expect(h.move).toHaveBeenCalledExactlyOnceWith('saved',[{time:1114,price:920}]);expect(h.commit).not.toHaveBeenCalled();expect(h.toolChange).not.toHaveBeenCalled();expect(h.layer.read()).toMatchObject({movingId:null,preview:[]});expect(h.overlay.style.display).toBe('none');expect(h.moveChange.mock.calls).toEqual([[true],[false]]);expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);h.layer.dispose();
 });
 it('translates both trendline endpoints by the same screen distance and commits on drag release',()=>{
  const h=harness(),drawing=saved('trendline');h.layer.update([drawing],bars,60);doubleClick(h.element,150,80);
  pointer(h.overlay,'pointermove',200,130);expect(h.layer.read().preview[0].coordinates).toEqual([{x:125,y:110},{x:275,y:150}]);expect(h.move).not.toHaveBeenCalled();
  pointer(h.overlay,'pointerdown',200,130,7);pointer(h.overlay,'pointermove',210,140,7);pointer(h.overlay,'pointerup',210,140,7);
  expect(h.move).toHaveBeenCalledExactlyOnceWith('saved',[{time:1081,price:880},{time:1171,price:840}]);expect(h.overlay.capture.size).toBe(0);expect(drawing.points).toEqual([{time:1045,price:940},{time:1135,price:900}]);h.layer.dispose();
 });
 it('uses the real pane bounds and left scale when the inactive overlay has no layout box',()=>{
  const h=harness(),drawing=saved();h.paneElement.style.left='30px';h.paneElement.style.top='45px';h.leftScale(55);h.layer.update([drawing],bars,60);
  // display:none reports a zero DOMRect in a browser, unlike the pane row.
  h.overlay.getBoundingClientRect=()=>({left:0,top:0,width:0,height:0});
  expect(doubleClick(h.element,150,50).defaultPrevented).toBe(false);expect(h.layer.read().movingId).toBeNull();
  expect(doubleClick(h.element,150+30+55,50+45).defaultPrevented).toBe(true);
  pointer(h.overlay,'pointermove',200+30+55,80+45);expect(h.layer.read().preview[0].coordinates).toEqual([{x:200,y:80}]);h.layer.dispose();
 });
 it('chooses the topmost overlapping item and leaves ordinary chart interaction alone on a miss',()=>{
  const h=harness();h.layer.update([saved('arrowUp','under'),saved('horizontal','top')],bars,60);
  const calls=h.chart.applyOptions.mock.calls.length;expect(doubleClick(h.element,250,200).defaultPrevented).toBe(false);expect(h.chart.applyOptions).toHaveBeenCalledTimes(calls);
  doubleClick(h.element,150,50);expect(h.layer.read().movingId).toBe('top');expect(h.layer.read().drawings.map(drawing=>drawing.id)).toEqual(['under']);
  const wheel=new Event('wheel',{cancelable:true});h.overlay.dispatchEvent(wheel);expect(wheel.defaultPrevented).toBe(true);h.layer.dispose();
 });
 it.each(['escape','cursor','another tool','pointercancel','series','dispose'] as const)('cancels using %s without persisting the preview or losing original controls',cancel=>{
  const h=harness(),drawing=saved();h.layer.update([drawing],bars,60);doubleClick(h.element,150,50);pointer(h.overlay,'pointermove',220,100);pointer(h.overlay,'pointerdown',220,100,3);
  if(cancel==='escape')expect(escape(h.document).defaultPrevented).toBe(true);
  if(cancel==='cursor')h.layer.setTool(null);
  if(cancel==='another tool')h.layer.setTool('trendline');
  if(cancel==='pointercancel')pointer(h.overlay,'pointercancel',220,100,3);
  if(cancel==='series')h.layer.setSeries({getPane:()=>h.pane,coordinateToPrice:(y:number)=>1000-y,priceToCoordinate:(price:number)=>1000-price} as unknown as DrawingPriceSeries);
  if(cancel==='dispose')h.layer.dispose();
  expect(h.move).not.toHaveBeenCalled();expect(h.layer.read().movingId).toBeNull();expect(h.overlay.capture.size).toBe(0);expect(h.moveChange.mock.calls).toEqual([[true],[false]]);
  if(cancel!=='dispose')expect(h.layer.read().drawings[0].points).toEqual(drawing.points);
  if(cancel==='another tool'){expect(h.layer.read().tool).toBe('trendline');h.layer.setTool(null);}
  expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);h.layer.dispose();
 });
 it('retains a preview through routine market updates and follows a changed viewport with the pointer stationary',()=>{
  const h=harness(),drawing=saved();h.layer.update([drawing],bars,60);doubleClick(h.element,150,50);pointer(h.overlay,'pointermove',200,80);
  h.layer.update([{...drawing,points:drawing.points.map(point=>({...point}))}],bars.map(bar=>({...bar})),60);expect(h.layer.read().movingId).toBe('saved');expect(h.moveChange).toHaveBeenCalledOnce();expect(h.move).not.toHaveBeenCalled();
  h.zoom(2);expect(h.layer.read().preview[0].coordinates).toEqual([{x:200,y:80}]);
  pointer(h.overlay,'pointerdown',200,80);pointer(h.overlay,'pointerup',200,80);expect(h.move).toHaveBeenCalledExactlyOnceWith('saved',[{time:1060,price:920}]);h.layer.dispose();
 });
 it.each(['removed','scope','type','points','no bars'] as const)('cancels safely if saved data is %s while moving',change=>{
  const h=harness(),drawing=saved();h.layer.update([drawing],bars,60);doubleClick(h.element,150,50);pointer(h.overlay,'pointermove',200,80);
  const changed=change==='removed'?[]:[change==='scope'?{...drawing,instrument:'ETHUSDT'}:change==='type'?{...drawing,type:'arrowDown' as const}:change==='points'?{...drawing,points:[{time:1090,price:900}]}:drawing];
  h.layer.update(changed,change==='no bars'?[]:bars,60);expect(h.layer.read().movingId).toBeNull();expect(h.layer.read().preview).toEqual([]);expect(h.move).not.toHaveBeenCalled();expect(h.moveChange.mock.calls).toEqual([[true],[false]]);expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);h.layer.dispose();
 });
 it('does not place out of bounds or at invalid prices and permits a later valid placement',()=>{
  const h=harness(),drawing=saved('trendline');h.layer.update([drawing],bars,60);doubleClick(h.element,150,80);
  pointer(h.overlay,'pointerdown',450,80);pointer(h.overlay,'pointerup',450,80);expect(h.layer.read().movingId).toBe('saved');expect(h.move).not.toHaveBeenCalled();
  // A translated endpoint can leave the pane; all resulting anchors must stay valid.
  h.layer.setTool(null);let valid=true;h.layer.setSeries({getPane:()=>h.pane,coordinateToPrice:(y:number)=>valid?1000-y:NaN,priceToCoordinate:(price:number)=>1000-price} as unknown as DrawingPriceSeries);doubleClick(h.element,150,80);valid=false;
  pointer(h.overlay,'pointerdown',200,100);pointer(h.overlay,'pointerup',200,100);expect(h.move).not.toHaveBeenCalled();expect(h.layer.read().movingId).toBe('saved');valid=true;
  pointer(h.overlay,'pointerdown',200,100);pointer(h.overlay,'pointerup',200,100);expect(h.move).toHaveBeenCalledOnce();h.layer.dispose();
 });
 it('preserves a trendline screen shape on logarithmic price scales',()=>{
  const h=harness(),price=(y:number)=>1000*Math.exp(-y/100),drawing={...saved('trendline'),points:[{time:1045,price:price(60)},{time:1135,price:price(100)}]};
  h.layer.setSeries({getPane:()=>h.pane,coordinateToPrice:price,priceToCoordinate:(value:number)=>-100*Math.log(value/1000)} as unknown as DrawingPriceSeries);h.layer.update([drawing],bars,60);doubleClick(h.element,150,80);pointer(h.overlay,'pointermove',200,130);
  const coordinates=h.layer.read().preview[0].coordinates;expect(coordinates[0].x).toBe(125);expect(coordinates[0].y).toBeCloseTo(110);expect(coordinates[1].x).toBe(275);expect(coordinates[1].y).toBeCloseTo(150);h.layer.dispose();
 });
 it('removes double-click handling on dispose and ignores selection while a drawing tool is active',()=>{
  const h=harness();h.layer.update([saved()],bars,60);h.layer.setTool('horizontal');expect(doubleClick(h.element,150,50).defaultPrevented).toBe(false);expect(h.layer.read().movingId).toBeNull();h.layer.dispose();
  expect(doubleClick(h.element,150,50).defaultPrevented).toBe(false);expect(h.moveChange).not.toHaveBeenCalled();
 });
});
