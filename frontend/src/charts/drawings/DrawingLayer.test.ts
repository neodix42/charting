import {afterEach,describe,expect,it,vi} from 'vitest';
import type {IChartApi,IPanePrimitive,PaneAttachedParameter} from 'lightweight-charts';
import type {Candle,DrawingConfig,DrawingType} from '../../types';
import {DrawingLayer} from './DrawingLayer';
import type {DrawingPriceSeries} from './DrawingPrimitive';

class Element extends EventTarget {
 style:Record<string,string>={};dataset:Record<string,string>={};className='';children:Element[]=[];parent:Element|null=null;capture=new Set<number>();tagName='DIV';isContentEditable=false;
 constructor(readonly ownerDocument:TestDocument){super();}
 setAttribute=vi.fn();
 appendChild(child:Element){this.children.push(child);child.parent=this;return child;}
 contains(child:unknown):boolean{return child===this||this.children.some(item=>item.contains(child));}
 closest(selector:string):Element|null{if(selector.startsWith('.'))return this.className.split(' ').includes(selector.slice(1))?this:this.parent?.closest(selector)??null;return ['INPUT','TEXTAREA','SELECT'].includes(this.tagName)||this.isContentEditable?this:this.parent?.closest(selector)??null;}
 remove(){if(this.parent)this.parent.children=this.parent.children.filter(child=>child!==this);}
 getBoundingClientRect(){return {left:10+Number.parseFloat(this.style.left||'0'),top:20+Number.parseFloat(this.style.top||'0'),width:Number.parseFloat(this.style.width||'400'),height:Number.parseFloat(this.style.height||'300')};}
 setPointerCapture(id:number){this.capture.add(id);}hasPointerCapture(id:number){return this.capture.has(id);}releasePointerCapture(id:number){this.capture.delete(id);}
}
class TestDocument extends EventTarget {modal=false;createElement(){return new Element(this);}querySelector(){return this.modal?{}:null;}}
function pointer(element:Element,type:string,x:number,y:number,id=1){
 const event=new Event(type,{cancelable:true});Object.assign(event,{clientX:x+10,clientY:y+20,pointerId:id,button:0});element.dispatchEvent(event);return event;
}
function doubleClick(element:Element,x:number,y:number){return pointer(element,'dblclick',x,y);}
function escape(document:TestDocument){const event=new Event('keydown',{cancelable:true});Object.assign(event,{key:'Escape'});document.dispatchEvent(event);return event;}
function key(document:TestDocument,value:string,properties:Record<string,unknown>={}){const event=new Event('keydown',{cancelable:true});Object.assign(event,{key:value});for(const [name,property] of Object.entries(properties))Object.defineProperty(event,name,{value:property});document.dispatchEvent(event);return event;}
function click(element:Element,x:number,y:number,id=1){pointer(element,'pointerdown',x,y,id);return pointer(element,'pointerup',x,y,id);}
const bars=[{time:1000},{time:1060},{time:1120}] as Candle[];
function harness(position=''){
 const document=new TestDocument(),element=new Element(document),paneElement=new Element(document),options={handleScale:{mouseWheel:true,pinch:false,axisPressedMouseMove:{time:true,price:false}},handleScroll:{mouseWheel:false,pressedMouseMove:true,horzTouchDrag:true,vertTouchDrag:false}},commit=vi.fn(),toolChange=vi.fn(),move=vi.fn(),moveChange=vi.fn(),selectionChange=vi.fn(),remove=vi.fn();let primitive:IPanePrimitive|null=null,zoom=1,leftScaleWidth=0;
 element.style.position=position;
 const pane={getHTMLElement:()=>paneElement,paneIndex:()=>0,attachPrimitive:vi.fn((value:IPanePrimitive)=>{primitive=value;value.attached?.({chart,requestUpdate:vi.fn()} as unknown as PaneAttachedParameter);}),detachPrimitive:vi.fn((value:IPanePrimitive)=>value.detached?.())};
 const chart={chartElement:()=>element,priceScale:()=>({width:()=>leftScaleWidth}),paneSize:()=>({width:400,height:300}),timeScale:()=>({timeToIndex:(time:number,nearest?:boolean)=>{const index=bars.findIndex(bar=>bar.time===time);return index!==-1?index:nearest?Math.min(2,Math.max(0,Math.ceil((time-1000)/60))):null;},logicalToCoordinate:(logical:number)=>logical*100*zoom,coordinateToLogical:(x:number)=>x/100/zoom,coordinateToTime:(x:number)=>bars[Math.round(x/100/zoom)]?.time??null}),options:()=>options,applyOptions:vi.fn()};
 const series={getPane:()=>pane,coordinateToPrice:(y:number)=>1000-y,priceToCoordinate:(price:number)=>1000-price};
 const layer=new DrawingLayer(chart as unknown as IChartApi,series as unknown as DrawingPriceSeries,commit,toolChange,move,moveChange,selectionChange,remove);layer.update([],bars,60);
 return {layer,overlay:element.children[0],element,pane,paneElement,document,chart,commit,toolChange,move,moveChange,selectionChange,remove,options,leftScale:(value:number)=>{leftScaleWidth=value;},zoom:(value:number)=>{zoom=value;primitive?.updateAllViews?.();}};
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

describe('selecting and editing saved drawings',()=>{
 it('selects the topmost item without taking chart controls, clears a miss, and leaves normal drags to pan',()=>{
  const h=harness();h.layer.update([saved('arrowUp','under'),saved('horizontal','top')],bars,60);const initial=h.chart.applyOptions.mock.calls.length;
  expect(click(h.element,150,50).defaultPrevented).toBe(false);expect(h.layer.read().selectedId).toBe('top');expect(h.selectionChange).toHaveBeenLastCalledWith('top');expect(h.overlay.style.display).toBe('none');expect(h.chart.applyOptions).toHaveBeenCalledTimes(initial);
  pointer(h.element,'pointerdown',250,50);pointer(h.element,'pointermove',300,70);pointer(h.element,'pointerup',300,70);expect(h.layer.read().selectedId).toBe('top');expect(h.move).not.toHaveBeenCalled();expect(h.chart.applyOptions).toHaveBeenCalledTimes(initial);
  click(h.element,300,200);expect(h.layer.read().selectedId).toBeNull();expect(h.selectionChange).toHaveBeenLastCalledWith(null);
  pointer(h.element,'pointerdown',150,50);pointer(h.element,'pointermove',200,80);pointer(h.element,'pointerup',150,50);expect(h.layer.read().selectedId).toBeNull();h.layer.dispose();
 });
 it('keeps only one selected drawing across chart instances and allows the owning toolbar to retain selection',()=>{
  const a=harness(),b=harness();a.layer.update([saved('arrowUp','a')],bars,60);b.layer.update([saved('arrowUp','b')],bars,60);
  click(a.element,150,50);click(b.element,150,50);expect(a.layer.read().selectedId).toBeNull();expect(b.layer.read().selectedId).toBe('b');
  const cell=new Element(b.document),toolbar=new Element(b.document),button=new Element(b.document);cell.className='chart-cell';toolbar.className='drawing-toolbar';cell.appendChild(b.element);cell.appendChild(toolbar);toolbar.appendChild(button);
  b.document.dispatchEvent(Object.defineProperty(new Event('pointerdown'),'target',{value:button}));expect(b.layer.read().selectedId).toBe('b');
  b.document.dispatchEvent(Object.defineProperty(new Event('pointerdown'),'target',{value:new Element(b.document)}));expect(b.layer.read().selectedId).toBeNull();a.layer.dispose();b.layer.dispose();
 });
 it.each(['selection','resize','move'] as const)('preserves a pending %s when Save is clicked and committed market data refreshes',mode=>{
  const h=harness(),drawing=saved('trendline');h.layer.update([drawing],bars,60);click(h.element,150,80);
  if(mode==='resize'){pointer(h.element,'pointerdown',75,60,7);pointer(h.overlay,'pointermove',125,90,7);}
  if(mode==='move'){doubleClick(h.element,150,80);pointer(h.overlay,'pointermove',200,130,7);}
  const before=h.layer.read(),save=new Element(h.document),label=new Element(h.document);save.className='save-indicator saved';label.className='save-label';save.appendChild(label);
  const event=Object.defineProperty(new Event('pointerdown',{cancelable:true}),'target',{value:label});h.document.dispatchEvent(event);
  h.layer.update([{...drawing,points:drawing.points.map(point=>({...point}))}],bars.map(bar=>({...bar})),60);
  expect(event.defaultPrevented).toBe(false);expect(h.layer.read()).toMatchObject({selectedId:'saved',movingId:before.movingId,editingId:before.editingId,preview:before.preview});expect(h.move).not.toHaveBeenCalled();expect(h.remove).not.toHaveBeenCalled();expect(h.commit).not.toHaveBeenCalled();expect(drawing.points).toEqual(saved('trendline').points);h.layer.dispose();
 });
 it.each(['Delete','Del'])('deletes exactly the selected item with the %s key and clears the selection',value=>{
  const h=harness();h.layer.update([saved('arrowUp','under'),saved('horizontal','top')],bars,60);click(h.element,150,50);
  expect(key(h.document,value).defaultPrevented).toBe(true);expect(h.remove).toHaveBeenCalledExactlyOnceWith('top');expect(h.layer.read().selectedId).toBeNull();expect(key(h.document,value).defaultPrevented).toBe(false);h.layer.dispose();
 });
 it('offers the same deletion through its public action and does nothing without selection',()=>{
  const h=harness();h.layer.update([saved()],bars,60);h.layer.deleteSelected();expect(h.remove).not.toHaveBeenCalled();click(h.element,150,50);h.layer.deleteSelected();expect(h.remove).toHaveBeenCalledExactlyOnceWith('saved');h.layer.dispose();
 });
 it('ignores Delete while typing, in dialogs, on modifier chords, repeated events, and in unrelated controls',()=>{
  const h=harness();h.layer.update([saved('trendline')],bars,60);click(h.element,150,80);
  for(const tagName of ['INPUT','TEXTAREA','SELECT']){const target=new Element(h.document);target.tagName=tagName;expect(key(h.document,'Delete',{target}).defaultPrevented).toBe(false);}
  const editable=new Element(h.document);editable.isContentEditable=true;expect(key(h.document,'Delete',{target:editable}).defaultPrevented).toBe(false);
  const outsideButton=new Element(h.document);outsideButton.tagName='BUTTON';expect(key(h.document,'Delete',{target:outsideButton}).defaultPrevented).toBe(false);
  for(const property of ['ctrlKey','altKey','metaKey','shiftKey','repeat'])expect(key(h.document,'Delete',{[property]:true}).defaultPrevented).toBe(false);
  h.document.modal=true;expect(key(h.document,'Delete').defaultPrevented).toBe(false);h.document.modal=false;
  expect(h.remove).not.toHaveBeenCalled();h.layer.dispose();
 });
 it.each(['resize keyboard','resize action','move keyboard','move action'] as const)('deletes a selected item during a pending %s without saving preview anchors',mode=>{
  const h=harness(),drawing=saved('trendline');h.layer.update([drawing],bars,60);
  if(mode.startsWith('resize')){click(h.element,150,80);pointer(h.element,'pointerdown',75,60,7);pointer(h.overlay,'pointermove',125,90,7);expect(h.layer.read().editingId).toBe('saved');}
  else{doubleClick(h.element,150,80);pointer(h.overlay,'pointermove',200,130,7);expect(h.layer.read().movingId).toBe('saved');}
  if(mode.endsWith('keyboard'))expect(key(h.document,'Delete').defaultPrevented).toBe(true);else h.layer.deleteSelected();
  expect(h.remove).toHaveBeenCalledExactlyOnceWith('saved');expect(h.move).not.toHaveBeenCalled();expect(h.layer.read()).toMatchObject({selectedId:null,editingId:null,movingId:null,preview:[]});expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);expect(h.overlay.capture.size).toBe(0);expect(drawing.points).toEqual(saved('trendline').points);pointer(h.overlay,'pointerup',125,90,7);expect(h.move).not.toHaveBeenCalled();h.layer.dispose();
 });
 it('moves only the grabbed trendline edge, previews without saving, and retains selection for the next edit',()=>{
  const h=harness(),drawing=saved('trendline');h.layer.update([drawing],bars,60);click(h.element,150,80);
  expect(pointer(h.element,'pointerdown',75,60,7).defaultPrevented).toBe(true);expect(h.layer.read()).toMatchObject({selectedId:'saved',editingId:'saved',editingIndex:0,drawings:[]});expect(h.overlay.capture.has(7)).toBe(true);
  pointer(h.overlay,'pointermove',125,90,7);expect(h.layer.read().preview[0].points).toEqual([{time:1075,price:910},drawing.points[1]]);expect(h.move).not.toHaveBeenCalled();expect(drawing.points).toEqual(saved('trendline').points);
  pointer(h.overlay,'pointerup',125,90,7);expect(h.move).toHaveBeenCalledExactlyOnceWith('saved',[{time:1075,price:910},drawing.points[1]]);expect(h.layer.read()).toMatchObject({selectedId:'saved',editingId:null,preview:[]});expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);expect(h.moveChange.mock.calls).toEqual([[true,true],[false]]);
  const points=h.move.mock.calls[0][1];h.layer.update([{...drawing,points}],bars,60);expect(h.layer.read().selectedId).toBe('saved');h.layer.dispose();
 });
 it('does not resize a line until it was selected by a completed click',()=>{
  const h=harness();h.layer.update([saved('trendline')],bars,60);expect(pointer(h.element,'pointerdown',75,60).defaultPrevented).toBe(false);pointer(h.element,'pointermove',125,90);pointer(h.element,'pointerup',125,90);expect(h.layer.read()).toMatchObject({selectedId:null,editingId:null});expect(h.move).not.toHaveBeenCalled();h.layer.dispose();
 });
 it('uses two virtual handles for legacy horizontal lines and persists finite horizontal endpoints only after a drag',()=>{
  const h=harness(),drawing=saved('horizontal');h.layer.update([drawing],bars,60);click(h.element,300,50);const handles=h.layer.read().selection!.coordinates;expect(handles).toHaveLength(2);
  pointer(h.element,'pointerdown',handles[0].x,handles[0].y);pointer(h.overlay,'pointerup',handles[0].x,handles[0].y);expect(h.move).not.toHaveBeenCalled();expect(h.layer.read().drawings[0].points).toEqual(drawing.points);
  pointer(h.element,'pointerdown',handles[1].x,handles[1].y);pointer(h.overlay,'pointermove',handles[1].x-35,handles[1].y+25);pointer(h.overlay,'pointerup',handles[1].x-35,handles[1].y+25);
  expect(h.move).toHaveBeenCalledOnce();const points=h.move.mock.calls[0][1];expect(points).toHaveLength(2);expect(points[0].price).toBe(925);expect(points[1].price).toBe(925);expect(points[0].time).toBe(Math.round(1000+handles[0].x*.6));expect(points[1].time).toBe(Math.round(1000+(handles[1].x-35)*.6));expect(drawing.points).toHaveLength(1);h.layer.dispose();
 });
 it('resizes finite horizontal lines without shifting the other endpoint time and rejects zero width',()=>{
  const h=harness(),drawing={...saved('horizontal'),points:[{time:1045,price:940},{time:1135,price:940}]};h.layer.update([drawing],bars,60);click(h.element,150,60);
  pointer(h.element,'pointerdown',75,60);pointer(h.overlay,'pointermove',225,90);pointer(h.overlay,'pointerup',225,90);expect(h.move).not.toHaveBeenCalled();expect(h.layer.read().editingId).toBeNull();
  pointer(h.element,'pointerdown',75,60);pointer(h.overlay,'pointermove',100,80);pointer(h.overlay,'pointerup',100,80);expect(h.move).toHaveBeenCalledExactlyOnceWith('saved',[{time:1060,price:920},{time:1135,price:920}]);h.layer.dispose();
 });
 it.each(['Escape','pointercancel','tool','outside','series','dispose'] as const)('cancels endpoint previews using %s without changing saved points',cancel=>{
  const h=harness(),drawing=saved('trendline');h.layer.update([drawing],bars,60);click(h.element,150,80);pointer(h.element,'pointerdown',75,60,7);pointer(h.overlay,'pointermove',125,90,7);
  if(cancel==='Escape')expect(escape(h.document).defaultPrevented).toBe(true);
  if(cancel==='pointercancel')pointer(h.overlay,'pointercancel',125,90,7);
  if(cancel==='tool')h.layer.setTool('arrowDown');
  if(cancel==='outside')h.document.dispatchEvent(Object.defineProperty(new Event('pointerdown'),'target',{value:new Element(h.document)}));
  if(cancel==='series')h.layer.setSeries({getPane:()=>h.pane,coordinateToPrice:(y:number)=>1000-y,priceToCoordinate:(price:number)=>1000-price} as unknown as DrawingPriceSeries);
  if(cancel==='dispose')h.layer.dispose();
  expect(h.layer.read()).toMatchObject({selectedId:null,editingId:null});expect(h.move).not.toHaveBeenCalled();expect(h.overlay.capture.size).toBe(0);expect(drawing.points).toEqual(saved('trendline').points);h.layer.dispose();
 });
 it('retains selection and a resize through market refreshes, and clears them if the saved item changes',()=>{
  const h=harness(),drawing=saved('trendline');h.layer.update([drawing],bars,60);click(h.element,150,80);pointer(h.element,'pointerdown',75,60);pointer(h.overlay,'pointermove',125,90);
  h.layer.update([{...drawing,points:drawing.points.map(point=>({...point}))}],bars.map(bar=>({...bar})),60);expect(h.layer.read()).toMatchObject({selectedId:'saved',editingId:'saved'});
  h.layer.update([{...drawing,instrument:'ETHUSDT'}],bars,60);expect(h.layer.read()).toMatchObject({selectedId:null,editingId:null,preview:[]});expect(h.move).not.toHaveBeenCalled();expect(h.overlay.capture.size).toBe(0);expect(h.chart.applyOptions).toHaveBeenLastCalledWith(h.options);h.layer.dispose();
 });
 it('removes selection, deletion, and handle editing handlers on disposal',()=>{
  const h=harness();h.layer.update([saved()],bars,60);click(h.element,150,50);h.layer.dispose();click(h.element,150,50);key(h.document,'Delete');expect(h.remove).not.toHaveBeenCalled();expect(h.layer.read().selectedId).toBeNull();
 });
});
