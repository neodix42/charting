import {describe,it,expect,vi} from 'vitest';
import type {IChartApi,ISeriesApi} from 'lightweight-charts';
import type {Candle,IndicatorConfig} from '../../types';
import {IndicatorLayer} from './IndicatorLayer';
import {calendarRangeValues,ichimoku,macd} from './math';
import {calendarRangeNames,type CalendarRangeType} from '../../../../screener-runtime/calendar-ranges.mjs';

function chartHarness(){
 type PriceLine={options:Record<string,unknown>;applyOptions:ReturnType<typeof vi.fn>};
 type FakeSeries={data:{time:number;value:number;color?:string}[];options:Record<string,unknown>;priceLines:PriceLine[];setData:ReturnType<typeof vi.fn>;applyOptions:ReturnType<typeof vi.fn>;createPriceLine:ReturnType<typeof vi.fn>;priceScale:()=>{applyOptions:ReturnType<typeof vi.fn>};attachPrimitive:ReturnType<typeof vi.fn>;detachPrimitive:ReturnType<typeof vi.fn>;priceToCoordinate:(price:number)=>number};
 type Pane={series:Set<FakeSeries>;factor:number;paneIndex:()=>number;setStretchFactor:ReturnType<typeof vi.fn>;getStretchFactor:()=>number;getHTMLElement:()=>null};
 const panes:Pane[]=[],removed:unknown[]=[],range={from:1000,to:4500},restore=vi.fn(),scale={getVisibleRange:()=>range,setVisibleRange:restore,timeToCoordinate:(time:number)=>time};
 function makePane():Pane{const pane:Pane={series:new Set(),factor:panes.length?1:2,paneIndex:()=>panes.indexOf(pane),setStretchFactor:vi.fn((factor:number)=>{pane.factor=factor;}),getStretchFactor:()=>pane.factor,getHTMLElement:()=>null};panes.push(pane);return pane;}
 function makeSeries(options:unknown,pane:Pane):FakeSeries{
  const series:FakeSeries={data:[],options:options as Record<string,unknown>,priceLines:[],setData:vi.fn((data:{time:number;value:number;color?:string}[])=>{series.data=data;}),applyOptions:vi.fn((options:Record<string,unknown>)=>Object.assign(series.options,options)),createPriceLine:vi.fn((options:Record<string,unknown>)=>{const line:PriceLine={options,applyOptions:vi.fn((value:Record<string,unknown>)=>Object.assign(line.options,value))};series.priceLines.push(line);return line;}),priceScale:()=>({applyOptions:vi.fn()}),attachPrimitive:vi.fn((primitive:{attached:(value:unknown)=>void})=>primitive.attached({chart,series,requestUpdate:vi.fn()})),detachPrimitive:vi.fn((primitive:{detached:()=>void})=>primitive.detached()),priceToCoordinate:(price:number)=>price};pane.series.add(series);return series;
 }
 const first=makePane(),main=makeSeries({},first);
 const chart={timeScale:()=>scale,panes:()=>panes,addPane:makePane,addSeries:vi.fn((_definition:unknown,options:unknown,index=0)=>makeSeries(options,panes[index])),removeSeries:vi.fn((series:ReturnType<typeof makeSeries>)=>{removed.push(series);const index=panes.findIndex(pane=>pane.series.has(series));if(index<0)throw Error('Removing an already removed series');panes[index].series.delete(series);if(index>0&&!panes[index].series.size)panes.splice(index,1);})};
 return {chart:chart as unknown as IChartApi,main:main as unknown as ISeriesApi<'Candlestick'>,panes,removed,restore};
}
const candles:Candle[]=Array.from({length:80},(_,i)=>({time:1000+i*60,open:i+10,high:i+12,low:i+9,close:i+11,volume:5,closed:true,quality:'authoritative',sourceRevision:1}));
const config=(id:string,type:IndicatorConfig['type'],parameters:IndicatorConfig['parameters']={}):IndicatorConfig=>({id,type,parameters});

describe('indicator series ownership',()=>{
 it('renders all day/week overlays on the price pane, updates corrections, suspends D1/W1 and disposes without rebuilding other series',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),ids=Object.keys(calendarRangeNames) as CalendarRangeType[],configs=ids.map(id=>config(id,id));
  const bars=Array.from({length:400},(_,i)=>({...candles[0],time:1704067200+i*3600,high:20+i%10,low:5-i%5}));
  layer.update(bars,configs,'1h');const lines=[...h.panes[0].series].slice(1);
  expect(h.panes).toHaveLength(1);expect(lines).toHaveLength(ids.length);expect(h.chart.addSeries).toHaveBeenCalledTimes(ids.length);
  ids.forEach((id,index)=>expect(lines[index].data).toEqual(calendarRangeValues(bars,id,'1h')));
  const corrected=[...bars.slice(0,-1),{...bars.at(-1)!,low:-1,high:100}];layer.update(corrected,configs,'1h');
  ids.forEach((id,index)=>expect(lines[index].data).toEqual(calendarRangeValues(corrected,id,'1h')));
  layer.update(corrected,configs,'1d');expect(lines.every(line=>!line.data.length)).toBe(true);
  layer.update(corrected,configs,'1h');expect(h.chart.addSeries).toHaveBeenCalledTimes(ids.length);expect(h.removed).toHaveLength(0);
  layer.dispose();expect(h.removed).toHaveLength(ids.length);expect(h.panes[0].series.size).toBe(1);
 });
 it('keeps only the selected Ichimoku line and removes hidden projections from the timeline',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),original=config('cloud','ichimoku'),baseOnly={...original,visibility:{conversion:false,spanA:false,spanB:false,lagging:false,cloud:false}};
  layer.update(candles,[original],'1m');const lines=[...h.panes[0].series].slice(1),cloud=lines[2].attachPrimitive.mock.calls[0][0],updates=vi.spyOn(cloud,'setData');
  const baseValues=ichimoku(candles).base.map(point=>({time:point.time,value:point.value}));
  layer.update(candles,[baseOnly],'1m');expect(lines[1].data).toEqual(baseValues);expect(lines[1].options).toMatchObject({visible:true,lineVisible:true});
  for(const index of [0,2,3,4]){expect(lines[index].data).toEqual([]);expect(lines[index].options).toMatchObject({visible:false,lineVisible:false});}
  expect(updates).toHaveBeenLastCalledWith([]);expect(h.chart.addSeries).toHaveBeenCalledTimes(5);expect(h.removed).toEqual([]);
  expect(lines.flatMap(line=>line.data).every(point=>point.time<=candles.at(-1)!.time)).toBe(true);
  const conversionOnly={...original,visibility:{base:false,spanA:false,spanB:false,lagging:false,cloud:false}};
  layer.update(candles,[conversionOnly],'1m');expect(lines[0].data).toEqual(ichimoku(candles).conversion);expect(lines[1].data).toEqual([]);
  const allHidden={...baseOnly,visibility:{...baseOnly.visibility,base:false}};
  layer.update(candles,[allHidden],'1m');expect(lines.every(line=>line.data.length===0&&line.options.visible===false)).toBe(true);expect(updates).toHaveBeenLastCalledWith([]);
  const correction=[...candles.slice(0,-1),{...candles.at(-1)!,high:200,low:0,close:199,sourceRevision:2}];
  layer.update(correction,[allHidden],'1m');expect(lines.every(line=>line.data.length===0)).toBe(true);
  layer.update(correction,[original],'1m');const values=ichimoku(correction);
  expect(lines.map(line=>line.data)).toEqual([values.conversion,values.base,values.leadingA,values.leadingB,values.lagging]);expect(updates).toHaveBeenLastCalledWith(values.cloud);
  expect(lines.every(line=>line.options.visible===true&&line.options.lineVisible===true)).toBe(true);expect(h.removed).toEqual([]);
  expect([...h.panes[0].series].slice(1)).toEqual(lines);layer.dispose();expect(h.removed).toEqual(lines);expect(lines[2].detachPrimitive).toHaveBeenCalledOnce();
 });
 it('renders the cloud fill independently of both span outlines using only its actual projected points',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),cloudOnly={...config('cloud','ichimoku'),visibility:{conversion:false,base:false,spanA:false,spanB:false,lagging:false}};
  layer.update(candles,[cloudOnly],'1m');const lines=[...h.panes[0].series].slice(1),values=ichimoku(candles),carrier=lines[2];
  expect(carrier.options).toMatchObject({visible:true,lineVisible:false});expect(carrier.data).toEqual(values.cloud.map(point=>({time:point.time,value:point.a})));
  expect(carrier.data[0].time).toBeGreaterThan(values.leadingA[0].time);expect(carrier.data.at(-1)!.time).toBeGreaterThan(candles.at(-1)!.time);
  for(const index of [0,1,3,4])expect(lines[index].data).toEqual([]);
  const cloud=carrier.attachPrimitive.mock.calls[0][0],updates=vi.spyOn(cloud,'setData');
  layer.update(candles,[{...cloudOnly,visibility:{...cloudOnly.visibility,cloud:false}}],'1m');expect(carrier.data).toEqual([]);expect(carrier.options).toMatchObject({visible:false,lineVisible:false});expect(updates).toHaveBeenLastCalledWith([]);
  layer.update(candles,[cloudOnly],'1m');expect(carrier.data).toEqual(values.cloud.map(point=>({time:point.time,value:point.a})));expect(updates).toHaveBeenLastCalledWith(values.cloud);expect(h.chart.addSeries).toHaveBeenCalledTimes(5);
 });
 it('switches MACD outputs and its zero baseline independently without retaining hidden scale values',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),original=config('momentum','macd');
  layer.update(candles,[original],'1m');const pane=h.panes[1],series=[...pane.series],histogram=series[0],line=series[1],signal=series[2],zero=line.priceLines[0],originalData=line.data;
  const allHidden={...original,visibility:{macd:false,signal:false,histogram:false,zero:false}};pane.factor=1.7;
  layer.update(candles,[allHidden],'1m');expect(series.every(item=>item.data.length===0&&item.options.visible===false)).toBe(true);expect(zero.options.lineVisible).toBe(false);expect(pane.factor).toBe(1.7);
  layer.update(candles,[{...allHidden,visibility:{...allHidden.visibility,zero:true}}],'1m');expect(line.options).toMatchObject({visible:true,lineVisible:false});expect(line.data).toEqual(originalData.map(point=>({...point,value:0})));expect(zero.options.lineVisible).toBe(true);expect(signal.data).toEqual([]);expect(histogram.data).toEqual([]);
  layer.update(candles,[{...allHidden,visibility:{...allHidden.visibility,histogram:true}}],'1m');expect(line.data).toEqual([]);expect(line.options.visible).toBe(false);expect(zero.options.lineVisible).toBe(false);expect(histogram.data.length).toBeGreaterThan(0);expect(histogram.options.visible).toBe(true);
  expect((histogram.options.priceFormat as {minMove:number}).minMove).toBeLessThanOrEqual(1e-8);
  layer.update(candles,[{...allHidden,visibility:{...allHidden.visibility,signal:true}}],'1m');expect(histogram.data).toEqual([]);expect(signal.data).toEqual(macd(candles).signal);expect(signal.options.visible).toBe(true);expect(line.data).toEqual([]);
  layer.update(candles,[{...allHidden,visibility:{...allHidden.visibility,macd:true}}],'1m');expect(line.data).toEqual(originalData);expect(line.options).toMatchObject({visible:true,lineVisible:true});expect(signal.data).toEqual([]);expect(histogram.data).toEqual([]);
  const provider=line.options.autoscaleInfoProvider as (original:()=>{priceRange:{minValue:number;maxValue:number}})=>unknown;
  expect(provider(()=>({priceRange:{minValue:5,maxValue:10}}))).toEqual({priceRange:{minValue:5,maxValue:10}});
  layer.update(candles,[original],'1m');const zeroProvider=line.options.autoscaleInfoProvider as typeof provider;
  expect(zeroProvider(()=>({priceRange:{minValue:5,maxValue:10}}))).toEqual({priceRange:{minValue:0,maxValue:10}});expect(zeroProvider(()=>({priceRange:{minValue:-10,maxValue:-5}}))).toEqual({priceRange:{minValue:-10,maxValue:0}});
  expect(pane.factor).toBe(1.7);expect([...pane.series]).toEqual(series);expect(h.removed).toEqual([]);expect(h.chart.addSeries).toHaveBeenCalledTimes(3);
  const correction=[...candles.slice(0,-1),{...candles.at(-1)!,close:200,sourceRevision:2}];layer.update(correction,[allHidden],'1m');layer.update(correction,[original],'1m');expect(line.data).toEqual(macd(correction).macd);expect(signal.data).toEqual(macd(correction).signal);expect(zero.options.lineVisible).toBe(true);
  layer.dispose();expect(h.removed).toEqual(expect.arrayContaining(series));expect(h.panes).toHaveLength(1);
 });
 it('treats omitted or explicitly enabled plots equally without mutating legacy configs',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),configs=[config('cloud','ichimoku'),config('momentum','macd')];
  layer.update(candles,configs,'1m');const series=h.panes.flatMap(pane=>[...pane.series]).slice(1),writes=series.map(item=>item.setData.mock.calls.length);
  layer.update(candles,[{...configs[0],visibility:{cloud:true,base:true,conversion:true,spanB:true,spanA:true,lagging:true}},{...configs[1],visibility:{zero:true,histogram:true,signal:true,macd:true}}],'1m');
  expect(series.map(item=>item.setData.mock.calls.length)).toEqual(writes);expect(configs.every(item=>!Object.hasOwn(item,'visibility'))).toBe(true);expect(h.restore).toHaveBeenCalledTimes(1);
 });
 it.each(['low','high'] as const)('renders %s on the price pane and reuses/removes its series after corrections and edits',type=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),configs=[config('range',type,{period:3})];
  layer.update(candles.slice(0,2),configs,'1m');const line=[...h.panes[0].series][1];expect(line.data).toEqual([]);
  layer.update(candles.slice(0,3),configs,'1m');expect(line.data).toEqual([{time:candles[2].time,value:type==='low'?9:14}]);expect(line.applyOptions).toHaveBeenLastCalledWith({pointMarkersVisible:true,pointMarkersRadius:3});
  const corrected=[...candles.slice(0,3),{...candles[3],low:0,high:100}];layer.update(corrected,configs,'1m');expect(line.data.at(-1)!.value).toBe(type==='low'?0:100);
  layer.update(corrected,[{...configs[0],parameters:{period:1},color:'#123456'}],'1m');expect(line.data).toHaveLength(4);expect(h.chart.addSeries).toHaveBeenCalledTimes(1);expect(h.panes).toHaveLength(1);
  layer.dispose();expect(h.removed).toEqual([line]);expect(h.panes[0].series.size).toBe(1);
 });

 it.each(['sma','ema'] as const)('defaults legacy %s to width 2 and applies all supported widths to the same series',type=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),legacy=config('average',type,{period:3});
  layer.update(candles,[legacy],'1m');const average=[...h.panes[0].series][1];
  expect(average.options.lineWidth).toBe(2);expect(average.applyOptions).toHaveBeenLastCalledWith(expect.objectContaining({lineWidth:2}));expect(legacy).not.toHaveProperty('lineWidth');
  const writes=average.setData.mock.calls.length;layer.update(candles,[{...legacy,lineWidth:2}],'1m');expect(average.setData).toHaveBeenCalledTimes(writes);
  for(const lineWidth of [1,2,3,4] as const){layer.update(candles,[{...legacy,lineWidth}],'1m');expect(average.applyOptions).toHaveBeenLastCalledWith(expect.objectContaining({lineWidth}));expect([...h.panes[0].series][1]).toBe(average);}
  expect(h.chart.addSeries).toHaveBeenCalledTimes(1);expect(h.removed).toHaveLength(0);
  const resized={...legacy,lineWidth:4 as const,parameters:{period:8}};layer.update(candles,[resized],'1m');expect(average.applyOptions).toHaveBeenLastCalledWith(expect.objectContaining({lineWidth:4}));
  layer.update(candles,[legacy],'1m');expect(average.applyOptions).toHaveBeenLastCalledWith(expect.objectContaining({lineWidth:2}));expect(legacy).not.toHaveProperty('lineWidth');layer.dispose();
 });
 it('editing an average width leaves other overlay settings and resized MACD panes intact',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),average={...config('average','sma',{period:5}),lineWidth:1 as const},momentum={...config('momentum','macd'),paneStretchFactor:.8},cloud=config('cloud','ichimoku');
  layer.update(candles,[average,momentum,cloud],'1m');const allSeries=h.panes.flatMap(pane=>[...pane.series]),macdPane=h.panes[1];macdPane.factor=2.5;
  layer.update(candles,[{...average,lineWidth:4},momentum,cloud],'1m');expect(h.panes.flatMap(pane=>[...pane.series])).toEqual(allSeries);expect(h.panes[1]).toBe(macdPane);expect(macdPane.factor).toBe(2.5);
  expect(allSeries[1].applyOptions).toHaveBeenLastCalledWith(expect.objectContaining({lineWidth:4}));expect(allSeries.filter(series=>series!==allSeries[0]&&series!==allSeries[1]).every(series=>series.options.lineWidth===undefined||series.options.lineWidth===1)).toBe(true);expect(h.removed).toHaveLength(0);layer.dispose();
 });

 it('captures every resized MACD ratio without resetting it on ticks, parameter edits or persistence echoes',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),configs=[config('one','macd'),config('two','macd')];
  layer.update(candles,configs,'1m');expect(h.panes.map(pane=>pane.factor)).toEqual([2,.3,.3]);expect(layer.capturePaneLayout(configs)).toBeNull();
  h.panes[0].factor=1.2;h.panes[1].factor=1.1;
  const resized=layer.capturePaneLayout(configs)!;expect(resized[0].paneStretchFactor).toBeCloseTo(1.1/1.2,8);expect(resized[1].paneStretchFactor).toBe(.25);expect(configs[0].paneStretchFactor).toBeUndefined();
  const firstSeries=[...h.panes[1].series][1],writes=firstSeries.setData.mock.calls.length;
  layer.update(candles,resized,'1m');expect(firstSeries.setData).toHaveBeenCalledTimes(writes);expect(h.restore).toHaveBeenCalledTimes(1);
  // A newer pointer movement can precede the React render acknowledging an earlier capture.
  h.panes[1].factor=1.3;layer.update(candles,resized,'1m');expect(h.panes[1].factor).toBe(1.3);
  layer.update([...candles.slice(0,-1),{...candles.at(-1)!,close:200}],resized,'1m');expect(h.panes[1].factor).toBe(1.3);
  layer.update(candles,[{...resized[0],parameters:{fast:5,slow:20,signal:4}},resized[1]],'1m');expect(h.panes[1].factor).toBe(1.3);
 });
 it('restores ratios by indicator identity and keeps the surviving pane after an earlier MACD is removed',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),configs=[{...config('one','macd'),paneStretchFactor:.8},{...config('two','macd'),paneStretchFactor:1.2}];
  layer.update(candles,configs,'1m');expect(h.panes.map(pane=>pane.factor)).toEqual([2,1.6,2.4]);expect(layer.capturePaneLayout(configs)).toBeNull();
  const survivor=h.panes[2];layer.update(candles,[configs[1]],'1m');expect(h.panes[1]).toBe(survivor);expect(survivor.factor).toBe(2.4);
  h.panes[0].factor=1;const capture=layer.capturePaneLayout([configs[1]])!;expect(capture[0]).toMatchObject({id:'two',paneStretchFactor:2.4});
  layer.dispose();expect(layer.capturePaneLayout(configs)).toBeNull();
 });
 it('keeps separate MACD panes and price overlays, reuses series on updates, and removes exactly its own resources',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),configs=[config('average','sma',{period:5}),config('cloud','ichimoku'),config('momentum-1','macd'),config('momentum-2','macd',{fast:3,slow:6,signal:2})];
  layer.update(candles,configs,'1m');expect(h.panes.map(pane=>pane.series.size)).toEqual([7,3,3]);expect(h.restore).toHaveBeenCalledWith({from:1000,to:4500});
  const baseSeries=[...h.panes[0].series],firstMACD=[...h.panes[1].series],secondMACD=[...h.panes[2].series];
  expect(firstMACD[0].options.base).toBe(0);expect(firstMACD[1].createPriceLine).toHaveBeenCalledWith(expect.objectContaining({price:0}));
  expect(firstMACD[0].data.every(point=>Math.abs(point.value)<1e-10)).toBe(true);
  const changed=[config('average','sma',{period:10}),config('cloud','ichimoku'),configs[3]];layer.update(candles,changed,'1m');
  expect(h.panes.map(pane=>pane.series.size)).toEqual([7,3]);expect([...h.panes[0].series]).toEqual(baseSeries);expect([...h.panes[1].series]).toEqual(secondMACD);expect(h.removed).toEqual(expect.arrayContaining(firstMACD));
  layer.dispose();expect(h.panes).toHaveLength(1);expect(h.panes[0].series.size).toBe(1);expect(h.panes[0].series.has(h.main as never)).toBe(true);
  const removed=h.removed.length;layer.dispose();layer.update(candles,configs,'1m');expect(h.removed).toHaveLength(removed);
 });
 it('redraws corrected bars and parameter changes without duplicating series or altering viewport on each live tick',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),configs=[config('average','ema',{period:3}),config('cloud','ichimoku')];
  layer.update(candles,configs,'1m');const series=[...h.panes[0].series],average=series[1],original=average.data.at(-1)!.value;
  const correction=[...candles.slice(0,-1),{...candles.at(-1)!,close:200,closed:false,sourceRevision:2}];layer.update(correction,configs,'1m');
  expect([...h.panes[0].series]).toEqual(series);expect(average.data.at(-1)!.value).toBeGreaterThan(original);expect(h.restore).toHaveBeenCalledTimes(1);
  layer.update(correction,[], '1m');expect(h.panes[0].series.size).toBe(1);expect(h.restore).toHaveBeenCalledTimes(2);
 });
 it('keeps tiny asset MACD values visible with a shared significant-digit format on all three series',()=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),input=candles.map(bar=>({...bar,open:Number(bar.open)*1e-15,high:Number(bar.high)*1e-15,low:Number(bar.low)*1e-15,close:Number(bar.close)*1e-15}));
  layer.update(input,[config('tiny','macd',{fast:3,slow:6,signal:2})],'1m');
  const series=[...h.panes[1].series];
  const formats=series.map(item=>item.applyOptions.mock.calls.find(call=>call[0].priceFormat)?.[0].priceFormat);
  expect(formats.every(format=>format&&format.type==='custom'&&format.minMove>0&&format.minMove<1e-15)).toBe(true);
  expect(formats[0]).toBe(formats[1]);expect(formats[1]).toBe(formats[2]);
  expect(formats[0].base).toBe(10**Math.round(-Math.log10(formats[0].minMove)));
  expect(formats[0].formatter(1.23456789e-15)).toBe('1.23457e-15');expect(formats[0].formatter(0)).toBe('0');
  expect(series[1].data.at(-1)!.value).toBeCloseTo(1.5e-15,28);
 });
 it.each(['sma','ema'] as const)('shows the first valid %s average as a point and switches to a line when another bar arrives',type=>{
  const h=chartHarness(),layer=new IndicatorLayer(h.chart,h.main),configs=[config('average',type,{period:3})];
  layer.update(candles.slice(0,2),configs,'1m');const average=[...h.panes[0].series][1];
  expect(average.data).toEqual([]);expect(average.applyOptions).toHaveBeenLastCalledWith({pointMarkersVisible:false,pointMarkersRadius:3,lineWidth:2});
  layer.update(candles.slice(0,3),configs,'1m');expect(average.data).toEqual([{time:candles[2].time,value:12}]);expect(average.applyOptions).toHaveBeenLastCalledWith({pointMarkersVisible:true,pointMarkersRadius:3,lineWidth:2});
  layer.update(candles.slice(0,4),configs,'1m');expect(average.data).toEqual([{time:candles[2].time,value:12},{time:candles[3].time,value:13}]);expect(average.applyOptions).toHaveBeenLastCalledWith({pointMarkersVisible:false,pointMarkersRadius:3,lineWidth:2});expect(h.panes[0].series.size).toBe(2);
 });
});
