import {describe,it,expect,vi} from 'vitest';
import type {IChartApi,ISeriesApi} from 'lightweight-charts';
import type {Candle,IndicatorConfig} from '../../types';
import {IndicatorLayer} from './IndicatorLayer';

function chartHarness(){
 type FakeSeries={data:{time:number;value:number;color?:string}[];options:Record<string,unknown>;setData:ReturnType<typeof vi.fn>;applyOptions:ReturnType<typeof vi.fn>;createPriceLine:ReturnType<typeof vi.fn>;priceScale:()=>{applyOptions:ReturnType<typeof vi.fn>};attachPrimitive:ReturnType<typeof vi.fn>;detachPrimitive:ReturnType<typeof vi.fn>;priceToCoordinate:(price:number)=>number};
 type Pane={series:Set<FakeSeries>;factor:number;paneIndex:()=>number;setStretchFactor:ReturnType<typeof vi.fn>;getStretchFactor:()=>number;getHTMLElement:()=>null};
 const panes:Pane[]=[],removed:unknown[]=[],range={from:1000,to:4500},restore=vi.fn(),scale={getVisibleRange:()=>range,setVisibleRange:restore,timeToCoordinate:(time:number)=>time};
 function makePane():Pane{const pane:Pane={series:new Set(),factor:panes.length?1:2,paneIndex:()=>panes.indexOf(pane),setStretchFactor:vi.fn((factor:number)=>{pane.factor=factor;}),getStretchFactor:()=>pane.factor,getHTMLElement:()=>null};panes.push(pane);return pane;}
 function makeSeries(options:unknown,pane:Pane):FakeSeries{
  const series:FakeSeries={data:[],options:options as Record<string,unknown>,setData:vi.fn((data:{time:number;value:number;color?:string}[])=>{series.data=data;}),applyOptions:vi.fn(),createPriceLine:vi.fn(),priceScale:()=>({applyOptions:vi.fn()}),attachPrimitive:vi.fn((primitive:{attached:(value:unknown)=>void})=>primitive.attached({chart,series,requestUpdate:vi.fn()})),detachPrimitive:vi.fn((primitive:{detached:()=>void})=>primitive.detached()),priceToCoordinate:(price:number)=>price};pane.series.add(series);return series;
 }
 const first=makePane(),main=makeSeries({},first);
 const chart={timeScale:()=>scale,panes:()=>panes,addPane:makePane,addSeries:vi.fn((_definition:unknown,options:unknown,index=0)=>makeSeries(options,panes[index])),removeSeries:vi.fn((series:ReturnType<typeof makeSeries>)=>{removed.push(series);const index=panes.findIndex(pane=>pane.series.has(series));if(index<0)throw Error('Removing an already removed series');panes[index].series.delete(series);if(index>0&&!panes[index].series.size)panes.splice(index,1);})};
 return {chart:chart as unknown as IChartApi,main:main as unknown as ISeriesApi<'Candlestick'>,panes,removed,restore};
}
const candles:Candle[]=Array.from({length:80},(_,i)=>({time:1000+i*60,open:i+10,high:i+12,low:i+9,close:i+11,volume:5,closed:true,quality:'authoritative',sourceRevision:1}));
const config=(id:string,type:IndicatorConfig['type'],parameters:IndicatorConfig['parameters']={}):IndicatorConfig=>({id,type,parameters});

describe('indicator series ownership',()=>{
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
