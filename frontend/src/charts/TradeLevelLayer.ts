import {LineStyle,type IPriceLine,type ISeriesApi,type SeriesType} from 'lightweight-charts';
import type {ThemePalette} from '../workspace/themes';
import type {TradeLevel} from '../workspace/chartTrades';

/** Native price lines belong to the displayed price series and never to drawings. */
export class TradeLevelLayer {
 private lines=new Map<string,{line:IPriceLine;signature:string}>();
 constructor(private series:ISeriesApi<SeriesType>){}
 setSeries(series:ISeriesApi<SeriesType>){if(series!==this.series){this.dispose();this.series=series;}}
 update(levels:TradeLevel[],palette:ThemePalette){
  const ids=new Set(levels.map(level=>level.id));
  for(const [id,{line}] of this.lines)if(!ids.has(id)){this.series.removePriceLine(line);this.lines.delete(id);}
  for(const level of levels){
   const options={price:level.price,title:level.title,color:level.kind==='sl'?palette.danger:level.kind==='tp'?palette.up:palette.accent,lineWidth:1 as const,lineStyle:level.kind==='entry'?LineStyle.Solid:LineStyle.Dashed,axisLabelVisible:true,lineVisible:true,axisLabelTextColor:palette.chartBackground};
   const signature=JSON.stringify(options),previous=this.lines.get(level.id);if(previous?.signature===signature)continue;
   if(previous){previous.line.applyOptions(options);previous.signature=signature;}
   else this.lines.set(level.id,{line:this.series.createPriceLine(options),signature});
  }
 }
 dispose(){for(const {line} of this.lines.values())this.series.removePriceLine(line);this.lines.clear();}
}
